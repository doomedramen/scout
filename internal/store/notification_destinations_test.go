package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestNotificationDestinationStoreKeepsSecretsOutOfMetadataAndHonorsLifecycle(t *testing.T) {
	repository := NewMemory()
	repository.SetClock(func() time.Time { return time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC) })
	destination := NotificationDestination{
		ID: "notification-store-fixture", Name: "Store fixture", BaseURL: "https://ntfy.example.test", MaskedTopic: "sc********s", HasToken: true,
		Enabled: false, SecretCiphertext: []byte("ciphertext"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}
	created, err := repository.PutNotificationDestination(context.Background(), destination, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || len(created.SecretCiphertext) != 0 {
		t.Fatalf("created metadata = %+v", created)
	}
	metadata, err := repository.GetNotificationDestination(context.Background(), destination.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.SecretCiphertext) != 0 || len(metadata.SecretNonce) != 0 || len(metadata.SecretWrappedDataKey) != 0 || metadata.SecretKeyVersion != 0 {
		t.Fatal("metadata read returned encrypted fields")
	}
	secret, err := repository.GetNotificationDestinationSecret(context.Background(), destination.ID)
	if err != nil || !bytes.Equal(secret.SecretCiphertext, destination.SecretCiphertext) {
		t.Fatalf("secret envelope = %+v err=%v", secret, err)
	}

	updated := metadata
	updated.Enabled = true
	updated.SecretCiphertext = append([]byte(nil), destination.SecretCiphertext...)
	updated.SecretNonce = append([]byte(nil), destination.SecretNonce...)
	updated.SecretWrappedDataKey = append([]byte(nil), destination.SecretWrappedDataKey...)
	updated.SecretKeyVersion = destination.SecretKeyVersion
	updated, err = repository.PutNotificationDestination(context.Background(), updated, 1)
	if err != nil || updated.Revision != 2 || !updated.Enabled {
		t.Fatalf("updated destination = %+v err=%v", updated, err)
	}
	if _, err := repository.PutNotificationDestination(context.Background(), updated, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale destination update = %v", err)
	}
	if _, err := repository.RecordNotificationDestinationTest(context.Background(), destination.ID, updated.Revision, time.Time{}); err != nil {
		t.Fatal(err)
	}
	retired, err := repository.RetireNotificationDestination(context.Background(), destination.ID, updated.Revision)
	if err != nil || retired.RetiredAt == nil || retired.Enabled || retired.Revision != 3 {
		t.Fatalf("retired destination = %+v err=%v", retired, err)
	}
	active, err := repository.ListNotificationDestinations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range active {
		if item.ID == destination.ID {
			t.Fatalf("retired destination remained active: %+v", item)
		}
	}
}

func TestNotificationDestinationStoreBoundsActiveDestinations(t *testing.T) {
	repository := NewMemory()
	for index := 0; index < MaxNotificationDestinations; index++ {
		id := "notification-cap-" + string(rune('a'+index))
		_, err := repository.PutNotificationDestination(context.Background(), NotificationDestination{
			ID: id, Name: id, BaseURL: "https://ntfy.example.test", MaskedTopic: "ca******" + string(rune('a'+index)),
			SecretCiphertext: []byte("ciphertext"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
		}, 0)
		if err != nil {
			t.Fatalf("destination %d: %v", index, err)
		}
	}
	_, err := repository.PutNotificationDestination(context.Background(), NotificationDestination{
		ID: "notification-cap-overflow", Name: "overflow", BaseURL: "https://ntfy.example.test", MaskedTopic: "ov******w",
		SecretCiphertext: []byte("ciphertext"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}, 0)
	if !errors.Is(err, ErrBackpressure) {
		t.Fatalf("active destination cap error = %v", err)
	}
}
