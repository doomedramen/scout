package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"scout.local/scout/internal/control"
)

func TestTransportRejectsUnauthenticatedInventoryAndHeaderImpersonation(t *testing.T) {
	app, err := control.NewApp(nil, nil, control.Config{SetupToken: "test-setup-secret-123456"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/devices")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("inventory status=%d", response.StatusCode)
	}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent/v1/heartbeat", nil)
	request.Header.Set("X-Scout-Agent-ID", "forged-agent")
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("header impersonation status=%d", response.StatusCode)
	}
}
