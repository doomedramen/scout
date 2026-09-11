package audit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

type Event struct {
	ActorKind string
	ActorID   string
	Action    string
	Target    string
	Outcome   map[string]any
	RequestID string
	At        time.Time
}

type Logger struct {
	Store *store.Store
	Now   func() time.Time
}

func NewLogger(repository *store.Store) *Logger {
	return &Logger{Store: repository, Now: func() time.Time { return time.Now().UTC() }}
}

func (l *Logger) Record(ctx context.Context, event Event) error {
	if l == nil || l.Store == nil {
		return fmt.Errorf("audit store unavailable")
	}
	if event.At.IsZero() {
		event.At = l.Now().UTC()
	}
	return l.Store.AppendAudit(ctx, store.AuditEvent{ID: store.NewID(), ActorKind: safeText(event.ActorKind), ActorID: safeText(event.ActorID), Action: safeText(event.Action), Target: safeText(event.Target), EventTime: event.At.UTC(), RedactedOutcome: Redact(event.Outcome), RequestID: safeText(event.RequestID)})
}

func safeText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

var secretKeyFragments = []string{"secret", "password", "token", "credential", "privatekey", "private_key", "authorization", "cookie", "csr", "backend", "diagnostic", "error"}

func isSecretKey(key string) bool {
	lower := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, fragment := range secretKeyFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func Redact(value any) map[string]any {
	result := map[string]any{}
	if value == nil {
		return result
	}
	if input, ok := value.(map[string]any); ok {
		for key, item := range input {
			if isSecretKey(key) {
				result[key] = "[redacted]"
				continue
			}
			result[key] = redactValue(item)
		}
		return result
	}
	result["value"] = redactValue(value)
	return result
}

func redactValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		output := map[string]any{}
		for key, nested := range item {
			if isSecretKey(key) {
				output[key] = "[redacted]"
			} else {
				output[key] = redactValue(nested)
			}
		}
		return output
	case []any:
		output := make([]any, len(item))
		for i, nested := range item {
			output[i] = redactValue(nested)
		}
		return output
	case string:
		if len(item) > 512 {
			return item[:512]
		}
		return item
	default:
		return value
	}
}
