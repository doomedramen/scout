package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"scout.local/scout/internal/store"
)

func TestMonitoringPolicyRoutesRequirePreviewAndExposeStatus(t *testing.T) {
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
	csrf := http.Header{"X-CSRF-Token": []string{loginBody.CSRFToken}}

	settingsResponse := getRequest(t, client, server.URL+"/api/v1/monitoring/settings", login.Cookies, nil)
	if settingsResponse.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", settingsResponse.Code, settingsResponse.Body)
	}
	var settings store.MonitoringSettings
	if err := json.Unmarshal([]byte(settingsResponse.Body), &settings); err != nil {
		t.Fatal(err)
	}

	previewHeaders := csrf.Clone()
	previewHeaders.Set("Idempotency-Key", "route-policy-preview")
	previewResponse := postJSON(t, client, server.URL+"/api/v1/monitoring/retention-preview", map[string]any{
		"expectedRevision": settings.Revision,
		"retention":        map[string]int{"rawDays": 1, "fiveMinuteDays": 90, "hourlyDays": 365},
	}, login.Cookies, previewHeaders)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("retention preview: %d %s", previewResponse.Code, previewResponse.Body)
	}
	var preview store.RetentionPreview
	if err := json.Unmarshal([]byte(previewResponse.Body), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.PreviewID == "" {
		t.Fatalf("retention preview did not return an id: %s", previewResponse.Body)
	}

	withoutPreview := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/monitoring/settings", map[string]any{
		"expectedRevision": settings.Revision,
		"retention":        map[string]int{"rawDays": 1},
	}, login.Cookies, csrf)
	if withoutPreview.Code != http.StatusConflict {
		t.Fatalf("reduction without preview: %d %s", withoutPreview.Code, withoutPreview.Body)
	}

	updatedResponse := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/monitoring/settings", map[string]any{
		"expectedRevision":   settings.Revision,
		"retention":          map[string]int{"rawDays": 1},
		"retentionPreviewId": preview.PreviewID,
	}, login.Cookies, csrf)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("settings update: %d %s", updatedResponse.Code, updatedResponse.Body)
	}
	var updated store.MonitoringSettings
	if err := json.Unmarshal([]byte(updatedResponse.Body), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Revision != settings.Revision+1 || updated.Retention.RawDays != 1 {
		t.Fatalf("settings update was not applied: %+v", updated)
	}

	statusResponse := getRequest(t, client, server.URL+"/api/v1/monitoring/status", login.Cookies, nil)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("monitoring status: %d %s", statusResponse.Code, statusResponse.Body)
	}
	var status store.MonitoringStatus
	if err := json.Unmarshal([]byte(statusResponse.Body), &status); err != nil {
		t.Fatal(err)
	}
	if status.StoragePressure.BudgetBytes != store.DefaultTelemetryBudgetBytes || status.LastSuccessfulJobs["retention"] == nil {
		t.Fatalf("monitoring status omitted bounded health data: %+v", status)
	}
}
