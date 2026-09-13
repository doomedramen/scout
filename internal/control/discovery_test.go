package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scout.local/scout/internal/store"
)

func TestScanPolicyAndOnDemandRoutes(t *testing.T) {
	repository := store.NewMemory()
	application, err := NewApp(repository, nil, Config{SetupToken: "scan-api-setup-token"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	client := server.Client()

	setup := postJSON(t, client, server.URL+"/api/v1/setup", map[string]any{"setupToken": "scan-api-setup-token", "password": "ScoutAa1"}, nil, nil)
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

	siteResponse := postJSON(t, client, server.URL+"/api/v1/sites", map[string]any{"name": "scan-api-site"}, login.Cookies, headers)
	if siteResponse.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", siteResponse.Code, siteResponse.Body)
	}
	var site store.Site
	if err := json.Unmarshal([]byte(siteResponse.Body), &site); err != nil {
		t.Fatal(err)
	}

	scopeResponse := postJSON(t, client, server.URL+"/api/v1/scopes", map[string]any{
		"siteId":  site.ID,
		"ranges":  []string{"127.0.0.1/32"},
		"methods": []string{"tcp"},
		"ports":   []int{22},
		"enabled": true,
		"scanPolicy": map[string]any{
			"serverEnabled":   true,
			"agentIds":        []string{},
			"scheduleSeconds": 60,
			"entryPoints": []map[string]any{{
				"id": "ssh-default", "name": "SSH", "transport": "tcp", "port": 22, "accessMethod": "ssh", "enabled": true,
			}},
			"limits": map[string]any{
				"probesPerSecond": 10, "concurrency": 1, "targetBudget": 1,
				"attemptBudget": 1, "timeoutMilliseconds": 100, "runDeadlineSeconds": 30, "resultPageSize": 10,
			},
		},
	}, login.Cookies, headers)
	if scopeResponse.Code != http.StatusCreated {
		t.Fatalf("scope: %d %s", scopeResponse.Code, scopeResponse.Body)
	}
	if !strings.Contains(scopeResponse.Body, `"scanPolicy"`) || !strings.Contains(scopeResponse.Body, `"serverEnabled":true`) {
		t.Fatalf("scope omitted scan policy: %s", scopeResponse.Body)
	}
	var scope struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
		Policy   struct {
			Revision int64 `json:"revision"`
		} `json:"scanPolicy"`
	}
	if err := json.Unmarshal([]byte(scopeResponse.Body), &scope); err != nil {
		t.Fatal(err)
	}
	if scope.ID == "" || scope.Revision != 1 || scope.Policy.Revision != 1 {
		t.Fatalf("unexpected scope revisions: %+v", scope)
	}

	detail := getRequest(t, client, server.URL+"/api/v1/scopes/"+scope.ID, login.Cookies, nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body, `"scanPolicy"`) {
		t.Fatalf("scope detail: %d %s", detail.Code, detail.Body)
	}

	runHeaders := headers.Clone()
	runHeaders.Set("Idempotency-Key", "scan-api-run-once")
	runBody := map[string]any{"expectedRevision": scope.Policy.Revision, "scanner": map[string]any{"kind": "server", "id": "control-server"}}
	runResponse := postJSON(t, client, server.URL+"/api/v1/scopes/"+scope.ID+"/scan-runs", runBody, login.Cookies, runHeaders)
	if runResponse.Code != http.StatusAccepted {
		t.Fatalf("scan run: %d %s", runResponse.Code, runResponse.Body)
	}
	if strings.Contains(runResponse.Body, "policySnapshot") || strings.Contains(runResponse.Body, "leaseOwner") {
		t.Fatalf("scan run leaked internal authority: %s", runResponse.Body)
	}
	var run struct {
		ID              string `json:"id"`
		ScopeID         string `json:"scopeId"`
		AttemptsPlanned int    `json:"attemptsPlanned"`
	}
	if err := json.Unmarshal([]byte(runResponse.Body), &run); err != nil {
		t.Fatal(err)
	}
	if run.ID == "" || run.ScopeID != scope.ID || run.AttemptsPlanned != 1 {
		t.Fatalf("unexpected run response: %+v", run)
	}

	replay := postJSON(t, client, server.URL+"/api/v1/scopes/"+scope.ID+"/scan-runs", runBody, login.Cookies, runHeaders)
	if replay.Code != http.StatusAccepted || !strings.Contains(replay.Body, `"id":"`+run.ID+`"`) {
		t.Fatalf("idempotent replay: %d %s", replay.Code, replay.Body)
	}

	active := postJSON(t, client, server.URL+"/api/v1/scopes/"+scope.ID+"/scan-runs", runBody, login.Cookies, headers)
	if active.Code != http.StatusConflict || !strings.Contains(active.Body, `"code":"scan_already_active"`) {
		t.Fatalf("active run response: %d %s", active.Code, active.Body)
	}

	status := getRequest(t, client, server.URL+"/api/v1/scopes/"+scope.ID+"/scan-status", login.Cookies, nil)
	if status.Code != http.StatusOK || !strings.Contains(status.Body, `"activeRun"`) || !strings.Contains(status.Body, `"lastRun"`) || !strings.Contains(status.Body, `"vantages"`) {
		t.Fatalf("scan status: %d %s", status.Code, status.Body)
	}
	if !strings.Contains(status.Body, `"lastRun":{"id":"`+run.ID+`"`) {
		t.Fatalf("scan status did not project the queued run: %s", status.Body)
	}
	runList := getRequest(t, client, server.URL+"/api/v1/scan-runs?scopeId="+scope.ID+"&limit=1", login.Cookies, nil)
	if runList.Code != http.StatusOK || !strings.Contains(runList.Body, `"id":"`+run.ID+`"`) || strings.Contains(runList.Body, "policySnapshot") {
		t.Fatalf("scan run list: %d %s", runList.Code, runList.Body)
	}
	runDetail := getRequest(t, client, server.URL+"/api/v1/scan-runs/"+run.ID, login.Cookies, nil)
	if runDetail.Code != http.StatusOK || !strings.Contains(runDetail.Body, `"attemptsCompleted":0`) || !strings.Contains(runDetail.Body, `"outcomeCounts"`) {
		t.Fatalf("scan run detail: %d %s", runDetail.Code, runDetail.Body)
	}
	cancel := postJSON(t, client, server.URL+"/api/v1/scan-runs/"+run.ID+"/cancel", map[string]any{"expectedRevision": scope.Policy.Revision}, login.Cookies, headers)
	if cancel.Code != http.StatusAccepted || !strings.Contains(cancel.Body, `"state":"cancelled"`) || !strings.Contains(cancel.Body, `"cancellationRequested":true`) {
		t.Fatalf("scan run cancellation: %d %s", cancel.Code, cancel.Body)
	}

	policyPatch := doJSONRequest(t, client, http.MethodPatch, server.URL+"/api/v1/scopes/"+scope.ID, map[string]any{
		"expectedRevision": scope.Revision,
		"scanPolicy":       map[string]any{"expectedRevision": scope.Policy.Revision, "serverEnabled": false},
	}, login.Cookies, headers)
	if policyPatch.Code != http.StatusOK || !strings.Contains(policyPatch.Body, `"serverEnabled":false`) || !strings.Contains(policyPatch.Body, `"revision":2`) {
		t.Fatalf("scan policy patch: %d %s", policyPatch.Code, policyPatch.Body)
	}

	staleRun := postJSON(t, client, server.URL+"/api/v1/scopes/"+scope.ID+"/scan-runs", runBody, login.Cookies, headers)
	if staleRun.Code != http.StatusConflict || !strings.Contains(staleRun.Body, `"code":"scan_revision_superseded"`) {
		t.Fatalf("stale scan run: %d %s", staleRun.Code, staleRun.Body)
	}
}

func TestCandidateRoutesExposeActionableSafeDetail(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	application, err := NewApp(repository, nil, Config{SetupToken: "candidate-api-setup-token"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	client := server.Client()

	setup := postJSON(t, client, server.URL+"/api/v1/setup", map[string]any{"setupToken": "candidate-api-setup-token", "password": "ScoutAa1"}, nil, nil)
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

	site, err := repository.CreateSite(ctx, store.Site{Name: "candidate-api-site"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := repository.UpsertCandidate(ctx, store.Candidate{
		ScopeID: scope.ID, Address: "192.0.2.10", Hostname: "found-host", State: "needs_credentials",
		CoverageState: "current", EntryPointIDs: []string{"ssh-default"}, PreferredAccessMethod: store.ScanAccessSSH,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertAccessRequest(ctx, store.AccessRequest{
		DeviceID: candidate.ID, CandidateID: candidate.ID, ScopeID: scope.ID, AccessMethod: store.ScanAccessSSH,
		Endpoint: "192.0.2.10:22", ReasonCode: "missing_credentials", State: "open",
		SafeDetails: map[string]string{"target": "192.0.2.10:22", "method": store.ScanAccessSSH},
	}); err != nil {
		t.Fatal(err)
	}

	list := getRequest(t, client, server.URL+"/api/v1/candidates?scopeId="+scope.ID+"&state=needs_credentials&accessMethod=ssh&freshness=current&query=192.0.2.10&limit=1", login.Cookies, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("candidate list: %d %s", list.Code, list.Body)
	}
	if !strings.Contains(list.Body, `"entryPointCount":1`) || !strings.Contains(list.Body, `"kind":"assign_credentials"`) || !strings.Contains(list.Body, `"address":"192.0.2.10"`) {
		t.Fatalf("candidate list omitted actionable safe summary: %s", list.Body)
	}
	if strings.Contains(list.Body, "ciphertext") || strings.Contains(list.Body, "private-key") {
		t.Fatalf("candidate list leaked secret material: %s", list.Body)
	}

	detail := getRequest(t, client, server.URL+"/api/v1/candidates/"+candidate.ID, login.Cookies, nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body, `"accessRequests"`) || !strings.Contains(detail.Body, `"enrollment":null`) {
		t.Fatalf("candidate detail: %d %s", detail.Code, detail.Body)
	}
	for index := 1; index <= 100; index++ {
		if _, err := repository.UpsertCandidate(ctx, store.Candidate{ScopeID: scope.ID, Address: fmt.Sprintf("192.0.3.%d", index), State: "discovered", Source: "active-scan"}); err != nil {
			t.Fatal(err)
		}
	}
	bounded := getRequest(t, client, server.URL+"/api/v1/candidates?scopeId="+scope.ID+"&limit=100", login.Cookies, nil)
	var boundedBody struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"nextCursor"`
	}
	if bounded.Code != http.StatusOK || json.Unmarshal([]byte(bounded.Body), &boundedBody) != nil || len(boundedBody.Items) != 100 || boundedBody.NextCursor == nil || *boundedBody.NextCursor == "" {
		t.Fatalf("candidate page was not bounded at 100: %d %+v body=%s", bounded.Code, boundedBody, bounded.Body)
	}
	entryPoints := getRequest(t, client, server.URL+"/api/v1/candidates/"+candidate.ID+"/entry-points?limit=1", login.Cookies, nil)
	if entryPoints.Code != http.StatusOK || !strings.Contains(entryPoints.Body, `"items"`) || !strings.Contains(entryPoints.Body, `"nextCursor"`) {
		t.Fatalf("candidate entry points: %d %s", entryPoints.Code, entryPoints.Body)
	}

	recheck := postJSON(t, client, server.URL+"/api/v1/candidates/"+candidate.ID+"/reevaluate", map[string]any{"expectedRevision": scope.Revision}, login.Cookies, headers)
	if recheck.Code != http.StatusAccepted || !strings.Contains(recheck.Body, `"eligible":false`) || !strings.Contains(recheck.Body, `missing_credentials`) {
		t.Fatalf("candidate re-evaluation: %d %s", recheck.Code, recheck.Body)
	}
}

func TestScanRunRouteRejectsUnsafeAndUnauthenticatedRequests(t *testing.T) {
	repository := store.NewMemory()
	application, err := NewApp(repository, nil, Config{SetupToken: "scan-api-setup-token"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scopes/not-a-scope/scan-runs", strings.NewReader(`{"expectedRevision":1,"scanner":{"kind":"server","id":"control-server"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated scan run: %d %s", response.Code, response.Body)
	}
}
