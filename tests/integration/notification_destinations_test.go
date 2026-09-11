package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

func TestSQLNotificationDestinationsPersistEncryptedEnvelopeAndRevisions(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	id := "sql-notification-destination-" + store.NewID()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_destinations WHERE id = $1`, id)
	})

	keyRing, err := secrets.NewKeyRing(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := json.Marshal(map[string]string{"topic": "sql_alerts", "token": "sql-token"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := keyRing.EncryptSecret(id, "notification-destination", plaintext)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.PutNotificationDestination(ctx, store.NotificationDestination{
		ID: id, Name: "SQL destination", BaseURL: "https://ntfy.example.test", MaskedTopic: "sq******ts", HasToken: true,
		Enabled: false, SecretCiphertext: envelope.Ciphertext, SecretNonce: envelope.Nonce, SecretWrappedDataKey: envelope.WrappedDataKey, SecretKeyVersion: envelope.KeyVersion,
	}, 0)
	if err != nil || created.Revision != 1 {
		t.Fatalf("create destination = %+v err=%v", created, err)
	}
	metadata, err := repository.GetNotificationDestination(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.SecretCiphertext) != 0 || metadata.HasToken != true {
		t.Fatalf("SQL metadata read = %+v", metadata)
	}
	stored, err := repository.GetNotificationDestinationSecret(ctx, id)
	if err != nil || !bytes.Equal(stored.SecretCiphertext, envelope.Ciphertext) {
		t.Fatalf("SQL secret envelope = %+v err=%v", stored, err)
	}
	decrypted, err := keyRing.DecryptSecret(id, "notification-destination", secrets.Envelope{Ciphertext: stored.SecretCiphertext, Nonce: stored.SecretNonce, WrappedDataKey: stored.SecretWrappedDataKey, KeyVersion: stored.SecretKeyVersion})
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("SQL secret decrypt = %q err=%v", decrypted, err)
	}

	restarted := store.NewSQL(db)
	restartedMetadata, err := restarted.GetNotificationDestination(ctx, id)
	if err != nil || restartedMetadata.Revision != 1 || restartedMetadata.Name != "SQL destination" {
		t.Fatalf("restarted metadata = %+v err=%v", restartedMetadata, err)
	}
	if _, err := restarted.RecordNotificationDestinationTest(ctx, id, 1, time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.PutNotificationDestination(ctx, restartedMetadata, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RetireNotificationDestination(ctx, id, 1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale SQL retirement = %v", err)
	}
	retired, err := restarted.RetireNotificationDestination(ctx, id, 2)
	if err != nil || retired.Revision != 3 || retired.RetiredAt == nil || retired.Enabled {
		t.Fatalf("SQL retirement = %+v err=%v", retired, err)
	}
	active, err := restarted.ListNotificationDestinations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range active {
		if item.ID == id {
			t.Fatalf("retired SQL destination remained active: %+v", item)
		}
	}
}

func TestSQLNotificationDeliveriesAreTransactionalAndLeased(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	destinationID := "sql-delivery-destination-" + store.NewID()
	incidentID := "sql-delivery-incident-" + store.NewID()
	transitionID := "sql-delivery-transition-" + store.NewID()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_deliveries WHERE destination_id = $1`, destinationID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM incidents WHERE id = $1`, incidentID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_destinations WHERE id = $1`, destinationID)
	})
	now := time.Date(2026, 9, 11, 23, 0, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	destination, err := repository.PutNotificationDestination(ctx, store.NotificationDestination{
		ID: destinationID, Name: "SQL delivery destination", BaseURL: "https://ntfy.example.test", MaskedTopic: "sq******ts", Enabled: true,
		SecretCiphertext: []byte("ciphertext"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	incident := &store.Incident{ID: incidentID, LineageID: "sql-delivery-lineage", EntityID: "sql-device", RuleRevision: 1, RuleSnapshot: map[string]any{"name": "SQL delivery"}, Severity: "warning", Status: "active", EvidenceState: "fresh", OpenedAt: now, ObservedAt: now, EvaluatedAt: now, Revision: 1}
	transition := store.IncidentTransition{ID: transitionID, IncidentID: incident.ID, Kind: "triggered", EvidenceState: "fresh", OccurredAt: now, RuleRevision: 1}
	evaluation := store.AlertEvaluation{LineageID: incident.LineageID, EntityID: incident.EntityID, EffectiveRevision: 1, EvidenceState: "fresh", IncidentID: incident.ID, UpdatedAt: now}
	delivery := store.NotificationDelivery{ID: "sql-delivery-" + store.NewID(), DestinationID: destination.ID, DestinationRevision: destination.Revision, IncidentID: incident.ID, TransitionID: transition.ID, Payload: []byte(`{"message":"queued","priority":3}`), ExpiresAt: now.Add(time.Hour), NextAttemptAt: &now, CreatedAt: now}
	if err := repository.ApplyAlertEvaluationWithDeliveries(ctx, evaluation, incident, []store.IncidentTransition{transition}, []store.NotificationDelivery{delivery}); err != nil {
		t.Fatal(err)
	}
	page, err := repository.ListNotificationDeliveries(ctx, store.NotificationDeliveryQuery{IncidentID: incident.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Status != store.NotificationDeliveryQueued {
		t.Fatalf("SQL queued delivery = %+v", page.Items)
	}
	claims, err := repository.ClaimNotificationDeliveries(ctx, "sql-worker", 10, time.Second)
	if err != nil || len(claims) != 1 || claims[0].Attempts != 1 {
		t.Fatalf("SQL delivery claim = %+v err=%v", claims, err)
	}
	accepted, err := repository.CompleteNotificationDelivery(ctx, claims[0], store.NotificationDeliveryOutcome{Accepted: true, RemoteID: "sql-remote", Now: now})
	if err != nil || accepted.Status != store.NotificationDeliveryAccepted {
		t.Fatalf("SQL delivery completion = %+v err=%v", accepted, err)
	}

	rollbackIncidentID := "sql-rollback-incident-" + store.NewID()
	rollbackIncident := *incident
	rollbackIncident.ID = rollbackIncidentID
	rollbackIncident.EntityID = "sql-rollback-device"
	rollbackTransition := store.IncidentTransition{ID: "sql-rollback-transition-" + store.NewID(), IncidentID: rollbackIncidentID, Kind: "triggered", EvidenceState: "fresh", OccurredAt: now, RuleRevision: 1}
	rollbackEvaluation := evaluation
	rollbackEvaluation.EntityID = rollbackIncident.EntityID
	rollbackEvaluation.IncidentID = rollbackIncidentID
	rollbackDelivery := store.NotificationDelivery{ID: "sql-rollback-delivery-" + store.NewID(), DestinationID: destination.ID, DestinationRevision: destination.Revision, IncidentID: rollbackIncidentID, TransitionID: rollbackTransition.ID, Payload: []byte(`not-json`), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := repository.ApplyAlertEvaluationWithDeliveries(ctx, rollbackEvaluation, &rollbackIncident, []store.IncidentTransition{rollbackTransition}, []store.NotificationDelivery{rollbackDelivery}); err == nil {
		t.Fatal("invalid delivery unexpectedly committed SQL alert transaction")
	}
	if _, err := repository.GetIncident(ctx, rollbackIncidentID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rolled back incident = %v", err)
	}
	if _, err := repository.GetAlertEvaluation(ctx, rollbackEvaluation.LineageID, rollbackEvaluation.EntityID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rolled back evaluation = %v", err)
	}
}
