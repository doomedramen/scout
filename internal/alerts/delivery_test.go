package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/notifications/ntfy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

func TestBuildNotificationDeliveryIntentsOnlyProjectsTriggerAndRecovery(t *testing.T) {
	now := time.Date(2026, 9, 11, 19, 0, 0, 0, time.UTC)
	incident := &store.Incident{ID: "incident-1", EntityID: "device-1", Severity: "critical", Source: "cpu"}
	destinations := []store.NotificationDestination{
		{ID: "enabled", Enabled: true, Revision: 2},
		{ID: "disabled", Enabled: false, Revision: 1},
		{ID: "retired", Enabled: true, Revision: 1, RetiredAt: timePointer(now)},
	}
	transitions := []store.IncidentTransition{
		{ID: "trigger-1", IncidentID: incident.ID, Kind: "triggered"},
		{ID: "ack-1", IncidentID: incident.ID, Kind: "acknowledged"},
		{ID: "recovery-1", IncidentID: incident.ID, Kind: "recovered"},
	}
	intents, err := BuildNotificationDeliveryIntents(destinations, incident, transitions, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 2 {
		t.Fatalf("intents = %+v", intents)
	}
	for _, intent := range intents {
		if intent.DestinationID != "enabled" || intent.DestinationRevision != 2 || intent.IncidentID != incident.ID || intent.Payload == nil {
			t.Fatalf("intent = %+v", intent)
		}
		if strings.Contains(string(intent.Payload), "token") || strings.Contains(string(intent.Payload), "topic") {
			t.Fatalf("secret fields reached delivery payload: %s", intent.Payload)
		}
	}
}

func TestDeliveryWorkerPublishesEncryptedDestinationAndAcceptsDelivery(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	clock := NewManualClock(now)
	repository := store.NewMemory()
	repository.SetClock(func() time.Time { return clock.Now() })
	keyRing, err := secrets.NewKeyRing([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan struct{}, 1)
	var receivedToken, receivedBody, receivedPath string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedToken = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		received <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"remote-1"}`)
	}))
	defer receiver.Close()

	destination := encryptedDeliveryDestination(t, keyRing, "destination-worker", receiver.URL, "scout_alerts", "token-secret")
	storedDestination, err := repository.PutNotificationDestination(ctx, destination, 0)
	if err != nil {
		t.Fatal(err)
	}
	incident := &store.Incident{ID: "incident-worker", EntityID: "device-worker", Severity: "warning", Source: "agent"}
	intents, err := BuildNotificationDeliveryIntents([]store.NotificationDestination{storedDestination}, incident, []store.IncidentTransition{{ID: "transition-worker", IncidentID: incident.ID, Kind: "triggered"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.EnqueueNotificationDeliveries(ctx, intents); err != nil {
		t.Fatal(err)
	}

	worker := NewDeliveryWorker(repository, ntfy.NewClient(receiver.Client()), keyRing, clock, "delivery-worker")
	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 1 || result.Accepted != 1 || result.Failed != 0 {
		t.Fatalf("worker result = %+v", result)
	}
	select {
	case <-received:
	default:
		t.Fatal("receiver did not receive notification")
	}
	if receivedPath != "/scout_alerts" || receivedToken != "Bearer token-secret" || !strings.Contains(receivedBody, "incident-worker") {
		t.Fatalf("received request path=%q token=%q body=%q", receivedPath, receivedToken, receivedBody)
	}
	stored, err := repository.GetNotificationDelivery(ctx, intents[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != store.NotificationDeliveryAccepted || stored.RemoteID != "remote-1" || stored.SafeError != "" {
		t.Fatalf("stored delivery = %+v", stored)
	}
}

func TestDeliveryWorkerRedactsPermanentPublishFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC)
	clock := NewManualClock(now)
	repository := store.NewMemory()
	repository.SetClock(func() time.Time { return clock.Now() })
	keyRing, err := secrets.NewKeyRing([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "private upstream detail", http.StatusBadRequest)
	}))
	defer receiver.Close()
	destination := encryptedDeliveryDestination(t, keyRing, "destination-failure", receiver.URL, "scout_alerts", "private-token")
	storedDestination, err := repository.PutNotificationDestination(ctx, destination, 0)
	if err != nil {
		t.Fatal(err)
	}
	intents, err := BuildNotificationDeliveryIntents([]store.NotificationDestination{storedDestination}, &store.Incident{ID: "incident-failure", EntityID: "device-failure", Severity: "warning"}, []store.IncidentTransition{{ID: "transition-failure", IncidentID: "incident-failure", Kind: "triggered"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.EnqueueNotificationDeliveries(ctx, intents); err != nil {
		t.Fatal(err)
	}
	worker := NewDeliveryWorker(repository, ntfy.NewClient(receiver.Client()), keyRing, clock, "delivery-worker")
	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || result.Accepted != 0 {
		t.Fatalf("worker failure result = %+v", result)
	}
	stored, err := repository.GetNotificationDelivery(ctx, intents[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != store.NotificationDeliveryFailed || stored.SafeError != "notification endpoint rejected delivery" || strings.Contains(stored.SafeError, "private") || stored.Payload != nil {
		t.Fatalf("redacted failure = %+v", stored)
	}
}

func encryptedDeliveryDestination(t *testing.T, keyRing *secrets.KeyRing, id, baseURL, topic, token string) store.NotificationDestination {
	t.Helper()
	plaintext, err := json.Marshal(map[string]string{"topic": topic, "token": token})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := keyRing.EncryptSecret(id, notificationDestinationSecretKind, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return store.NotificationDestination{ID: id, Name: id, BaseURL: baseURL, MaskedTopic: "sc********s", HasToken: token != "", AllowPlainHTTP: true, Enabled: true, SecretCiphertext: envelope.Ciphertext, SecretNonce: envelope.Nonce, SecretWrappedDataKey: envelope.WrappedDataKey, SecretKeyVersion: envelope.KeyVersion}
}
