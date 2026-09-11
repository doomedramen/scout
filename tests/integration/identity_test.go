package integration

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"scout.local/scout/internal/control"
	"scout.local/scout/internal/store"
)

func TestSetupIsSingletonAndInventoryRequiresOwnerSession(t *testing.T) {
	repository := store.NewMemory()
	application, err := control.NewApp(repository, nil, control.Config{SetupToken: "integration-setup-token"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()

	const workers = 8
	responses := make(chan int, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			response := integrationPost(t, server.URL+"/api/v1/setup", map[string]string{"setupToken": "integration-setup-token", "password": "correct horse battery staple"})
			responses <- response.StatusCode
		}()
	}
	group.Wait()
	close(responses)
	created := 0
	conflicts := 0
	for status := range responses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected setup status: %d", status)
		}
	}
	if created != 1 || conflicts != workers-1 {
		t.Fatalf("setup race results: created=%d conflicts=%d", created, conflicts)
	}

	response, err := http.Get(server.URL + "/api/v1/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated inventory status=%d", response.StatusCode)
	}
}

func integrationPost(t *testing.T, target string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	return response
}

func TestAgentTransportFailsClosedForCertificateImpersonation(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	application, err := control.NewApp(repository, nil, control.Config{AgentRequireMTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{DisplayName: "transport-target"})
	if err != nil {
		t.Fatal(err)
	}
	token := "agent-token"
	identity := store.AgentIdentity{ID: store.NewID(), DeviceID: device.ID, AuthTokenHash: store.HashToken(token), CertSerial: "12345", ExpiresAt: repository.Now().Add(time.Hour)}
	if err := repository.CreateAgentIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/v1/desired-state", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bearer token bypassed mTLS requirement: %d %s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/agent/v1/desired-state", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.TLS = &tls.ConnectionState{}
	response = httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid peer certificate accepted: %d %s", response.Code, response.Body)
	}
}
