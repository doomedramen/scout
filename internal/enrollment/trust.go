package enrollment

import (
	"context"
	"strings"

	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

type TrustDecision struct {
	Allowed     bool
	Reason      string
	Endpoint    string
	Fingerprint string
}

func VerifyTrust(ctx context.Context, repository *store.Store, scopeID, host string, port int, fingerprint string) (TrustDecision, error) {
	if repository == nil || scopeID == "" || host == "" || fingerprint == "" {
		return TrustDecision{}, store.ErrInvalid
	}
	endpoint := secrets.Target(host, port)
	if endpoint == "" {
		return TrustDecision{Reason: "invalid_endpoint"}, nil
	}
	matched, err := repository.ScopeTrust(ctx, scopeID, endpoint, strings.TrimSpace(fingerprint))
	if err != nil {
		return TrustDecision{}, err
	}
	if !matched {
		return TrustDecision{Reason: "host_trust_required", Endpoint: endpoint, Fingerprint: strings.TrimSpace(fingerprint)}, nil
	}
	return TrustDecision{Allowed: true, Reason: "trusted", Endpoint: endpoint, Fingerprint: strings.TrimSpace(fingerprint)}, nil
}

func NormalizeEndpoint(host string, port int) string { return secrets.Target(host, port) }
