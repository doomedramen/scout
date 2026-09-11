package store

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestNotificationDeliveryLeaseRetryAndAcceptance(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	repository := NewMemory()
	repository.SetClock(func() time.Time { return now })
	if _, err := repository.PutNotificationDestination(ctx, notificationDeliveryTestDestination("destination-1"), 0); err != nil {
		t.Fatal(err)
	}
	delivery := NotificationDelivery{ID: "delivery-1", DestinationID: "destination-1", IncidentID: "incident-1", TransitionID: "transition-1", Payload: []byte(`{"message":"incident","priority":3}`)}
	enqueued, err := repository.EnqueueNotificationDeliveries(ctx, []NotificationDelivery{delivery})
	if err != nil {
		t.Fatal(err)
	}
	if enqueued.Enqueued != 1 || enqueued.Dropped != 0 {
		t.Fatalf("enqueue result = %+v", enqueued)
	}
	duplicate, err := repository.EnqueueNotificationDeliveries(ctx, []NotificationDelivery{{ID: "delivery-2", DestinationID: delivery.DestinationID, IncidentID: delivery.IncidentID, TransitionID: delivery.TransitionID, Payload: delivery.Payload}})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Enqueued != 0 {
		t.Fatalf("duplicate enqueue result = %+v", duplicate)
	}

	claims, err := repository.ClaimNotificationDeliveries(ctx, "worker-a", 10, 5*time.Second)
	if err != nil || len(claims) != 1 {
		t.Fatalf("first claim = %+v err=%v", claims, err)
	}
	claim := claims[0]
	if claim.Attempts != 1 || claim.LeaseEpoch != 1 || claim.LeaseOwner != "worker-a" {
		t.Fatalf("first lease = %+v", claim)
	}
	otherClaims, err := repository.ClaimNotificationDeliveries(ctx, "worker-b", 10, 5*time.Second)
	if err != nil || len(otherClaims) != 0 {
		t.Fatalf("in-flight claim = %+v err=%v", otherClaims, err)
	}

	retried, err := repository.CompleteNotificationDelivery(ctx, claim, NotificationDeliveryOutcome{Retryable: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != NotificationDeliveryRetry || retried.NextAttemptAt == nil || !retried.NextAttemptAt.After(now) {
		t.Fatalf("retry result = %+v", retried)
	}
	if _, err := repository.CompleteNotificationDelivery(ctx, claim, NotificationDeliveryOutcome{Accepted: true, Now: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale completion error = %v", err)
	}

	now = retried.NextAttemptAt.Add(time.Second)
	claims, err = repository.ClaimNotificationDeliveries(ctx, "worker-b", 10, 5*time.Second)
	if err != nil || len(claims) != 1 {
		t.Fatalf("retry claim = %+v err=%v", claims, err)
	}
	accepted, err := repository.CompleteNotificationDelivery(ctx, claims[0], NotificationDeliveryOutcome{Accepted: true, RemoteID: "remote-1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != NotificationDeliveryAccepted || accepted.AcceptedAt == nil || accepted.RemoteID != "remote-1" {
		t.Fatalf("accepted result = %+v", accepted)
	}
	if accepted.Payload != nil || accepted.LeaseOwner != "" {
		t.Fatalf("public delivery leaked private fields = %+v", accepted)
	}
}

func TestNotificationDeliveryFencesDestinationChangesAndExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 17, 0, 0, 0, time.UTC)
	repository := NewMemory()
	repository.SetClock(func() time.Time { return now })
	destination := notificationDeliveryTestDestination("destination-fence")
	if _, err := repository.PutNotificationDestination(ctx, destination, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.EnqueueNotificationDeliveries(ctx, []NotificationDelivery{{ID: "delivery-fence", DestinationID: destination.ID, TransitionID: "transition-fence", Payload: []byte(`{"message":"fence","priority":3}`)}}); err != nil {
		t.Fatal(err)
	}
	metadata, err := repository.GetNotificationDestination(ctx, destination.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Enabled = false
	if _, err := repository.PutNotificationDestination(ctx, metadata, metadata.Revision); err != nil {
		t.Fatal(err)
	}
	if claims, err := repository.ClaimNotificationDeliveries(ctx, "worker", 1, time.Second); err != nil || len(claims) != 0 {
		t.Fatalf("disabled destination claim = %+v err=%v", claims, err)
	}
	fenced, err := repository.GetNotificationDelivery(ctx, "delivery-fence")
	if err != nil {
		t.Fatal(err)
	}
	if fenced.Status != NotificationDeliveryCancelled {
		t.Fatalf("fenced delivery = %+v", fenced)
	}

	second := notificationDeliveryTestDestination("destination-expiry")
	if _, err := repository.PutNotificationDestination(ctx, second, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.EnqueueNotificationDeliveries(ctx, []NotificationDelivery{{ID: "delivery-expiry", DestinationID: second.ID, TransitionID: "transition-expiry", ExpiresAt: now.Add(2 * time.Second), Payload: []byte(`{"message":"expiry","priority":3}`)}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if claims, err := repository.ClaimNotificationDeliveries(ctx, "worker", 1, time.Second); err != nil || len(claims) != 0 {
		t.Fatalf("expired delivery claim = %+v err=%v", claims, err)
	}
	expired, err := repository.GetNotificationDelivery(ctx, "delivery-expiry")
	if err != nil {
		t.Fatal(err)
	}
	if expired.Status != NotificationDeliveryExpired || expired.NextAttemptAt != nil {
		t.Fatalf("expired delivery = %+v", expired)
	}
}

func TestNotificationDeliveryQueueCapRecordsOverflow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)
	repository := NewMemory()
	repository.SetClock(func() time.Time { return now })
	destination := notificationDeliveryTestDestination("destination-cap")
	if _, err := repository.PutNotificationDestination(ctx, destination, 0); err != nil {
		t.Fatal(err)
	}
	if err := repository.mutate(ctx, func(state *State) error {
		for index := 0; index < MaxPendingNotificationDeliveries; index++ {
			id := "pending-" + strconv.Itoa(index)
			state.NotificationDeliveries[id] = NotificationDelivery{ID: id, DestinationID: destination.ID, DestinationRevision: 1, TransitionID: id, Status: NotificationDeliveryQueued, Attempts: 0, NextAttemptAt: timePointer(now), ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now, Payload: []byte(`{"message":"pending","priority":3}`)}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := repository.EnqueueNotificationDeliveries(ctx, []NotificationDelivery{{ID: "over-cap", DestinationID: destination.ID, TransitionID: "over-cap-transition", Payload: []byte(`{"message":"over","priority":3}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Enqueued != 0 || result.Dropped != 1 {
		t.Fatalf("over-cap result = %+v", result)
	}
	stats, err := repository.NotificationDeliveryStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.QueueOverflow != 1 || stats.Queued != MaxPendingNotificationDeliveries {
		t.Fatalf("queue stats = %+v", stats)
	}
}

func TestApplyAlertEvaluationWithInvalidDeliveryRollsBackMemory(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 18, 30, 0, 0, time.UTC)
	repository := NewMemory()
	repository.SetClock(func() time.Time { return now })
	destination := notificationDeliveryTestDestination("destination-rollback")
	if _, err := repository.PutNotificationDestination(ctx, destination, 0); err != nil {
		t.Fatal(err)
	}
	incident := &Incident{ID: "incident-rollback", LineageID: "lineage-rollback", EntityID: "entity-rollback", RuleRevision: 1, RuleSnapshot: map[string]any{"name": "rollback"}, Severity: "warning", Status: "active", EvidenceState: "fresh", OpenedAt: now, ObservedAt: now, EvaluatedAt: now, Revision: 1}
	transition := IncidentTransition{ID: "transition-rollback", IncidentID: incident.ID, Kind: "triggered", EvidenceState: "fresh", OccurredAt: now, RuleRevision: 1}
	evaluation := AlertEvaluation{LineageID: incident.LineageID, EntityID: incident.EntityID, EffectiveRevision: 1, EvidenceState: "fresh", IncidentID: incident.ID, UpdatedAt: now}
	delivery := NotificationDelivery{ID: "delivery-rollback", DestinationID: destination.ID, IncidentID: incident.ID, TransitionID: transition.ID, Payload: []byte(`not-json`), ExpiresAt: now.Add(time.Hour)}
	if err := repository.ApplyAlertEvaluationWithDeliveries(ctx, evaluation, incident, []IncidentTransition{transition}, []NotificationDelivery{delivery}); err == nil {
		t.Fatal("invalid delivery unexpectedly committed memory alert transaction")
	}
	if _, err := repository.GetIncident(ctx, incident.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled back memory incident = %v", err)
	}
	if _, err := repository.GetAlertEvaluation(ctx, evaluation.LineageID, evaluation.EntityID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled back memory evaluation = %v", err)
	}
}

func notificationDeliveryTestDestination(id string) NotificationDestination {
	return NotificationDestination{ID: id, Name: id, BaseURL: "https://ntfy.example.test", MaskedTopic: "sc********s", Enabled: true, SecretCiphertext: []byte("ciphertext"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1}
}
