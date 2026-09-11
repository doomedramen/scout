package control

import (
	"net/http"
	"testing"

	"scout.local/scout/internal/store"
)

func TestMapErrorExplainsRecentMFARequirement(t *testing.T) {
	status, code, message, retryable := mapError(store.ErrRecentMFA)
	if status != http.StatusForbidden || code != "recent_mfa_required" || message == "Action not permitted" || retryable {
		t.Fatalf("recent MFA mapping = %d %q %q %t", status, code, message, retryable)
	}
}
