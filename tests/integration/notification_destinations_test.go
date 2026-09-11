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
