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
		state.AlertEvaluations["lineage\x00entity"] = AlertEvaluation{
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
