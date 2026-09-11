package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestAlertControlCRUDPaginationAndAcknowledgment(t *testing.T) {
	ctx := context.Background()
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

	createdSite, err := repository.CreateSite(ctx, store.Site{ID: "site-alerts", Name: "Alerts"})
	if err != nil {
		t.Fatal(err)
	}
	createdDevice, err := repository.CreateDevice(ctx, store.Device{ID: "device-alerts", SiteID: createdSite.ID, DisplayName: "Alert host"})
	if err != nil {
		t.Fatal(err)
	}

	ruleResponse := postJSON(t, client, server.URL+"/api/v1/alert-rules", map[string]any{
		"name": "CPU pressure", "targetKind": "fleet", "kind": "numeric", "severity": "warning", "enabled": true,
		"condition": map[string]any{"metric": "cpu.utilization", "operator": "gt", "triggerValue": 90, "clearValue": 85, "triggerSeconds": 300, "clearSeconds": 120},
	}, login.Cookies, headers)
	if ruleResponse.Code != http.StatusCreated {
		t.Fatalf("create rule: %d %s", ruleResponse.Code, ruleResponse.Body)
	}
	var ruleBody struct {
		ID         string         `json:"id"`
		Revision   int64          `json:"revision"`
		Condition  map[string]any `json:"condition"`
		TargetKind string         `json:"targetKind"`
	}
	if err := json.Unmarshal([]byte(ruleResponse.Body), &ruleBody); err != nil {
		t.Fatal(err)
	}
	if ruleBody.ID == "" || ruleBody.Revision != 1 || ruleBody.Condition["metric"] != "cpu.utilization" || ruleBody.TargetKind != "fleet" {
		t.Fatalf("created rule shape = %+v", ruleBody)
	}
	if ruleBody.Condition["triggerValue"] != float64(90) || ruleBody.Condition["clearValue"] != float64(85) {
		t.Fatalf("created rule omitted nested condition: %s", ruleResponse.Body)
	}

	list := getRequest(t, client, server.URL+"/api/v1/alert-rules?limit=1", login.Cookies, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body, ruleBody.ID) {
		t.Fatalf("list rules: %d %s", list.Code, list.Body)
	}
	get := getRequest(t, client, server.URL+"/api/v1/alert-rules/"+ruleBody.ID, login.Cookies, nil)
	if get.Code != http.StatusOK || !strings.Contains(get.Body, "CPU pressure") {
		t.Fatalf("get rule: %d %s", get.Code, get.Body)
	}

	updated := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/alert-rules/"+ruleBody.ID, map[string]any{"expectedRevision": 1, "name": "CPU pressure revised"}, login.Cookies, headers)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body, "CPU pressure revised") || !strings.Contains(updated.Body, "\"revision\":2") {
		t.Fatalf("update rule: %d %s", updated.Code, updated.Body)
	}
	stale := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/alert-rules/"+ruleBody.ID, map[string]any{"expectedRevision": 1, "name": "stale"}, login.Cookies, headers)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body, "conflict") {
		t.Fatalf("stale rule update: %d %s", stale.Code, stale.Body)
	}

	override := postJSON(t, client, server.URL+"/api/v1/alert-rules/"+ruleBody.ID+"/overrides", map[string]any{
		"targetKind": "site", "targetId": createdSite.ID, "severity": "critical", "enabled": true,
		"condition": map[string]any{"metric": "cpu.utilization", "operator": "gt", "triggerValue": 80, "clearValue": 70, "triggerSeconds": 60, "clearSeconds": 30},
	}, login.Cookies, headers)
	if override.Code != http.StatusCreated || !strings.Contains(override.Body, createdSite.ID) {
		t.Fatalf("create override: %d %s", override.Code, override.Body)
	}
	var overrideBody struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal([]byte(override.Body), &overrideBody); err != nil {
		t.Fatal(err)
	}
	if overrideBody.ID == "" || overrideBody.Revision != 1 {
		t.Fatalf("override identity = %+v", overrideBody)
	}
	overrideList := getRequest(t, client, server.URL+"/api/v1/alert-rules/"+ruleBody.ID+"/overrides", login.Cookies, nil)
	if overrideList.Code != http.StatusOK || !strings.Contains(overrideList.Body, overrideBody.ID) {
		t.Fatalf("list overrides: %d %s", overrideList.Code, overrideList.Body)
	}
	overrideUpdate := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/alert-rules/"+ruleBody.ID+"/overrides/"+overrideBody.ID, map[string]any{"expectedRevision": 1, "enabled": false}, login.Cookies, headers)
	if overrideUpdate.Code != http.StatusOK || !strings.Contains(overrideUpdate.Body, "\"enabled\":false") || !strings.Contains(overrideUpdate.Body, "\"revision\":2") {
		t.Fatalf("update override: %d %s", overrideUpdate.Code, overrideUpdate.Body)
	}

	now := time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)
	value := 95.0
	incidentID := store.NewID()
	if err := repository.ApplyAlertEvaluation(ctx, store.AlertEvaluation{LineageID: ruleBody.ID, EntityID: "host", EffectiveRevision: 2, EvidenceState: "fresh", UpdatedAt: now}, &store.Incident{
		ID: incidentID, LineageID: ruleBody.ID, EntityID: "host", DeviceID: createdDevice.ID, RuleRevision: 2,
		RuleSnapshot: map[string]any{"name": "CPU pressure revised", "kind": "numeric", "metric": "cpu.utilization", "operator": "gt", "triggerValue": value, "clearValue": 85, "triggerSeconds": 300, "clearSeconds": 120},
		Severity:     "critical", Status: "active", EvidenceState: "fresh", Value: &value, Unit: "percent", Source: "fixture", OpenedAt: now, ObservedAt: now, EvaluatedAt: now, Revision: 1,
	}, []store.IncidentTransition{{IncidentID: incidentID, Kind: "triggered", EvidenceState: "fresh", Value: &value, OccurredAt: now, RuleRevision: 2}}); err != nil {
		t.Fatal(err)
	}

	incidentList := getRequest(t, client, server.URL+"/api/v1/incidents?siteId="+createdSite.ID+"&limit=1", login.Cookies, nil)
	if incidentList.Code != http.StatusOK || !strings.Contains(incidentList.Body, incidentID) || !strings.Contains(incidentList.Body, createdSite.ID) {
		t.Fatalf("list incidents: %d %s", incidentList.Code, incidentList.Body)
	}
	incidentDetail := getRequest(t, client, server.URL+"/api/v1/incidents/"+incidentID, login.Cookies, nil)
	if incidentDetail.Code != http.StatusOK || !strings.Contains(incidentDetail.Body, "notificationSuppression") || !strings.Contains(incidentDetail.Body, "ruleSnapshot") {
		t.Fatalf("incident detail: %d %s", incidentDetail.Code, incidentDetail.Body)
	}

	acknowledged := postJSON(t, client, server.URL+"/api/v1/incidents/"+incidentID+"/acknowledgment", map[string]any{"expectedRevision": 1}, login.Cookies, headers)
	if acknowledged.Code != http.StatusOK || !strings.Contains(acknowledged.Body, "acknowledgedAt") || !strings.Contains(acknowledged.Body, "\"revision\":2") {
		t.Fatalf("acknowledge incident: %d %s", acknowledged.Code, acknowledged.Body)
	}
	repeated := postJSON(t, client, server.URL+"/api/v1/incidents/"+incidentID+"/acknowledgment", map[string]any{"expectedRevision": 1}, login.Cookies, headers)
	if repeated.Code != http.StatusOK || !strings.Contains(repeated.Body, "\"revision\":2") {
		t.Fatalf("repeat acknowledgment: %d %s", repeated.Code, repeated.Body)
	}

	transitions := getRequest(t, client, server.URL+"/api/v1/incidents/"+incidentID+"/transitions?limit=1", login.Cookies, nil)
	if transitions.Code != http.StatusOK || !strings.Contains(transitions.Body, "triggered") || !strings.Contains(transitions.Body, "nextCursor") {
		t.Fatalf("first transitions page: %d %s", transitions.Code, transitions.Body)
	}
	var transitionPage struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(transitions.Body), &transitionPage); err != nil {
		t.Fatal(err)
	}
	if len(transitionPage.Items) != 1 || transitionPage.NextCursor == "" {
		t.Fatalf("transition pagination = %+v", transitionPage)
	}
	secondTransitions := getRequest(t, client, server.URL+"/api/v1/incidents/"+incidentID+"/transitions?limit=1&cursor="+transitionPage.NextCursor, login.Cookies, nil)
	if secondTransitions.Code != http.StatusOK || !strings.Contains(secondTransitions.Body, "acknowledged") {
		t.Fatalf("second transitions page: %d %s", secondTransitions.Code, secondTransitions.Body)
	}

	invalidState := postJSON(t, client, server.URL+"/api/v1/alert-rules", map[string]any{
		"name": "Invalid state", "targetKind": "fleet", "kind": "state", "severity": "warning", "enabled": true,
		"condition": map[string]any{"state": "not-a-catalog-state", "triggerSeconds": 0, "clearSeconds": 0},
	}, login.Cookies, headers)
	if invalidState.Code != http.StatusBadRequest {
		t.Fatalf("invalid state status = %d %s", invalidState.Code, invalidState.Body)
	}
}

func doJSONRequest(t *testing.T, client *http.Client, method, target string, value any, cookies []*http.Cookie, headers http.Header) recordedResponse {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, target, bytes.NewReader(data))
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
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return recordedResponse{Code: response.StatusCode, Body: string(body), Cookies: response.Cookies()}
}
