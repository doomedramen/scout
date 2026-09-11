package control

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/store"
	"scout.local/scout/internal/telemetry"
)

type fakeDB struct{ err error }

func (db fakeDB) PingContext(context.Context) error { return db.err }

func TestStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		db   Database
		want string
	}{
		{"missing", nil, "not configured"},
		{"ready", fakeDB{}, "connected"},
		{"failure", fakeDB{errors.New("secret connection string")}, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			Handler(tc.db).ServeHTTP(response, httptest.NewRequest("GET", "/api/status", nil))
			body := response.Body.String()
			if response.Code != 200 || !strings.Contains(body, tc.want) {
				t.Fatalf("unexpected response: %d %s", response.Code, body)
			}
			if strings.Contains(body, "secret") {
				t.Fatal("database error leaked")
			}
		})
	}
}

func TestAuthenticatedFirstAgentStoresRealBatchAndHeartbeat(t *testing.T) {
	repository := store.NewMemory()
	app, err := NewApp(repository, nil, Config{SetupToken: "setup-secret-123456789"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	client := server.Client()

	setup := postJSON(t, client, server.URL+"/api/v1/setup", map[string]any{"setupToken": "setup-secret-123456789", "password": "correct horse battery staple"}, nil, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body)
	}
	login := postJSON(t, client, server.URL+"/api/v1/sessions", map[string]any{"password": "correct horse battery staple"}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	var loginBody struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal([]byte(login.Body), &loginBody); err != nil {
		t.Fatal(err)
	}
	cookies := login.Cookies
	if len(cookies) == 0 || loginBody.CSRFToken == "" {
		t.Fatal("session cookies missing")
	}
	header := http.Header{"X-CSRF-Token": []string{loginBody.CSRFToken}}
	siteResponse := postJSON(t, client, server.URL+"/api/v1/sites", map[string]any{"name": "test-lab"}, cookies, header)
	if siteResponse.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", siteResponse.Code, siteResponse.Body)
	}
	var site store.Site
	if err := json.Unmarshal([]byte(siteResponse.Body), &site); err != nil {
		t.Fatal(err)
	}
	sessionToken := ""
	for _, cookie := range cookies {
		if cookie.Name == "scout_session" {
			sessionToken = cookie.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("session token missing")
	}
	if err := repository.SetSessionMFA(context.Background(), store.HashToken(sessionToken), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	invitationResponse := postJSON(t, client, server.URL+"/api/v1/bootstrap-invitations", map[string]any{"displayName": "linux-test", "siteId": site.ID}, cookies, header)
	if invitationResponse.Code != http.StatusCreated {
		t.Fatalf("invitation: %d %s", invitationResponse.Code, invitationResponse.Body)
	}
	var invitation struct {
		DeviceID   string `json:"deviceId"`
		Invitation string `json:"invitation"`
	}
	if err := json.Unmarshal([]byte(invitationResponse.Body), &invitation); err != nil {
		t.Fatal(err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "linux-test"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	enroll := postJSON(t, client, server.URL+"/api/v1/agent/v1/enroll", map[string]any{"invitation": invitation.Invitation, "csrPem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})), "agentVersion": "0.1.0", "platform": "linux", "architecture": "arm64"}, nil, nil)
	if enroll.Code != http.StatusOK {
		t.Fatalf("enroll: %d %s", enroll.Code, enroll.Body)
	}
	var identity struct {
		AgentID    string `json:"agentId"`
		AgentToken string `json:"agentToken"`
	}
	if err := json.Unmarshal([]byte(enroll.Body), &identity); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	value := 12.5
	batch := telemetry.Batch{ProtocolVersion: 1, BootID: "boot-1", BatchID: "batch-1", ObservedAt: now, Collector: telemetry.CollectorRef{ID: "host", SchemaVersion: 1}, Samples: []telemetry.Sample{{EntityID: "host", Metric: "cpu.utilization", Value: &value, Availability: "current", Unit: "percent", ObservedAt: now}}, Observations: []telemetry.RelationshipObservation{}, DroppedCount: 0}
	batchResponse := postJSON(t, client, server.URL+"/api/v1/agent/v1/batches", batch, nil, http.Header{"Authorization": []string{"Bearer " + identity.AgentToken}})
	if batchResponse.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", batchResponse.Code, batchResponse.Body)
	}
	heartbeatResponse := postJSON(t, client, server.URL+"/api/v1/agent/v1/heartbeat", map[string]any{"bootId": "boot-1", "installedVersion": "0.1.0", "uptimeSeconds": 4, "collectorStates": []any{}, "updateState": map[string]string{}}, nil, http.Header{"Authorization": []string{"Bearer " + identity.AgentToken}})
	if heartbeatResponse.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", heartbeatResponse.Code, heartbeatResponse.Body)
	}
	devicesResponse := getRequest(t, client, server.URL+"/api/v1/devices", cookies, nil)
	if devicesResponse.Code != http.StatusOK || !strings.Contains(devicesResponse.Body, invitation.DeviceID) {
		t.Fatalf("devices: %d %s", devicesResponse.Code, devicesResponse.Body)
	}
	metricsResponse := getRequest(t, client, server.URL+"/api/v1/devices/"+invitation.DeviceID+"/metrics?metric=cpu.utilization", cookies, nil)
	if metricsResponse.Code != http.StatusOK || !strings.Contains(metricsResponse.Body, "12.5") {
		t.Fatalf("metrics: %d %s", metricsResponse.Code, metricsResponse.Body)
	}
}

type recordedResponse struct {
	Code    int
	Body    string
	Cookies []*http.Cookie
}

func postJSON(t *testing.T, client *http.Client, target string, value any, cookies []*http.Cookie, headers http.Header) recordedResponse {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		for _, item := range values {
			request.Header.Add(key, item)
		}
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return recordedResponse{Code: response.StatusCode, Body: string(body), Cookies: response.Cookies()}
}
func getRequest(t *testing.T, client *http.Client, target string, cookies []*http.Cookie, headers http.Header) recordedResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, values := range headers {
		for _, item := range values {
			request.Header.Add(key, item)
		}
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return recordedResponse{Code: response.StatusCode, Body: string(data), Cookies: response.Cookies()}
}
