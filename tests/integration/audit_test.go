package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"scout.local/scout/internal/audit"
	"scout.local/scout/internal/control"
	"scout.local/scout/internal/store"
)

func TestAuditAndErrorResponsesDoNotExposeSecrets(t *testing.T) {
	repository := store.NewMemory()
	logger := audit.NewLogger(repository)
	secret := "do-not-log-this-secret"
	if err := logger.Record(context.Background(), audit.Event{ActorKind: "owner", Action: "credential.create", Target: "credential", Outcome: map[string]any{"secret": secret, "backendError": "postgres password"}}); err != nil {
		t.Fatal(err)
	}
	events, err := repository.ListAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "postgres password") {
		t.Fatalf("redacted audit event leaked sensitive data: %s", encoded)
	}
	application, err := control.NewApp(store.NewMemory(), auditDatabase{}, control.Config{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/api/status", nil))
	body, _ := io.ReadAll(response.Body)
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "postgres password") {
		t.Fatalf("backend error leaked in API response: %s", body)
	}
}

type auditDatabase struct{}

func (auditDatabase) PingContext(context.Context) error { return errors.New("postgres password") }
