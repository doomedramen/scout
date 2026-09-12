package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scout.local/scout/internal/auth"
	"scout.local/scout/internal/control"
	"scout.local/scout/internal/store"
)

type monitoringSecurityResponse struct {
	Code    int
	Body    string
	Cookies []*http.Cookie
}

func monitoringSecurityJSON(
	t *testing.T,
	client *http.Client,
	method string,
	target string,
	value any,
	cookies []*http.Cookie,
	headers http.Header,
) monitoringSecurityResponse {
	t.Helper()
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
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
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return monitoringSecurityResponse{Code: response.StatusCode, Body: string(responseBody), Cookies: response.Cookies()}
}

func monitoringSecurityCSRF(t *testing.T, response monitoringSecurityResponse) string {
	t.Helper()
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body.CSRFToken == "" {
		t.Fatal("login did not return a CSRF token")
	}
	return body.CSRFToken
}

func monitoringSecurityCookie(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func TestMonitoringSecurityRejectsUnauthorizedCSRFAndSecretReads(t *testing.T) {
	repository := store.NewMemory()
	app, err := control.NewApp(repository, nil, control.Config{SetupToken: "monitoring-security-setup"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	client := server.Client()

	unauthenticated := monitoringSecurityJSON(t, client, http.MethodGet, server.URL+"/api/v1/owner", nil, nil, nil)
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body, `"code":"unauthorized"`) {
		t.Fatalf("unauthenticated owner request = %d %s", unauthenticated.Code, unauthenticated.Body)
	}

	setup := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/setup", map[string]string{
		"setupToken": "monitoring-security-setup",
		"password":   "ScoutAa1",
	}, nil, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body)
	}
	login := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/sessions", map[string]string{
		"password": "ScoutAa1",
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body)
	}
	cookies := login.Cookies
	csrf := monitoringSecurityCSRF(t, login)
	csrfHeader := http.Header{"X-CSRF-Token": []string{csrf}}

	withoutCSRF := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/sites", map[string]string{
		"name": "csrf-missing",
	}, cookies, nil)
	if withoutCSRF.Code != http.StatusForbidden || !strings.Contains(withoutCSRF.Body, `"code":"forbidden"`) {
		t.Fatalf("mutation without CSRF = %d %s", withoutCSRF.Code, withoutCSRF.Body)
	}

	siteResponse := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/sites", map[string]string{
		"name": "security-lab",
	}, cookies, csrfHeader)
	if siteResponse.Code != http.StatusCreated {
		t.Fatalf("site = %d %s", siteResponse.Code, siteResponse.Body)
	}
	var site store.Site
	if err := json.Unmarshal([]byte(siteResponse.Body), &site); err != nil {
		t.Fatal(err)
	}

	app.Config.Production = true
	scopeInput := map[string]any{
		"siteId":  site.ID,
		"ranges":  []string{"192.0.2.0/24"},
		"methods": []string{"tcp"},
		"ports":   []int{22},
		"enabled": true,
	}
	withoutMFA := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/scopes", scopeInput, cookies, csrfHeader)
	if withoutMFA.Code != http.StatusForbidden || !strings.Contains(withoutMFA.Body, `"code":"recent_mfa_required"`) {
		t.Fatalf("sensitive scope mutation without MFA = %d %s", withoutMFA.Code, withoutMFA.Body)
	}

	secret := "ssh-private-secret"
	credentialInput := map[string]any{
		"kind":       "ssh",
		"secret":     secret,
		"allowedUse": []string{"enrollment"},
		"targets":    []string{"192.0.2.10:22"},
	}
	credentialWithoutMFA := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/credentials", credentialInput, cookies, csrfHeader)
	if credentialWithoutMFA.Code != http.StatusForbidden || !strings.Contains(credentialWithoutMFA.Body, `"code":"recent_mfa_required"`) {
		t.Fatalf("credential mutation without MFA = %d %s", credentialWithoutMFA.Code, credentialWithoutMFA.Body)
	}

	sessionToken := monitoringSecurityCookie(cookies, "scout_session")
	if sessionToken == "" {
		t.Fatal("session cookie missing")
	}
	if err := repository.SetSessionMFA(t.Context(), store.HashToken(sessionToken), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	credentialResponse := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/credentials", credentialInput, cookies, csrfHeader)
	if credentialResponse.Code != http.StatusCreated || strings.Contains(credentialResponse.Body, secret) || strings.Contains(credentialResponse.Body, "ciphertext") {
		t.Fatalf("credential response leaked secret material: %d %s", credentialResponse.Code, credentialResponse.Body)
	}
	listed := monitoringSecurityJSON(t, client, http.MethodGet, server.URL+"/api/v1/credentials", nil, cookies, nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body, secret) || strings.Contains(listed.Body, "ciphertext") || strings.Contains(listed.Body, "wrappedDataKey") {
		t.Fatalf("credential listing leaked secret material: %d %s", listed.Code, listed.Body)
	}

	logout := monitoringSecurityJSON(t, client, http.MethodDelete, server.URL+"/api/v1/sessions/current", nil, cookies, csrfHeader)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", logout.Code, logout.Body)
	}
	afterLogout := monitoringSecurityJSON(t, client, http.MethodGet, server.URL+"/api/v1/owner", nil, cookies, nil)
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session remained usable: %d %s", afterLogout.Code, afterLogout.Body)
	}
}

func TestMonitoringMFARejectsInvalidSecondFactor(t *testing.T) {
	repository := store.NewMemory()
	app, err := control.NewApp(repository, nil, control.Config{SetupToken: "monitoring-mfa-setup"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	client := server.Client()

	setup := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/setup", map[string]string{
		"setupToken": "monitoring-mfa-setup",
		"password":   "ScoutAa1",
	}, nil, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body)
	}
	login := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/sessions", map[string]string{"password": "ScoutAa1"}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body)
	}
	csrf := monitoringSecurityCSRF(t, login)
	csrfHeader := http.Header{"X-CSRF-Token": []string{csrf}}

	begin := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/owner/mfa/setup", map[string]string{"password": "ScoutAa1"}, login.Cookies, csrfHeader)
	if begin.Code != http.StatusOK {
		t.Fatalf("MFA setup = %d %s", begin.Code, begin.Body)
	}
	var firstSecret struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(begin.Body), &firstSecret); err != nil || firstSecret.Secret == "" {
		t.Fatalf("MFA setup secret = %d %s", begin.Code, begin.Body)
	}
	badConfirmation := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/owner/mfa/confirm", map[string]string{"totpCode": "abcdef"}, login.Cookies, csrfHeader)
	if badConfirmation.Code != http.StatusUnauthorized {
		t.Fatalf("invalid MFA confirmation = %d %s", badConfirmation.Code, badConfirmation.Body)
	}

	secondBegin := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/owner/mfa/setup", map[string]string{"password": "ScoutAa1"}, login.Cookies, csrfHeader)
	if secondBegin.Code != http.StatusOK {
		t.Fatalf("retry MFA setup = %d %s", secondBegin.Code, secondBegin.Body)
	}
	var secondSecret struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(secondBegin.Body), &secondSecret); err != nil || secondSecret.Secret == "" {
		t.Fatalf("retry MFA secret = %d %s", secondBegin.Code, secondBegin.Body)
	}
	app.Auth.Now = func() time.Time { return time.Now().UTC() }
	code, err := auth.TOTPCode(secondSecret.Secret, app.Auth.Now())
	if err != nil {
		t.Fatal(err)
	}
	confirmation := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/owner/mfa/confirm", map[string]string{"totpCode": code}, login.Cookies, csrfHeader)
	if confirmation.Code != http.StatusOK {
		t.Fatalf("valid MFA confirmation = %d %s", confirmation.Code, confirmation.Body)
	}

	wrongLogin := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/sessions", map[string]string{"password": "ScoutAa1", "totpCode": "abcdef"}, nil, nil)
	if wrongLogin.Code != http.StatusUnauthorized {
		t.Fatalf("invalid MFA sign-in = %d %s", wrongLogin.Code, wrongLogin.Body)
	}
	validLogin := monitoringSecurityJSON(t, client, http.MethodPost, server.URL+"/api/v1/sessions", map[string]string{"password": "ScoutAa1", "totpCode": code}, nil, nil)
	if validLogin.Code != http.StatusOK {
		t.Fatalf("valid MFA sign-in = %d %s", validLogin.Code, validLogin.Body)
	}
}
