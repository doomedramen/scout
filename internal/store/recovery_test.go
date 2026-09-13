package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackupRestoreAndRecoveryPauseAuthority(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	s := NewMemory()
	s.SetClock(func() time.Time { return now })
	owner := Owner{ID: NewID(), PasswordHash: "argon2id$fixture"}
	if err := s.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, Session{TokenHash: "session", CSRFHash: "csrf", OwnerID: owner.ID, CreatedAt: now, LastSeen: now, AbsoluteExpiry: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	site, _ := s.CreateSite(ctx, Site{Name: "restore-lab"})
	device, _ := s.CreateDevice(ctx, Device{DisplayName: "restore-target", SiteID: site.ID})
	agent := AgentIdentity{ID: NewID(), DeviceID: device.ID, ExpiresAt: now.Add(-time.Minute)}
	if err := s.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateJob(ctx, Job{Kind: "enrollment", DeviceID: device.ID}); err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBackup(backup); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreMemory(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Owner(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, "session"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("recovery did not invalidate sessions: %v", err)
	}
	state, err := s.ReconcileRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.RecoveryMode || !state.EnrollmentPaused || !state.UpdatesPaused {
		t.Fatalf("recovery did not leave authority paused: %+v", state)
	}
	agents, _ := s.Agent(ctx, agent.ID)
	if agents.RevokedAt == nil {
		t.Fatal("expired agent was not revoked during reconciliation")
	}
}

func TestBackupRestorePreservesScanStateAndFencesActiveAuthority(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	s, scope, policy := newScanFixture(t)
	s.SetClock(func() time.Time { return now })

	policy.ServerEnabled = true
	policy.AgentIDs = []string{"restore-agent"}
	terminalFinished := now.Add(-2 * time.Minute)
	activeLeaseUntil := now.Add(30 * time.Second)
	terminalRun := ScanRun{
		ID: "restore-terminal-run", ScopeID: scope.ID, ScopeRevision: policy.Revision, ScannerKind: "agent", ScannerID: "restore-agent",
		Trigger: "schedule", State: ScanRunCompleted, ScheduledAt: now.Add(-3 * time.Minute), FinishedAt: &terminalFinished,
		AssignmentExpiresAt: now.Add(-time.Minute), TargetsPlanned: 1, AttemptsPlanned: 1, AttemptsCompleted: 1,
		OutcomeCounts: ScanOutcomeCounts{Open: 1}, PageCount: 1, FinalPageOrdinal: intPointer(0), CreatedAt: now.Add(-3 * time.Minute), UpdatedAt: terminalFinished,
	}
	activeRun := ScanRun{
		ID: "restore-active-run", ScopeID: scope.ID, ScopeRevision: policy.Revision, ScannerKind: "server", ScannerID: "control-server",
		Trigger: "owner", State: ScanRunRunning, ScheduledAt: now.Add(-time.Minute), StartedAt: timePointer(now.Add(-50 * time.Second)),
		AssignmentExpiresAt: now.Add(5 * time.Minute), LeaseOwner: "old-server", LeaseEpoch: 7, LeaseExpiresAt: &activeLeaseUntil,
		TargetsPlanned: 1, AttemptsPlanned: 1, AttemptsCompleted: 0, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-50 * time.Second),
	}
	observation := EntryPointObservation{
		ID: "restore-observation", RunID: terminalRun.ID, PageOrdinal: 0, ScopeID: scope.ID, ScopeRevision: policy.Revision,
		ScannerKind: terminalRun.ScannerKind, ScannerID: terminalRun.ScannerID, Address: "192.0.2.10", Transport: ScanTransportTCP, Port: 22,
		EntryPointID: policy.EntryPoints[0].ID, Outcome: "open", ObservedAt: terminalFinished, ReceivedAt: terminalFinished, ExpiresAt: now.Add(24 * time.Hour), Actionable: true,
	}
	candidate := Candidate{
		ID: "restore-candidate", SiteID: scope.SiteID, ScopeID: scope.ID, Address: observation.Address, Source: "active-scan", State: "needs_credentials",
		ScopeRevision: policy.Revision, FirstSeen: terminalFinished, LastSeen: terminalFinished, ExpiresAt: now.Add(time.Hour), EvidenceIDs: []string{observation.ID}, CoverageState: "current", EntryPointIDs: []string{policy.EntryPoints[0].ID}, PreferredAccessMethod: ScanAccessSSH,
	}
	request := AccessRequest{
		ID: "restore-access-request", DeviceID: candidate.ID, CandidateID: candidate.ID, ScopeID: scope.ID, AccessMethod: ScanAccessSSH,
		Endpoint: "192.0.2.10:22", EntryPointObservationID: observation.ID, ReasonCode: "missing_credentials", SafeDetails: map[string]string{"target": "192.0.2.10:22"}, State: "open", LastAttempt: terminalFinished,
	}
	current := EntryPointCurrent{
		Key: entryPointCurrentKey(scope.ID, observation.Address, ScanTransportTCP, 22, terminalRun.ScannerKind, terminalRun.ScannerID), ScopeID: scope.ID,
		Address: observation.Address, Transport: ScanTransportTCP, Port: 22, ScannerKind: terminalRun.ScannerKind, ScannerID: terminalRun.ScannerID,
		ObservationID: observation.ID, Outcome: "open", Freshness: "current", ObservedAt: observation.ObservedAt, ReceivedAt: observation.ReceivedAt, ExpiresAt: observation.ExpiresAt,
	}
	receipt := ScanResultReceipt{RunID: terminalRun.ID, PageOrdinal: 0, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AcceptedAt: terminalFinished, ResultCount: 1, IsFinal: true}
	assignment := ScanVantageAssignment{
		ScopeID: scope.ID, ScannerKind: "agent", ScannerID: "restore-agent", DeviceID: "restore-device", Capabilities: []string{"scan-protocol:1", "tcp"},
		State: "available", Revision: policy.Revision, UpdatedAt: terminalFinished,
	}
	extension := ScanCandidateExtension{
		CandidateID: candidate.ID, CoverageState: "current", EntryPointIDs: []string{policy.EntryPoints[0].ID}, PreferredAccessMethod: ScanAccessSSH,
		LastScannedAt: &terminalFinished, Provenance: []ScanVantageEvidence{{ScannerKind: terminalRun.ScannerKind, ScannerID: terminalRun.ScannerID, LastObservedAt: terminalFinished, Outcome: "open"}}, UpdatedAt: terminalFinished,
	}
	requestKey := ScanAccessRequestKey{
		DedupeKey: scanAccessRequestDedupeKey(candidate.ID, ScanAccessSSH, request.Endpoint), CandidateID: candidate.ID, AccessMethod: ScanAccessSSH,
		Endpoint: request.Endpoint, AccessRequestID: request.ID, State: request.State, UpdatedAt: terminalFinished,
	}
	if err := s.mutate(ctx, func(state *State) error {
		state.ScanPolicies[scope.ID] = policy
		state.ScanVantageAssignments["restore-assignment"] = assignment
		state.ScanRuns[terminalRun.ID] = terminalRun
		state.ScanRuns[activeRun.ID] = activeRun
		state.ScanRunLeases[activeRun.ID] = ScanRunLease{RunID: activeRun.ID, LeaseOwner: activeRun.LeaseOwner, LeaseEpoch: activeRun.LeaseEpoch, LeaseUntil: activeLeaseUntil, State: activeRun.State}
		state.ScanResultReceipts[scanReceiptKey(receipt.RunID, receipt.PageOrdinal)] = receipt
		state.EntryPointObservations[observation.ID] = observation
		state.EntryPointCurrent[current.Key] = current
		state.Candidates[candidateKey(scope.ID, candidate.Address)] = candidate
		state.AccessRequests[request.ID] = request
		state.ScanCandidateExtensions[candidate.ID] = extension
		state.ScanAccessRequestKeys[requestKey.DedupeKey] = requestKey
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreMemory(backup)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := restored.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.RecoveryMode || !workspace.EnrollmentPaused || !workspace.UpdatesPaused {
		t.Fatalf("restored scan authority was not paused: %+v", workspace)
	}
	restoredPolicy, err := restored.ScanPolicy(ctx, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !restoredPolicy.ServerEnabled || len(restoredPolicy.AgentIDs) != 1 || restoredPolicy.AgentIDs[0] != "restore-agent" || restoredPolicy.EntryPoints[0].Port != 22 {
		t.Fatalf("scan policy was not restored: %+v", restoredPolicy)
	}
	if err := restored.read(ctx, func(state *State) error {
		if _, ok := state.ScanVantageAssignments["restore-assignment"]; !ok {
			return errors.New("scan vantage assignment was not restored")
		}
		if _, ok := state.EntryPointCurrent[current.Key]; !ok {
			return errors.New("current entry-point projection was not restored")
		}
		if _, ok := state.ScanCandidateExtensions[candidate.ID]; !ok {
			return errors.New("candidate extension was not restored")
		}
		if _, ok := state.ScanAccessRequestKeys[requestKey.DedupeKey]; !ok {
			return errors.New("access request dedupe key was not restored")
		}
		if _, ok := state.ScanRunLeases[activeRun.ID]; ok {
			return errors.New("active scan lease survived recovery")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	terminal, err := restored.GetScanRun(ctx, terminalRun.ID)
	if err != nil || terminal.State != ScanRunCompleted || terminal.FinishedAt == nil {
		t.Fatalf("terminal scan summary was not restored: %+v err=%v", terminal, err)
	}
	active, err := restored.GetScanRun(ctx, activeRun.ID)
	if err != nil || active.State != ScanRunCancelled || !active.CancellationRequested || active.LeaseOwner != "" || active.LeaseExpiresAt != nil {
		t.Fatalf("active scan authority was not fenced: %+v err=%v", active, err)
	}
	if _, err := restored.ScanResultReceipt(ctx, receipt.RunID, receipt.PageOrdinal); err != nil {
		t.Fatalf("scan result receipt was not restored: %v", err)
	}
	if _, err := restored.GetEntryPointObservation(ctx, observation.ID); err != nil {
		t.Fatalf("scan evidence was not restored: %v", err)
	}
	if _, err := restored.CandidateByScopeAddress(ctx, scope.ID, candidate.Address); err != nil {
		t.Fatalf("candidate identity was not restored: %v", err)
	}
	requests, err := restored.ListAccessRequestsForCandidate(ctx, candidate.ID)
	if err != nil || len(requests) != 1 || requests[0].EntryPointObservationID != observation.ID || requests[0].State != "open" {
		t.Fatalf("access request link was not restored: %+v err=%v", requests, err)
	}
}

func TestRecoveryCancelsPendingWorkAndResumesOnlyFreshSummaries(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	s := NewMemory()
	s.SetClock(func() time.Time { return now })
	destination, err := s.PutNotificationDestination(ctx, NotificationDestination{
		ID: "recovery-destination", Name: "Recovery", BaseURL: "https://ntfy.example.test", MaskedTopic: "re********y", Enabled: true,
		SecretCiphertext: []byte("cipher"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueNotificationDeliveries(ctx, []NotificationDelivery{{
		ID: "recovery-old", DestinationID: destination.ID, TransitionID: "recovery-old-transition", Payload: []byte(`{"message":"old","priority":3}`),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.mutate(ctx, func(state *State) error {
		observed := now.Add(-time.Minute)
		state.AlertEvaluations[alertEvaluationKey("lineage", "entity")] = AlertEvaluation{
			LineageID: "lineage", EntityID: "entity", EffectiveRevision: 4, EvidenceState: "fresh",
			LastObservedAt: &observed, LastReceivedAt: &observed, PendingSince: &observed, RecoverySince: &observed, LastValidAt: &observed,
			TriggerConsecutive: 3, RecoveryConsecutive: 2, IncidentID: "incident", UpdatedAt: observed,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	started, err := s.StartRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !started.NotificationsPaused || !started.RecoveryMode {
		t.Fatalf("recovery pause = %+v", started)
	}
	old, err := s.GetNotificationDelivery(ctx, "recovery-old")
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != NotificationDeliveryCancelled || old.SafeError == "" {
		t.Fatalf("old delivery after recovery = %+v", old)
	}
	evaluation, err := s.GetAlertEvaluation(ctx, "lineage", "entity")
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.EvidenceState != "unknown" || evaluation.TriggerConsecutive != 0 || evaluation.RecoveryConsecutive != 0 || evaluation.LastObservedAt != nil || evaluation.IncidentID != "incident" {
		t.Fatalf("evaluation was not reset while preserving incident identity = %+v", evaluation)
	}
	if claims, err := s.ClaimNotificationDeliveries(ctx, "recovery-worker", 10, time.Minute); err != nil {
		t.Fatal(err)
	} else if len(claims) != 0 {
		t.Fatalf("paused recovery claimed %d deliveries", len(claims))
	}

	reconciled, err := s.ReconcileRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.RecoveryMode || !reconciled.NotificationsPaused {
		t.Fatalf("reconciliation pause = %+v", reconciled)
	}
	if _, err := s.EnqueueNotificationDeliveries(ctx, []NotificationDelivery{{
		ID: "recovery-stale", DestinationID: destination.ID, SummaryKey: "stale-summary", Payload: []byte(`{"message":"stale","priority":3}`),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ResumeNotificationDeliveries(ctx, reconciled.PolicyRevision-1, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale resume error = %v", err)
	}
	resumed, result, err := s.ResumeNotificationDeliveries(ctx, reconciled.PolicyRevision, []NotificationDelivery{{
		ID: "recovery-fresh", DestinationID: destination.ID, SummaryKey: "fresh-summary", Payload: []byte(`{"message":"fresh","priority":3}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.NotificationsPaused || result.Enqueued != 1 {
		t.Fatalf("resume result = %+v, %+v", resumed, result)
	}
	stale, err := s.GetNotificationDelivery(ctx, "recovery-stale")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != NotificationDeliveryCancelled {
		t.Fatalf("stale delivery after resume = %+v", stale)
	}
	claims, err := s.ClaimNotificationDeliveries(ctx, "recovery-worker", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].ID != "recovery-fresh" {
		t.Fatalf("resume claims = %+v", claims)
	}
}
