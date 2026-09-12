package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

type scanIngestionFixture struct {
	service *Service
	store   *store.Store
	scope   store.Scope
	policy  store.ScanPolicy
	run     store.ScanRun
	scanner policy.ScanVantage
	now     time.Time
}

func newScanIngestionFixture(t *testing.T) scanIngestionFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	repository := store.NewMemory()
	repository.SetClock(func() time.Time { return now })
	site, err := repository.CreateSite(ctx, store.Site{Name: "scan-ingestion"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{
		SiteID:         site.ID,
		Ranges:         []string{"192.0.2.0/24"},
		Exclusions:     []string{"192.0.2.9"},
		AllowedMethods: []string{"tcp"},
		Ports:          []int{22},
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{DisplayName: "scan-agent", SiteID: site.ID, Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	scanner := policy.ScanVantage{Kind: "agent", ID: "agent-scan-1", DeviceID: device.ID}
	if err := repository.CreateAgentIdentity(ctx, store.AgentIdentity{ID: scanner.ID, DeviceID: device.ID, AuthTokenHash: store.HashToken("scan-token"), ExpiresAt: now.Add(24 * time.Hour), InstalledVersion: "0.1.0"}); err != nil {
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
	run, err := repository.CreateScanRun(ctx, store.ScanRun{
		ID:                  "scan-run-1",
		ScopeID:             scope.ID,
		ScopeRevision:       scanPolicy.Revision,
		ScannerKind:         scanner.Kind,
		ScannerID:           scanner.ID,
		Trigger:             "owner",
		ScheduledAt:         now.Add(-time.Minute),
		AssignmentExpiresAt: now.Add(5 * time.Minute),
		PolicySnapshot: store.ScanPolicySnapshot{
			Ranges:      scope.Ranges,
			Exclusions:  scope.Exclusions,
			EntryPoints: scanPolicy.EntryPoints,
			Limits:      scanPolicy.Limits,
		},
		TargetsPlanned:  1,
		AttemptsPlanned: 1,
	})
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
	return scanIngestionFixture{service: &Service{Store: repository, Policy: &policy.Engine{Store: repository}, Now: func() time.Time { return now }}, store: repository, scope: scope, policy: scanPolicy, run: run, scanner: scanner, now: now}
}

func validScanResultPage(fixture scanIngestionFixture) ScanResultPage {
	return ScanResultPage{
		ProtocolVersion: 1,
		RunID:           fixture.run.ID,
		LeaseEpoch:      fixture.run.LeaseEpoch,
		ScopeRevision:   fixture.run.ScopeRevision,
		PageOrdinal:     0,
		ObservedFrom:    fixture.now.Add(-2 * time.Second),
		ObservedTo:      fixture.now,
		Final:           true,
		Results: []ScanResult{{
			Address:      "192.0.2.10",
			EntryPointID: fixture.policy.EntryPoints[0].ID,
			Transport:    store.ScanTransportTCP,
			Port:         22,
			Outcome:      "open",
			ObservedAt:   fixture.now,
		}},
		Summary: &ScanResultSummary{TargetsPlanned: 1, AttemptsPlanned: 1, AttemptsCompleted: 1},
	}
}

func TestIngestScanResultPageAcceptsAuthenticatedBoundedResultWithoutAction(t *testing.T) {
	ctx := context.Background()
	fixture := newScanIngestionFixture(t)
	receipt, duplicate, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, validScanResultPage(fixture))
	if err != nil || duplicate || receipt.ResultCount != 1 {
		t.Fatalf("first result page: %+v duplicate=%t err=%v", receipt, duplicate, err)
	}
	run, err := fixture.store.GetScanRun(ctx, fixture.run.ID)
	if err != nil || run.State != store.ScanRunCompleted || run.AttemptsCompleted != 1 {
		t.Fatalf("run was not completed: %+v err=%v", run, err)
	}
	observations, err := fixture.store.ListEntryPointObservations(ctx, store.EntryPointObservationQuery{RunID: fixture.run.ID})
	if err != nil || len(observations.Items) != 1 || observations.Items[0].Actionable {
		t.Fatalf("unexpected stored observation: %+v err=%v", observations, err)
	}
	candidates, err := fixture.store.ListCandidates(ctx, fixture.scope.ID, "")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("result ingestion created candidate action: %+v err=%v", candidates, err)
	}
	requests, err := fixture.store.ListAccessRequests(ctx, "", "")
	if err != nil || len(requests) != 0 {
		t.Fatalf("result ingestion created access request: %+v err=%v", requests, err)
	}
	jobs, err := fixture.store.ListJobs(ctx, "", "", "")
	if err != nil || len(jobs) != 0 {
		t.Fatalf("result ingestion created job: %+v err=%v", jobs, err)
	}
}

func TestIngestScanResultPageReplaysIdenticallyAndRejectsConflicts(t *testing.T) {
	ctx := context.Background()
	fixture := newScanIngestionFixture(t)
	page := validScanResultPage(fixture)
	first, duplicate, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, page)
	if err != nil || duplicate {
		t.Fatalf("first result page: %+v duplicate=%t err=%v", first, duplicate, err)
	}
	replayed, duplicate, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, page)
	if err != nil || !duplicate || replayed.ContentHash != first.ContentHash {
		t.Fatalf("identical replay: %+v duplicate=%t err=%v", replayed, duplicate, err)
	}
	fixture = newScanIngestionFixture(t)
	page = validScanResultPage(fixture)
	if _, _, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, page); err != nil {
		t.Fatalf("seed result page: %v", err)
	}
	page.ContentHash = "conflicting-content-hash"
	if _, _, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, page); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflicting content hash was accepted: %v", err)
	}
}

func TestIngestScanResultPageRejectsWrongScannerExpiredAndUnsafeResults(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		change func(*scanIngestionFixture, *ScanResultPage)
		want   error
	}{
		{name: "wrong scanner", change: func(fixture *scanIngestionFixture, _ *ScanResultPage) { fixture.scanner.ID = "other-agent" }, want: store.ErrForbidden},
		{name: "wrong epoch", change: func(_ *scanIngestionFixture, page *ScanResultPage) { page.LeaseEpoch++ }, want: store.ErrConflict},
		{name: "excluded", change: func(_ *scanIngestionFixture, page *ScanResultPage) { page.Results[0].Address = "192.0.2.9" }, want: store.ErrForbidden},
		{name: "outside scope", change: func(_ *scanIngestionFixture, page *ScanResultPage) { page.Results[0].Address = "198.51.100.10" }, want: store.ErrForbidden},
		{name: "unsafe reason", change: func(_ *scanIngestionFixture, page *ScanResultPage) { page.Results[0].ReasonCode = "remote-error" }, want: store.ErrInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newScanIngestionFixture(t)
			page := validScanResultPage(fixture)
			testCase.change(&fixture, &page)
			if _, _, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, page); !errors.Is(err, testCase.want) {
				t.Fatalf("got error %v, want %v", err, testCase.want)
			}
			observations, err := fixture.store.ListEntryPointObservations(ctx, store.EntryPointObservationQuery{RunID: fixture.run.ID})
			if err != nil || len(observations.Items) != 0 {
				t.Fatalf("rejected page left evidence: %+v err=%v", observations, err)
			}
		})
	}

	fixture := newScanIngestionFixture(t)
	page := validScanResultPage(fixture)
	fixture.store.SetClock(func() time.Time { return fixture.now.Add(2 * time.Hour) })
	if _, _, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, page); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expired assignment accepted: %v", err)
	}

	fixture = newScanIngestionFixture(t)
	currentPolicy, err := fixture.store.ScanPolicy(ctx, fixture.scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.UpdateScanPolicy(ctx, fixture.scope.ID, currentPolicy.Revision, currentPolicy); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, validScanResultPage(fixture)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("superseded policy accepted: %v", err)
	}

	fixture = newScanIngestionFixture(t)
	if err := fixture.store.RevokeAgent(ctx, fixture.scanner.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.service.IngestScanResultPage(ctx, fixture.scanner, validScanResultPage(fixture)); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("revoked scanner accepted: %v", err)
	}
}
