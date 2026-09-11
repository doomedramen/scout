package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scout.local/scout/internal/store"
)

func TestSuppressionWindowControlCRUDAndRevisionChecks(t *testing.T) {
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
	headers := http.Header{"X-CSRF-Token": []string{loginBody.CSRFToken}}
	created := postJSON(t, client, server.URL+"/api/v1/suppression-windows", map[string]any{
		"name": "Nightly maintenance", "targetKind": "fleet", "enabled": true, "mode": "recurring",
		"timezone": "Europe/London", "weekdays": []int{1, 2, 3, 4, 5}, "startLocal": "22:00", "endLocal": "06:00",
	}, login.Cookies, headers)
	if created.Code != http.StatusCreated {
		t.Fatalf("create window: %d %s", created.Code, created.Body)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(created.Body), &response); err != nil {
		t.Fatal(err)
	}
	id, _ := response["id"].(string)
	if id == "" || response["revision"] != float64(1) || response["timezone"] != "Europe/London" || response["targetId"] != nil {
		t.Fatalf("created window response = %+v", response)
	}
	listed := getRequest(t, client, server.URL+"/api/v1/suppression-windows", login.Cookies, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body, id) {
		t.Fatalf("list windows: %d %s", listed.Code, listed.Body)
	}
	updated := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/suppression-windows/"+id, map[string]any{"expectedRevision": 1, "name": "Updated maintenance"}, login.Cookies, headers)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body, `"revision":2`) || !strings.Contains(updated.Body, "Updated maintenance") {
		t.Fatalf("update window: %d %s", updated.Code, updated.Body)
	}
	invalid := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/suppression-windows/"+id, map[string]any{"expectedRevision": 2, "startLocal": "22:00", "endLocal": "22:00"}, login.Cookies, headers)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid window update: %d %s", invalid.Code, invalid.Body)
	}
	stale := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/suppression-windows/"+id, map[string]any{"expectedRevision": 1, "enabled": false}, login.Cookies, headers)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale window update: %d %s", stale.Code, stale.Body)
	}
	deleted := doJSONRequest(t, client, http.MethodDelete, server.URL+"/api/v1/suppression-windows/"+id, map[string]any{"expectedRevision": 2}, login.Cookies, headers)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete window: %d %s", deleted.Code, deleted.Body)
	}
	listed = getRequest(t, client, server.URL+"/api/v1/suppression-windows", login.Cookies, nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body, id) {
		t.Fatalf("retired window list: %d %s", listed.Code, listed.Body)
	}
}
