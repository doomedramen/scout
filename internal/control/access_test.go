package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scout.local/scout/internal/store"
)

func TestCandidateScopedCredentialMutationDoesNotReevaluateWholeScope(t *testing.T) {
	ctx := t.Context()
	repository := store.NewMemory()
	application, err := NewApp(repository, nil, Config{SetupToken: "access-route-setup-token"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	client := server.Client()

	setup := postJSON(t, client, server.URL+"/api/v1/setup", map[string]any{
		"setupToken": "access-route-setup-token",
		"password":   "ScoutAa1",
	}, nil, nil)
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

	site, err := repository.CreateSite(ctx, store.Site{Name: "candidate-access-site"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{
		SiteID:         site.ID,
		Ranges:         []string{"192.0.2.0/24"},
		AllowedMethods: []string{"tcp"},
		Ports:          []int{22},
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PutTrust(ctx, store.TrustRecord{
		ScopeID: scope.ID, Host: "192.0.2.10", Endpoint: "192.0.2.10:22", Fingerprint: "SHA256:fixture",
	}); err != nil {
		t.Fatal(err)
	}
	first, err := repository.UpsertCandidate(ctx, store.Candidate{
		ScopeID: scope.ID, Address: "192.0.2.10", State: "needs_credentials", CoverageState: "current",
		EntryPointIDs: []string{"ssh-default"}, PreferredAccessMethod: store.ScanAccessSSH,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.UpsertCandidate(ctx, store.Candidate{
		ScopeID: scope.ID, Address: "192.0.2.11", State: "needs_credentials", CoverageState: "current",
		EntryPointIDs: []string{"ssh-default"}, PreferredAccessMethod: store.ScanAccessSSH,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []store.Candidate{first, second} {
		if _, err := repository.UpsertAccessRequest(ctx, store.AccessRequest{
			CandidateID: candidate.ID, DeviceID: candidate.ID, ScopeID: scope.ID, AccessMethod: store.ScanAccessSSH,
			Endpoint: candidate.Address + ":22", ReasonCode: "missing_credentials", State: "open",
			SafeDetails: map[string]string{"target": candidate.Address + ":22", "method": store.ScanAccessSSH},
		}); err != nil {
			t.Fatal(err)
		}
	}

	response := postJSON(t, client, server.URL+"/api/v1/credentials", map[string]any{
		"kind":                  "ssh",
		"authMethod":            "password",
		"secret":                "fixture-password",
		"username":              "fixture",
		"allowedUse":            []string{"enrollment"},
		"targets":               []string{"192.0.2.10:22"},
		"endpoint":              "192.0.2.10:22",
		"scopeId":               scope.ID,
		"candidateId":           first.ID,
		"expectedScopeRevision": scope.Revision,
	}, login.Cookies, headers)
	if response.Code != http.StatusCreated {
		t.Fatalf("credential: %d %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body, "fixture-password") || !strings.Contains(response.Body, `"affectedCandidateCount":1`) {
		t.Fatalf("credential response leaked or reported the wrong candidate count: %s", response.Body)
	}

	updatedFirst, err := repository.GetCandidate(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedFirst.State != "queued" {
		t.Fatalf("target candidate state = %q, want queued", updatedFirst.State)
	}
	updatedSecond, err := repository.GetCandidate(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedSecond.State != "needs_credentials" {
		t.Fatalf("unrelated candidate state = %q, want needs_credentials", updatedSecond.State)
	}
}
