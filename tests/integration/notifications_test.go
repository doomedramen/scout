package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestSQLRecoveryFencesNotificationOutboxAndAllowsInFlightCompletion(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	repository := store.NewSQL(db)
	now := time.Date(2026, 9, 11, 23, 0, 0, 0, time.UTC)
	repository.SetClock(func() time.Time { return now })
	initialBackup, err := repository.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	destinationID := "sql-recovery-destination-" + store.NewID()
	inFlightID := "sql-recovery-in-flight-" + store.NewID()
	queuedID := "sql-recovery-queued-" + store.NewID()
	duringRecoveryID := "sql-recovery-during-" + store.NewID()
	resumedID := "sql-recovery-resumed-" + store.NewID()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_deliveries WHERE destination_id = $1`, destinationID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM notification_destinations WHERE id = $1`, destinationID)
		backup, parseErr := store.ParseBackup(initialBackup)
		if parseErr != nil {
			return
		}
		raw, marshalErr := json.Marshal(backup.State)
		if marshalErr != nil {
			return
		}
		_, _ = db.ExecContext(cleanupCtx, `UPDATE workspace_state SET recovery_mode=$1, enrollment_paused=$2, updates_paused=$3, schema_version=$4, state_json=$5, updated_at=now() WHERE singleton=true`, backup.State.Workspace.RecoveryMode, backup.State.Workspace.EnrollmentPaused, backup.State.Workspace.UpdatesPaused, backup.State.Workspace.SchemaVersion, raw)
	})

	destination, err := repository.PutNotificationDestination(ctx, store.NotificationDestination{
		ID: destinationID, Name: "SQL recovery destination", BaseURL: "https://ntfy.example.test", MaskedTopic: "sq*********n", Enabled: true,
		SecretCiphertext: []byte("ciphertext"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.EnqueueNotificationDeliveries(ctx, []store.NotificationDelivery{{
		ID: inFlightID, DestinationID: destination.ID, TransitionID: "sql-recovery-in-flight-transition", Payload: []byte(`{"message":"in flight","priority":3}`),
	}}); err != nil {
		t.Fatal(err)
	}
	inFlightClaims, err := repository.ClaimNotificationDeliveries(ctx, "sql-recovery-worker", 1, time.Hour)
	if err != nil || len(inFlightClaims) != 1 || inFlightClaims[0].ID != inFlightID {
		t.Fatalf("in-flight claim = %+v err=%v", inFlightClaims, err)
	}
	inFlightClaim := inFlightClaims[0]
	if _, err := repository.EnqueueNotificationDeliveries(ctx, []store.NotificationDelivery{{
		ID: queuedID, DestinationID: destination.ID, TransitionID: "sql-recovery-queued-transition", Payload: []byte(`{"message":"queued","priority":3}`),
	}}); err != nil {
		t.Fatal(err)
	}

	started, err := repository.StartRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !started.RecoveryMode || !started.NotificationsPaused {
		t.Fatalf("started recovery state = %+v", started)
	}
	queued, err := repository.GetNotificationDelivery(ctx, queuedID)
	if err != nil || queued.Status != store.NotificationDeliveryCancelled {
		t.Fatalf("queued delivery after start = %+v err=%v", queued, err)
	}
	inFlight, err := repository.GetNotificationDelivery(ctx, inFlightID)
	if err != nil || inFlight.Status != store.NotificationDeliverySending {
		t.Fatalf("in-flight delivery after start = %+v err=%v", inFlight, err)
	}
	if pausedClaims, err := repository.ClaimNotificationDeliveries(ctx, "sql-recovery-worker-2", 10, time.Minute); err != nil || len(pausedClaims) != 0 {
		t.Fatalf("paused claim = %+v err=%v", pausedClaims, err)
	}
	completed, err := repository.CompleteNotificationDelivery(ctx, inFlightClaim, store.NotificationDeliveryOutcome{Accepted: true, Now: now})
	if err != nil || completed.Status != store.NotificationDeliveryAccepted {
		t.Fatalf("in-flight completion after recovery = %+v err=%v", completed, err)
	}

	if _, err := repository.EnqueueNotificationDeliveries(ctx, []store.NotificationDelivery{{
		ID: duringRecoveryID, DestinationID: destination.ID, SummaryKey: "sql-recovery-during", Payload: []byte(`{"message":"during recovery","priority":3}`),
	}}); err != nil {
		t.Fatal(err)
	}
	reconciled, err := repository.ReconcileRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.RecoveryMode || !reconciled.NotificationsPaused {
		t.Fatalf("reconciled recovery state = %+v", reconciled)
	}
	during, err := repository.GetNotificationDelivery(ctx, duringRecoveryID)
	if err != nil || during.Status != store.NotificationDeliveryCancelled {
		t.Fatalf("during-recovery delivery = %+v err=%v", during, err)
	}
	if _, _, err := repository.ResumeNotificationDeliveries(ctx, started.PolicyRevision, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale resume error = %v", err)
	}
	resumed, result, err := repository.ResumeNotificationDeliveries(ctx, reconciled.PolicyRevision, []store.NotificationDelivery{{
		ID: resumedID, DestinationID: destination.ID, SummaryKey: "sql-recovery-fresh", Payload: []byte(`{"message":"fresh summary","priority":3}`),
	}})
	if err != nil || result.Enqueued != 1 || resumed.NotificationsPaused {
		t.Fatalf("resume = %+v result=%+v err=%v", resumed, result, err)
	}
	if claims, err := repository.ClaimNotificationDeliveries(ctx, "sql-recovery-worker-3", 10, time.Minute); err != nil || len(claims) != 1 || claims[0].ID != resumedID {
		t.Fatalf("fresh resume claim = %+v err=%v", claims, err)
	}
}
