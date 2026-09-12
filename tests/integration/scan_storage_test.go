package integration

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

func TestSQLScanStorageIsIdempotentFencedAndRestartable(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	var tableCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_catalog.pg_tables WHERE schemaname='public' AND tablename IN ('scan_policies','scan_vantage_assignments','scan_runs','scan_run_leases','scan_result_receipts','scan_entry_point_observations','scan_entry_point_current','scan_candidate_extensions','scan_access_request_keys')`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 9 {
		t.Fatalf("active scanning migration created %d tables, want 9", tableCount)
	}

	first := store.NewSQL(db)
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	first.SetClock(func() time.Time { return now })
	site, err := first.CreateSite(ctx, store.Site{ID: store.NewID(), Name: "sql-scan-storage"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := first.CreateScope(ctx, store.Scope{ID: store.NewID(), SiteID: site.ID, Ranges: []string{"198.18.0.0/24"}, Exclusions: []string{"198.18.0.9"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := first.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := first.CreateScanRun(ctx, store.ScanRun{ID: store.NewID(), ScopeID: scope.ID, ScopeRevision: scanPolicy.Revision, ScannerKind: "server", ScannerID: "sql-server", ScheduledAt: now, AssignmentExpiresAt: now.Add(10 * time.Minute), PolicySnapshot: store.ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: scanPolicy.EntryPoints, Limits: scanPolicy.Limits}, TargetsPlanned: 1, AttemptsPlanned: 1})
	if err != nil {
		t.Fatal(err)
	}

	second := store.NewSQL(db)
	second.SetClock(func() time.Time { return now })
	leases := make(chan error, 2)
	var group sync.WaitGroup
	for _, repository := range []*store.Store{first, second} {
		group.Add(1)
		go func(repository *store.Store) {
			defer group.Done()
			_, leaseErr := repository.LeaseScanRun(ctx, run.ID, "sql-server", time.Minute)
			leases <- leaseErr
		}(repository)
	}
	group.Wait()
	close(leases)
	successes := 0
	conflicts := 0
	for leaseErr := range leases {
		if leaseErr == nil {
			successes++
		} else if errors.Is(leaseErr, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected SQL concurrent lease error: %v", leaseErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("SQL concurrent lease result: successes=%d conflicts=%d", successes, conflicts)
	}

	restarted := store.NewSQL(db)
	restarted.SetClock(func() time.Time { return now })
	leased, err := restarted.GetScanRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leased.LeaseOwner != "sql-server" || leased.LeaseEpoch != 1 {
		t.Fatalf("lease did not survive restart: %+v", leased)
	}
	if _, err := restarted.StartScanRun(ctx, run.ID, leased.LeaseOwner, leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	observation := store.EntryPointObservation{RunID: run.ID, PageOrdinal: 0, ScopeID: scope.ID, ScopeRevision: run.ScopeRevision, ScannerKind: "server", ScannerID: "sql-server", Address: "198.18.0.10", Transport: store.ScanTransportTCP, Port: 22, EntryPointID: scanPolicy.EntryPoints[0].ID, Outcome: "open", ObservedAt: now, ReceivedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	receipt := store.ScanResultReceipt{RunID: run.ID, PageOrdinal: 0, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResultCount: 1, IsFinal: true, AcceptedAt: now}
	accepted, duplicate, err := restarted.AcceptScanResultPage(ctx, receipt, []store.EntryPointObservation{observation})
	if err != nil || duplicate || accepted.ResultCount != 1 {
		t.Fatalf("SQL page acceptance: %+v duplicate=%t err=%v", accepted, duplicate, err)
	}
	replayed, duplicate, err := restarted.AcceptScanResultPage(ctx, receipt, []store.EntryPointObservation{observation})
	if err != nil || !duplicate || replayed.ContentHash != receipt.ContentHash {
		t.Fatalf("SQL identical page replay: %+v duplicate=%t err=%v", replayed, duplicate, err)
	}
	conflicting := receipt
	conflicting.ContentHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, _, err := restarted.AcceptScanResultPage(ctx, conflicting, []store.EntryPointObservation{observation}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("SQL conflicting page replay was accepted: %v", err)
	}
	if _, err := restarted.FinalizeScanRun(ctx, run.ID, leased.LeaseOwner, leased.LeaseEpoch, store.ScanRunCompleted, "", ""); err != nil {
		t.Fatal(err)
	}
	third := store.NewSQL(db)
	third.SetClock(func() time.Time { return now })
	final, err := third.GetScanRun(ctx, run.ID)
	if err != nil || final.State != store.ScanRunCompleted || final.AttemptsCompleted != 1 {
		t.Fatalf("final run did not survive restart: %+v err=%v", final, err)
	}
	replayed, duplicate, err = third.AcceptScanResultPage(ctx, receipt, []store.EntryPointObservation{observation})
	if !errors.Is(err, store.ErrConflict) || duplicate || replayed.ContentHash != "" {
		t.Fatalf("terminal SQL replay was accepted: %+v duplicate=%t err=%v", replayed, duplicate, err)
	}

}

type sqlScanAgentFixture struct {
	repository *store.Store
	service    *discovery.Service
	scope      store.Scope
	policy     store.ScanPolicy
	run        store.ScanRun
	scanner    policy.ScanVantage
	now        *time.Time
}

func newSQLScanAgentFixture(t *testing.T, db *sql.DB, id string) sqlScanAgentFixture {
	t.Helper()
	ctx := context.Background()
	clock := time.Date(2026, 9, 12, 19, 0, 0, 0, time.UTC)
	now := &clock
	repository := store.NewSQL(db)
	repository.SetClock(func() time.Time { return *now })
	site, err := repository.CreateSite(ctx, store.Site{ID: store.NewID(), Name: "sql-scan-agent-" + id})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{ID: store.NewID(), SiteID: site.ID, Ranges: []string{"198.19.0.0/24"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{ID: store.NewID(), SiteID: site.ID, DisplayName: "sql-scan-agent-" + id, Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	scanner := policy.ScanVantage{Kind: "agent", ID: "sql-scan-agent-" + id, DeviceID: device.ID}
	if err := repository.CreateAgentIdentity(ctx, store.AgentIdentity{ID: scanner.ID, DeviceID: device.ID, ExpiresAt: clock.Add(time.Hour), Capabilities: store.ScanCapabilities{ScanProtocolVersions: []int{1}, ScanTransports: []string{store.ScanTransportTCP}}}); err != nil {
		t.Fatal(err)
	}
	scanPolicy, err := repository.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanPolicy.AgentIDs = []string{scanner.ID}
	scanPolicy, err = repository.UpdateScanPolicy(ctx, scope.ID, scanPolicy.Revision, scanPolicy)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repository.CreateScanRun(ctx, store.ScanRun{ID: store.NewID(), ScopeID: scope.ID, ScopeRevision: scanPolicy.Revision, ScannerKind: scanner.Kind, ScannerID: scanner.ID, ScheduledAt: clock, AssignmentExpiresAt: clock.Add(time.Minute), PolicySnapshot: store.ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: scanPolicy.EntryPoints, Limits: scanPolicy.Limits}, TargetsPlanned: 1, AttemptsPlanned: 1})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := repository.LeaseScanRun(ctx, run.ID, scanner.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	run, err = repository.StartScanRun(ctx, run.ID, scanner.ID, leased.LeaseEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return sqlScanAgentFixture{repository: repository, service: &discovery.Service{Store: repository, Policy: &policy.Engine{Store: repository}, Now: repository.Now}, scope: scope, policy: scanPolicy, run: run, scanner: scanner, now: now}
}

func sqlScanPage(fixture sqlScanAgentFixture) discovery.ScanResultPage {
	return discovery.ScanResultPage{ProtocolVersion: 1, RunID: fixture.run.ID, LeaseEpoch: fixture.run.LeaseEpoch, ScopeRevision: fixture.run.ScopeRevision, PageOrdinal: 0, ObservedFrom: fixture.now.Add(-time.Second), ObservedTo: *fixture.now, Final: true, Results: []discovery.ScanResult{{Address: "198.19.0.10", EntryPointID: fixture.policy.EntryPoints[0].ID, Transport: store.ScanTransportTCP, Port: 22, Outcome: "open", ObservedAt: *fixture.now}}, Summary: &discovery.ScanResultSummary{TargetsPlanned: 1, AttemptsPlanned: 1, AttemptsCompleted: 1}}
}

func TestSQLScanIngestionFencesWrongScannerSupersededExpiryAndRevocation(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	fixture := newSQLScanAgentFixture(t, db, "fences")
	if _, _, err := fixture.service.IngestScanResultPage(ctx, policy.ScanVantage{Kind: "agent", ID: "other-agent", DeviceID: "other-device"}, sqlScanPage(fixture)); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("wrong scanner was accepted: %v", err)
	}
	currentPolicy, err := fixture.repository.ScanPolicy(ctx, fixture.scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.UpdateScanPolicy(ctx, fixture.scope.ID, currentPolicy.Revision, currentPolicy); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, sqlScanPage(fixture)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("superseded SQL scan was accepted: %v", err)
	}

	expired := newSQLScanAgentFixture(t, db, "expiry")
	*expired.now = expired.now.Add(2 * time.Minute)
	if _, _, err := expired.service.IngestScanResultPage(ctx, expired.scanner, sqlScanPage(expired)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expired SQL scan was accepted: %v", err)
	}

	revoked := newSQLScanAgentFixture(t, db, "revoked")
	if err := revoked.repository.RevokeAgent(ctx, revoked.scanner.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := revoked.service.IngestScanResultPage(ctx, revoked.scanner, sqlScanPage(revoked)); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("revoked SQL scanner was accepted: %v", err)
	}
}
