package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"scout.local/scout/internal/store"
)

func TestRedactRemovesSecretsRecursively(t *testing.T) {
	s := store.NewMemory()
	logger := NewLogger(s)
	if err := logger.Record(context.Background(), Event{ActorKind: "owner", ActorID: "owner-1", Action: "credential.create", Target: "credential-1", RequestID: "req-1", Outcome: map[string]any{"password": "do-not-store", "metadata": map[string]any{"token": "also-do-not-store"}, "ok": true}}); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "do-not-store") || strings.Contains(string(raw), "also-do-not-store") {
		t.Fatalf("secret leaked: %s", raw)
	}
}
