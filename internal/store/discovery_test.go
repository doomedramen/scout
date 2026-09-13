package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPersistedDiscoveryKeysAreJSONSafe(t *testing.T) {
	keys := []string{
		candidateKey("scope\x00id", "2001:db8::10"),
		scanAccessRequestDedupeKey("candidate", ScanAccessSSH, "2001:db8::10:22"),
	}
	for _, key := range keys {
		if strings.ContainsRune(key, '\x00') {
			t.Fatalf("composite key contains a NUL byte: %q", key)
		}
		encoded, err := json.Marshal(map[string]any{key: true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "\\u0000") {
			t.Fatalf("JSON encoded composite key contains a NUL escape: %s", encoded)
		}
	}
}

func TestCandidatesDeduplicateAndPreserveExclusions(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	site, err := s.CreateSite(ctx, Site{Name: "discovery"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.CreateScope(ctx, Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, Exclusions: []string{"192.0.2.9"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.UpsertCandidate(ctx, Candidate{ScopeID: scope.ID, Address: "192.0.2.8", Source: "vantage-a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.UpsertCandidate(ctx, Candidate{ScopeID: scope.ID, Address: "192.0.2.8", Source: "vantage-b", LastSeen: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate sighting created two candidates: %s %s", first.ID, second.ID)
	}
	if first.State != "discovered" {
		t.Fatalf("candidate default state = %q, want discovered", first.State)
	}
	excluded, err := s.UpsertCandidate(ctx, Candidate{ScopeID: scope.ID, Address: "192.0.2.9", Source: "vantage-a"})
	if err != nil {
		t.Fatal(err)
	}
	if !excluded.Excluded || excluded.State != "excluded" {
		t.Fatalf("scope exclusion was not persistent: %+v", excluded)
	}
	items, err := s.ListCandidates(ctx, scope.ID, "")
	if err != nil || len(items) != 2 {
		t.Fatalf("candidate list: %d %v", len(items), err)
	}
}

func TestScanHistoryCleanupPreservesUnresolvedEvidenceAndResumesInBatches(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := newScanFixture(t)
	now := s.Now()
	old := now.Add(-31 * 24 * time.Hour)
	veryOld := now.Add(-91 * 24 * time.Hour)
	protectedRun := ScanRun{
		ID: "protected-scan-run", ScopeID: scope.ID, ScopeRevision: policy.Revision, ScannerKind: "server", ScannerID: "control-server-1",
		Trigger: "schedule", State: ScanRunCompleted, ScheduledAt: veryOld, FinishedAt: timePtr(veryOld.Add(time.Minute)), AssignmentExpiresAt: veryOld.Add(10 * time.Minute),
		PolicySnapshot: ScanPolicySnapshot{Ranges: scope.Ranges, Exclusions: scope.Exclusions, EntryPoints: policy.EntryPoints, Limits: policy.Limits}, TargetsPlanned: 1, AttemptsPlanned: 1,
	}
	removableRun := protectedRun
	removableRun.ID = "removable-scan-run"
	removableRun.FinishedAt = timePtr(veryOld.Add(2 * time.Minute))
	protectedObservation := EntryPointObservation{
		ID: "protected-observation", RunID: protectedRun.ID, PageOrdinal: 0, ScopeID: scope.ID, ScopeRevision: policy.Revision,
		ScannerKind: "server", ScannerID: protectedRun.ScannerID, Address: "192.0.2.10", Transport: ScanTransportTCP, Port: 22,
		EntryPointID: policy.EntryPoints[0].ID, Outcome: "open", ObservedAt: old, ReceivedAt: old, ExpiresAt: old.Add(24 * time.Hour), Actionable: true,
	}
	removableObservation := protectedObservation
	removableObservation.ID = "removable-observation"
	removableObservation.RunID = removableRun.ID
	removableObservation.Address = "192.0.2.11"
	candidate := Candidate{ID: "candidate-with-open-request", ScopeID: scope.ID, SiteID: scope.SiteID, Address: protectedObservation.Address, Source: "active-scan", State: "needs_credentials", FirstSeen: old, LastSeen: old, ExpiresAt: old.Add(15 * time.Minute), EvidenceIDs: []string{protectedObservation.ID}}
	request := AccessRequest{ID: "open-request", CandidateID: candidate.ID, DeviceID: candidate.ID, ScopeID: scope.ID, AccessMethod: ScanAccessSSH, Endpoint: "192.0.2.10:22", EntryPointObservationID: protectedObservation.ID, ReasonCode: "missing_credentials", State: "open", LastAttempt: old, SafeDetails: map[string]string{"target": "192.0.2.10:22"}}
	if err := s.mutate(ctx, func(state *State) error {
		state.ScanRuns[protectedRun.ID] = protectedRun
		state.ScanRuns[removableRun.ID] = removableRun
		state.ScanResultReceipts[scanReceiptKey(protectedRun.ID, 0)] = ScanResultReceipt{RunID: protectedRun.ID, PageOrdinal: 0, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AcceptedAt: old, ResultCount: 1, IsFinal: true}
		state.ScanResultReceipts[scanReceiptKey(removableRun.ID, 0)] = ScanResultReceipt{RunID: removableRun.ID, PageOrdinal: 0, ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", AcceptedAt: old, ResultCount: 1, IsFinal: true}
		state.EntryPointObservations[protectedObservation.ID] = protectedObservation
		state.EntryPointObservations[removableObservation.ID] = removableObservation
		state.Candidates[candidateKey(scope.ID, candidate.Address)] = candidate
		state.AccessRequests[request.ID] = request
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	first, err := s.CleanupScanHistory(ctx, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.ObservationsDeleted != 1 || first.ReceiptsDeleted != 0 || first.RunsDeleted != 0 || !first.MoreWork {
		t.Fatalf("first cleanup batch = %+v, want one evidence deletion and more work", first)
	}
	second, err := s.CleanupScanHistory(ctx, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if second.ObservationsDeleted != 0 || second.ReceiptsDeleted != 1 || second.RunsDeleted != 1 || second.MoreWork || !second.Blocked {
		t.Fatalf("resumed cleanup batch = %+v", second)
	}
	if _, err := s.GetEntryPointObservation(ctx, protectedObservation.ID); err != nil {
		t.Fatalf("unresolved-request evidence was deleted: %v", err)
	}
	if _, err := s.GetScanRun(ctx, protectedRun.ID); err != nil {
		t.Fatalf("run holding unresolved evidence was deleted: %v", err)
	}
	if _, err := s.GetCandidate(ctx, candidate.ID); err != nil {
		t.Fatalf("candidate was deleted during cleanup: %v", err)
	}
	requests, err := s.ListAccessRequestsForCandidate(ctx, candidate.ID)
	if err != nil || len(requests) != 1 || requests[0].State != "open" {
		t.Fatalf("open request after cleanup = %+v err=%v", requests, err)
	}
	if _, err := s.GetEntryPointObservation(ctx, removableObservation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removable evidence survived cleanup: %v", err)
	}
	if _, err := s.GetScanRun(ctx, removableRun.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removable run survived cleanup: %v", err)
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func TestCurrentEntryPointFilterKeepsNewestVantageEvidenceAndContradiction(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := newScanFixture(t)
	now := s.Now()
	accept := func(id, observedScanner string, observedAt time.Time, outcome string) {
		runInput := scanRunFixture(scope, policy, id, observedAt)
		runInput.ScannerID = observedScanner
		runInput.TargetsPlanned = 1
		runInput.AttemptsPlanned = 1
		run, err := s.CreateScanRun(ctx, runInput)
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
		observation := EntryPointObservation{
			ID: NewID(), RunID: run.ID, PageOrdinal: 0, ScopeID: scope.ID, ScopeRevision: policy.Revision,
			ScannerKind: "server", ScannerID: observedScanner, Address: "192.0.2.10", Transport: ScanTransportTCP,
			Port: 22, EntryPointID: policy.EntryPoints[0].ID, Outcome: outcome, ObservedAt: observedAt,
		}
		receipt := ScanResultReceipt{RunID: run.ID, PageOrdinal: 0, ContentHash: id + "-hash", ResultCount: 1, IsFinal: true}
		if _, _, err := s.AcceptScanResultPage(ctx, receipt, []EntryPointObservation{observation}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.FinalizeScanRun(ctx, run.ID, "coordinator", leased.LeaseEpoch, ScanRunCompleted, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	accept("current-open", "server-vantage", now, "open")
	accept("current-closed", "server-vantage", now.Add(time.Minute), "closed")

	page, err := s.ListEntryPointObservations(ctx, EntryPointObservationQuery{ScopeID: scope.ID, Address: "192.0.2.10", CurrentOnly: true, Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].Outcome != "closed" {
		t.Fatalf("current-only projection: %+v err=%v", page, err)
	}
	key := entryPointCurrentKey(scope.ID, "192.0.2.10", ScanTransportTCP, 22, "server", "server-vantage")
	var current EntryPointCurrent
	if err := s.read(ctx, func(state *State) error {
		current = state.EntryPointCurrent[key]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !current.Contradicted || current.ObservationID != page.Items[0].ID {
		t.Fatalf("contradiction/current projection: %+v", current)
	}
}

func TestWorkerClaimIsSiteScopedAndRecoveryAware(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	site, _ := s.CreateSite(ctx, Site{Name: "worker-site"})
	other, _ := s.CreateSite(ctx, Site{Name: "other-site"})
	device, _ := s.CreateDevice(ctx, Device{DisplayName: "target", SiteID: site.ID})
	if _, err := s.CreateJob(ctx, Job{Kind: "enrollment", DeviceID: device.ID, ScopeID: "scope", ScopeRevision: 1}); err != nil {
		t.Fatal(err)
	}
	worker := WorkerIdentity{ID: "worker-1", Name: "local", Kind: "enroller", AuthTokenHash: HashToken("worker-token"), SiteIDs: []string{other.ID}}
	if err := s.CreateWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimWorkerJob(ctx, worker); !errors.Is(err, ErrNotFound) {
		t.Fatalf("worker claimed a job outside its site: %v", err)
	}
	if _, err := s.SetWorkspace(ctx, func(state *WorkspaceState) error { state.EnrollmentPaused = true; return nil }); err != nil {
		t.Fatal(err)
	}
	worker.SiteIDs = []string{site.ID}
	if _, err := s.ClaimWorkerJob(ctx, worker); !errors.Is(err, ErrConflict) {
		t.Fatalf("paused enrollment was not fenced: %v", err)
	}
}

func TestWorkerProgressKeepsLeaseUntilTerminalState(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	site, _ := s.CreateSite(ctx, Site{Name: "progress-site"})
	scope, err := s.CreateScope(ctx, Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	device, _ := s.CreateDevice(ctx, Device{DisplayName: "target", SiteID: site.ID})
	job, err := s.CreateJob(ctx, Job{Kind: "enrollment", DeviceID: device.ID, ScopeID: scope.ID, ScopeRevision: scope.Revision})
	if err != nil {
		t.Fatal(err)
	}
	worker := WorkerIdentity{ID: "worker-progress", Kind: "enroller", SiteIDs: []string{site.ID}}
	claimed, err := s.ClaimWorkerJob(ctx, worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReportJob(ctx, job.ID, worker.ID, claimed.Epoch, "connecting", nil); err != nil {
		t.Fatal(err)
	}
	progress, err := s.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.State != "connecting" || progress.LeaseExpiry == nil {
		t.Fatalf("progress lease = %+v", progress)
	}
	if err := s.ReportJob(ctx, job.ID, worker.ID, claimed.Epoch, "failed", map[string]string{"code": "fixture"}); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.State != "failed" || terminal.LeaseExpiry != nil {
		t.Fatalf("terminal lease = %+v", terminal)
	}
}

func TestScanStoreConcurrentLeaseRollbackAndCrossPageDuplicate(t *testing.T) {
	ctx := context.Background()
	s, scope, policy := newScanFixture(t)
	now := s.Now()
	run, err := s.CreateScanRun(ctx, scanRunFixture(scope, policy, "concurrent-lease", now))
	if err != nil {
		t.Fatal(err)
	}
	leases := make(chan error, 2)
	var group sync.WaitGroup
	for _, owner := range []string{"scanner-a", "scanner-b"} {
		group.Add(1)
		go func(owner string) {
			defer group.Done()
			_, leaseErr := s.LeaseScanRun(ctx, run.ID, owner, time.Minute)
			leases <- leaseErr
		}(owner)
	}
	group.Wait()
	close(leases)
	successes := 0
	conflicts := 0
	for leaseErr := range leases {
		if leaseErr == nil {
			successes++
		} else if errors.Is(leaseErr, ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent lease error: %v", leaseErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent lease result: successes=%d conflicts=%d", successes, conflicts)
	}
	leased, err := s.GetScanRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartScanRun(ctx, run.ID, leased.LeaseOwner, leased.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	invalid := ScanResultReceipt{RunID: run.ID, PageOrdinal: 0, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResultCount: 1}
	badObservation := EntryPointObservation{RunID: run.ID, PageOrdinal: 0, ScopeID: "wrong-scope", ScopeRevision: run.ScopeRevision, ScannerKind: run.ScannerKind, ScannerID: run.ScannerID, Address: "192.0.2.10", Transport: ScanTransportTCP, Port: 22, EntryPointID: policy.EntryPoints[0].ID, Outcome: "open", ObservedAt: now}
	if _, _, err := s.AcceptScanResultPage(ctx, invalid, []EntryPointObservation{badObservation}); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid page did not roll back: %v", err)
	}
	if _, err := s.ScanResultReceipt(ctx, run.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid page left receipt: %v", err)
	}
	goodObservation := badObservation
	goodObservation.ScopeID = scope.ID
	first, duplicate, err := s.AcceptScanResultPage(ctx, invalid, []EntryPointObservation{goodObservation})
	if err != nil || duplicate || first.ResultCount != 1 {
		t.Fatalf("valid page: %+v duplicate=%t err=%v", first, duplicate, err)
	}
	second := invalid
	second.PageOrdinal = 1
	second.ContentHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	goodObservation.PageOrdinal = 1
	if _, _, err := s.AcceptScanResultPage(ctx, second, []EntryPointObservation{goodObservation}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("cross-page duplicate was accepted: %v", err)
	}
	current, err := s.GetScanRun(ctx, run.ID)
	if err != nil || current.AttemptsCompleted != 1 {
		t.Fatalf("cross-page duplicate changed counters: %+v err=%v", current, err)
	}
}
