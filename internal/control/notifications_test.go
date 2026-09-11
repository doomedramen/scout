package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"scout.local/scout/internal/notifications/ntfy"
	"scout.local/scout/internal/store"
)

func TestNotificationDestinationControlRedactsSecretsAndTestsSavedRevision(t *testing.T) {
	receiver := newControlNotificationReceiver(t)
	repository := store.NewMemory()
	app, err := NewApp(repository, nil, Config{SetupToken: "setup-secret-123456789"})
	if err != nil {
		t.Fatal(err)
	}
	app.Ntfy = ntfy.NewClient(receiver.server.Client())
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	client := server.Client()

	setup := postJSON(t, client, server.URL+"/api/v1/setup", map[string]any{"setupToken": "setup-secret-123456789", "password": "ScoutAa1"}, nil, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body)
	}
	login := postJSON(t, client, server.URL+"/api/v1/sessions", map[string]any{"password": "ScoutAa1"}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	var loginBody struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal([]byte(login.Body), &loginBody); err != nil {
		t.Fatal(err)
	}
	headers := http.Header{"X-CSRF-Token": []string{loginBody.CSRFToken}}

	created := postJSON(t, client, server.URL+"/api/v1/notification-destinations", map[string]any{
		"name": "Local ntfy", "baseUrl": receiver.server.URL, "topic": "scout_alerts", "token": "secret-token", "allowPlainHttp": true, "enabled": true,
	}, login.Cookies, headers)
	if created.Code != http.StatusCreated {
		t.Fatalf("create destination: %d %s", created.Code, created.Body)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(created.Body), &response); err != nil {
		t.Fatal(err)
	}
	id, _ := response["id"].(string)
	if id == "" || response["maskedTopic"] == "scout_alerts" || response["hasToken"] != true || response["allowPlainHttp"] != true || response["revision"] != float64(1) {
		t.Fatalf("created destination metadata = %+v", response)
	}
	if _, exists := response["topic"]; exists {
		t.Fatal("destination response leaked topic")
	}
	if _, exists := response["token"]; exists || strings.Contains(created.Body, "secret-token") || strings.Contains(created.Body, "scout_alerts") {
		t.Fatalf("destination response leaked secret: %s", created.Body)
	}

	metadata, err := repository.GetNotificationDestination(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.SecretCiphertext) != 0 || len(metadata.SecretNonce) != 0 || len(metadata.SecretWrappedDataKey) != 0 || metadata.SecretKeyVersion != 0 {
		t.Fatal("metadata read returned encrypted secret material")
	}
	stored, err := repository.GetNotificationDestinationSecret(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.SecretCiphertext) == 0 || bytes.Contains(stored.SecretCiphertext, []byte("secret-token")) || bytes.Contains(stored.SecretCiphertext, []byte("scout_alerts")) {
		t.Fatal("stored destination did not keep the secret encrypted")
	}

	listed := getRequest(t, client, server.URL+"/api/v1/notification-destinations", login.Cookies, nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body, "secret-token") || strings.Contains(listed.Body, "scout_alerts") {
		t.Fatalf("destination list: %d %s", listed.Code, listed.Body)
	}

	tested := postJSON(t, client, server.URL+"/api/v1/notification-destinations/"+id+"/test", map[string]any{"expectedRevision": 1}, login.Cookies, headers)
	if tested.Code != http.StatusAccepted || !strings.Contains(tested.Body, `"status":"queued"`) {
		t.Fatalf("test destination: %d %s", tested.Code, tested.Body)
	}
	received := receiver.lastRequest()
	if received.authorization != "Bearer secret-token" || received.path != "/scout_alerts" || !bytes.Contains(received.body, []byte(`"topic":"scout_alerts"`)) {
		t.Fatalf("test request = %+v", received)
	}
	metadata, err = repository.GetNotificationDestination(t.Context(), id)
	if err != nil || metadata.LastTestAt == nil {
		t.Fatalf("test timestamp = %+v err=%v", metadata, err)
	}

	updated := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/notification-destinations/"+id, map[string]any{"expectedRevision": 1, "name": "Updated ntfy", "enabled": true}, login.Cookies, headers)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body, `"revision":2`) || !strings.Contains(updated.Body, `"hasToken":true`) {
		t.Fatalf("update destination: %d %s", updated.Code, updated.Body)
	}
	paused := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/notification-destinations/"+id, map[string]any{"expectedRevision": 2, "enabled": false}, login.Cookies, headers)
	if paused.Code != http.StatusOK || !strings.Contains(paused.Body, `"revision":3`) || !strings.Contains(paused.Body, `"enabled":false`) {
		t.Fatalf("pause destination: %d %s", paused.Code, paused.Body)
	}
	disabledTest := postJSON(t, client, server.URL+"/api/v1/notification-destinations/"+id+"/test", map[string]any{"expectedRevision": 3}, login.Cookies, headers)
	if disabledTest.Code != http.StatusConflict || receiver.count.Load() != 1 {
		t.Fatalf("disabled destination test: %d %s (receiver requests: %d)", disabledTest.Code, disabledTest.Body, receiver.count.Load())
	}
	reenabled := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/notification-destinations/"+id, map[string]any{"expectedRevision": 3, "enabled": true}, login.Cookies, headers)
	if reenabled.Code != http.StatusOK || !strings.Contains(reenabled.Body, `"revision":4`) || !strings.Contains(reenabled.Body, `"enabled":true`) {
		t.Fatalf("re-enable destination: %d %s", reenabled.Code, reenabled.Body)
	}
	removedToken := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/notification-destinations/"+id, map[string]any{"expectedRevision": 4, "token": nil}, login.Cookies, headers)
	if removedToken.Code != http.StatusOK || !strings.Contains(removedToken.Body, `"revision":5`) || !strings.Contains(removedToken.Body, `"hasToken":false`) {
		t.Fatalf("remove destination token: %d %s", removedToken.Code, removedToken.Body)
	}
	stale := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/notification-destinations/"+id, map[string]any{"expectedRevision": 4, "enabled": false}, login.Cookies, headers)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale destination update: %d %s", stale.Code, stale.Body)
	}
	nullTopic := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/notification-destinations/"+id, map[string]any{"expectedRevision": 5, "topic": nil}, login.Cookies, headers)
	if nullTopic.Code != http.StatusBadRequest {
		t.Fatalf("null topic update: %d %s", nullTopic.Code, nullTopic.Body)
	}

	retired := doJSONRequest(t, client, http.MethodDelete, server.URL+"/api/v1/notification-destinations/"+id, map[string]any{"expectedRevision": 5}, login.Cookies, headers)
	if retired.Code != http.StatusNoContent {
		t.Fatalf("retire destination: %d %s", retired.Code, retired.Body)
	}
	getRetired := getRequest(t, client, server.URL+"/api/v1/notification-destinations/"+id, login.Cookies, nil)
	if getRetired.Code != http.StatusNotFound {
		t.Fatalf("retired destination read: %d %s", getRetired.Code, getRetired.Body)
	}
}

func TestNotificationDestinationControlRejectsUnsafeEndpoint(t *testing.T) {
	repository := store.NewMemory()
	app, err := NewApp(repository, nil, Config{SetupToken: "setup-secret-123456789"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	client := server.Client()
	setup := postJSON(t, client, server.URL+"/api/v1/setup", map[string]any{"setupToken": "setup-secret-123456789", "password": "ScoutAa1"}, nil, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body)
	}
	login := postJSON(t, client, server.URL+"/api/v1/sessions", map[string]any{"password": "ScoutAa1"}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	var loginBody struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal([]byte(login.Body), &loginBody); err != nil {
		t.Fatal(err)
	}
	response := postJSON(t, client, server.URL+"/api/v1/notification-destinations", map[string]any{
		"name": "Unsafe", "baseUrl": "http://example.com", "topic": "scout_alerts", "allowPlainHttp": true,
	}, login.Cookies, http.Header{"X-CSRF-Token": []string{loginBody.CSRFToken}})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_destination") {
		t.Fatalf("unsafe destination: %d %s", response.Code, response.Body)
	}
}

func TestNotificationDeliveryHistoryControlRedactsQueuePayload(t *testing.T) {
	ctx := t.Context()
	repository := store.NewMemory()
	destination, err := repository.PutNotificationDestination(ctx, store.NotificationDestination{
		ID: "history-destination", Name: "History destination", BaseURL: "https://ntfy.example.test", MaskedTopic: "sc********s", Enabled: true,
		SecretCiphertext: []byte("encrypted-token"), SecretNonce: []byte("nonce"), SecretWrappedDataKey: []byte("wrapped"), SecretKeyVersion: 1,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.EnqueueNotificationDeliveries(ctx, []store.NotificationDelivery{{ID: "history-delivery", DestinationID: destination.ID, IncidentID: "history-incident", TransitionID: "history-transition", Payload: []byte(`{"message":"private payload","token":"private-token","priority":3}`)}}); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(repository, nil, Config{SetupToken: "setup-secret-123456789"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	client := server.Client()
	setup := postJSON(t, client, server.URL+"/api/v1/setup", map[string]any{"setupToken": "setup-secret-123456789", "password": "ScoutAa1"}, nil, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body)
	}
	login := postJSON(t, client, server.URL+"/api/v1/sessions", map[string]any{"password": "ScoutAa1"}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	response := getRequest(t, client, server.URL+"/api/v1/notification-deliveries?status=queued", login.Cookies, nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body, "private payload") || strings.Contains(response.Body, "private-token") || strings.Contains(response.Body, "encrypted-token") {
		t.Fatalf("delivery history: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"id":"history-delivery"`) || !strings.Contains(response.Body, `"status":"queued"`) || !strings.Contains(response.Body, `"safeError":null`) {
		t.Fatalf("delivery history response = %s", response.Body)
	}
	invalid := getRequest(t, client, server.URL+"/api/v1/notification-deliveries?status=unknown", login.Cookies, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid delivery status: %d %s", invalid.Code, invalid.Body)
	}
}

type controlNotificationReceiver struct {
	server *httptest.Server
	count  atomic.Int32
	mu     sync.Mutex
	last   controlNotificationRequest
}

type controlNotificationRequest struct {
	path          string
	authorization string
	body          []byte
}

func newControlNotificationReceiver(t *testing.T) *controlNotificationReceiver {
	t.Helper()
	receiver := &controlNotificationReceiver{}
	receiver.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		receiver.count.Add(1)
		receiver.mu.Lock()
		receiver.last = controlNotificationRequest{path: request.URL.Path, authorization: request.Header.Get("Authorization"), body: body}
		receiver.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"control-accepted"}`))
	}))
	t.Cleanup(receiver.server.Close)
	return receiver
}

func (receiver *controlNotificationReceiver) lastRequest() controlNotificationRequest {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	return receiver.last
}
