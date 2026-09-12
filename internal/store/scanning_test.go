package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newScanFixture(t *testing.T) (*Store, Scope, ScanPolicy) {
	t.Helper()
	ctx := context.Background()
	s := NewMemory()
	now := time.Date(2026, 9, 12, 14, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	site, err := s.CreateSite(ctx, Site{Name: "scan-store"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.CreateScope(ctx, Scope{
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
	policy, err := s.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, scope, policy
}

func scanRunFixture(scope Scope, policy ScanPolicy, idempotency string, scheduledAt time.Time) ScanRun {
	return ScanRun{
		ScopeID:             scope.ID,
		ScopeRevision:       policy.Revision,
		ScannerKind:         "server",
		ScannerID:           "control-server-1",
		Trigger:             "owner",
		ScheduledAt:         scheduledAt,
		AssignmentExpiresAt: scheduledAt.Add(10 * time.Minute),
		PolicySnapshot: ScanPolicySnapshot{
			Ranges:      append([]string(nil), scope.Ranges...),
			Exclusions:  append([]string(nil), scope.Exclusions...),
			EntryPoints: append([]ScanEntryPoint(nil), policy.EntryPoints...),
			Limits:      policy.Limits,
		},
		TargetsPlanned:  2,
		AttemptsPlanned: 2,
		IdempotencyKey:  idempotency,
	}
}

func TestScanPolicyRevisionAndDisabledDefault(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := newScanFixture(t)
	if policy.ScopeID != scope.ID || policy.Enabled != scope.Enabled || policy.ServerEnabled {
		t.Fatalf("unexpected default scan policy: %+v", policy)
	}
	if len(policy.AgentIDs) != 0 || len(policy.EntryPoints) != 1 || policy.EntryPoints[0].Port != 22 {
		t.Fatalf("unexpected default scan policy contents: %+v", policy)
	}
	policy.ServerEnabled = true
	updated, err := s.UpdateScanPolicy(ctx, scope.ID, policy.Revision, policy)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != policy.Revision+1 || !updated.ServerEnabled {
		t.Fatalf("policy revision did not advance: %+v", updated)
	}
	if _, err := s.UpdateScanPolicy(ctx, scope.ID, policy.Revision, policy); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale policy revision was accepted: %v", err)
	}
}

func TestScanRunsEnforceActiveUniquenessAndIdempotency(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := newScanFixture(t)
	now := s.Now()
	first, err := s.CreateScanRun(ctx, scanRunFixture(scope, policy, "run-once", now))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateScanRun(ctx, scanRunFixture(scope, policy, "run-once", now.Add(time.Second)))
	if err != nil || replay.ID != first.ID {
		t.Fatalf("idempotent run replay: %+v %v", replay, err)
	}
	duplicate := scanRunFixture(scope, policy, "run-two", now.Add(time.Second))
	if _, err := s.CreateScanRun(ctx, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("active scope/vantage duplicate was accepted: %v", err)
	}
	leased, err := s.LeaseScanRun(ctx, first.ID, "coordinator-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if leased.State != "leased" || leased.LeaseEpoch != 1 {
		t.Fatalf("unexpected first lease: %+v", leased)
	}
	if _, err := s.StartScanRun(ctx, first.ID, "coordinator-b", leased.LeaseEpoch); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong lease owner started run: %v", err)
	}
	if _, err := s.StartScanRun(ctx, first.ID, "coordinator-a", leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinalizeScanRun(ctx, first.ID, "coordinator-a", leased.LeaseEpoch, "completed", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateScanRun(ctx, duplicate); err != nil {
		t.Fatalf("new run after terminal transition: %v", err)
	}
}

func TestScanLeaseExpiryFencesLateExecutor(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := newScanFixture(t)
	now := s.Now()
	run, err := s.CreateScanRun(ctx, scanRunFixture(scope, policy, "lease", now))
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.LeaseScanRun(ctx, run.ID, "coordinator-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	currentNow := now
	s.SetClock(func() time.Time { return currentNow })
	currentNow = currentNow.Add(2 * time.Minute)
	second, err := s.LeaseScanRun(ctx, run.ID, "coordinator-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.LeaseEpoch != first.LeaseEpoch+1 || second.LeaseOwner != "coordinator-b" {
		t.Fatalf("expired lease was not re-fenced: first=%+v second=%+v", first, second)
	}
	if _, err := s.StartScanRun(ctx, run.ID, "coordinator-a", first.LeaseEpoch); !errors.Is(err, ErrConflict) {
		t.Fatalf("late executor crossed lease fence: %v", err)
	}
}

func TestScanResultPagesAreIdempotentAndTerminalStateIsFenced(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := newScanFixture(t)
	now := s.Now()
	run, err := s.CreateScanRun(ctx, scanRunFixture(scope, policy, "pages", now))
	if err != nil {
		t.Fatal(err)
	}
	leased, err := s.LeaseScanRun(ctx, run.ID, "coordinator", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartScanRun(ctx, run.ID, "coordinator", leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	latency := 4.0
	receipt := ScanResultReceipt{RunID: run.ID, PageOrdinal: 0, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResultCount: 1, IsFinal: true}
	observation := EntryPointObservation{
		ID:                  NewID(),
		RunID:               run.ID,
		PageOrdinal:         0,
		ScopeID:             scope.ID,
		ScopeRevision:       policy.Revision,
		ScannerKind:         "server",
		ScannerID:           "control-server-1",
		Address:             "192.0.2.10",
		Transport:           "tcp",
		Port:                22,
		EntryPointID:        policy.EntryPoints[0].ID,
		Outcome:             "open",
		LatencyMilliseconds: &latency,
		ObservedAt:          now,
		ReceivedAt:          now,
		ExpiresAt:           now.Add(30 * 24 * time.Hour),
		Actionable:          true,
	}
	accepted, duplicate, err := s.AcceptScanResultPage(ctx, receipt, []EntryPointObservation{observation})
	if err != nil || duplicate || accepted.ResultCount != 1 {
		t.Fatalf("first page acceptance: %+v duplicate=%t err=%v", accepted, duplicate, err)
	}
	replayed, duplicate, err := s.AcceptScanResultPage(ctx, receipt, []EntryPointObservation{observation})
	if err != nil || !duplicate || replayed.ContentHash != receipt.ContentHash {
		t.Fatalf("identical replay: %+v duplicate=%t err=%v", replayed, duplicate, err)
	}
	conflict := receipt
	conflict.ContentHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, _, err := s.AcceptScanResultPage(ctx, conflict, []EntryPointObservation{observation}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting page replay was accepted: %v", err)
	}
	if _, err := s.FinalizeScanRun(ctx, run.ID, "coordinator", leased.LeaseEpoch, "completed", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcceptScanResultPage(ctx, receipt, []EntryPointObservation{observation}); !errors.Is(err, ErrConflict) {
		t.Fatalf("page was accepted after terminal transition: %v", err)
	}
}

func TestScanRunAndObservationPaginationIsBounded(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := newScanFixture(t)
	now := s.Now()
	for i := 0; i < 3; i++ {
		run := scanRunFixture(scope, policy, "", now.Add(time.Duration(i)*time.Hour))
		run.ScannerID = "server-" + string(rune('a'+i))
		created, err := s.CreateScanRun(ctx, run)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.LeaseScanRun(ctx, created.ID, "coordinator", time.Minute); err != nil {
			t.Fatal(err)
		}
		leased, _ := s.GetScanRun(ctx, created.ID)
		if _, err := s.StartScanRun(ctx, created.ID, "coordinator", leased.LeaseEpoch); err != nil {
			t.Fatal(err)
		}
		if _, err := s.FinalizeScanRun(ctx, created.ID, "coordinator", leased.LeaseEpoch, "completed", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListScanRuns(ctx, ScanRunQuery{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("bounded run page: %+v err=%v", page, err)
	}
	page, err = s.ListScanRuns(ctx, ScanRunQuery{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("cursor run page: %+v err=%v", page, err)
	}
}
