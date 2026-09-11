package policy

import (
	"context"
	"net/netip"
	"strings"

	"scout.local/scout/internal/store"
)

type Engine struct{ Store *store.Store }

type Decision struct {
	Allowed           bool
	Reason            string
	ScopeRevision     int64
	CredentialVersion int64
}

func (e *Engine) Evaluate(ctx context.Context, scopeID, destination, method string, port int) (Decision, error) {
	if e == nil || e.Store == nil {
		return Decision{}, store.ErrInvalid
	}
	scope, err := e.Store.GetScope(ctx, scopeID)
	if err != nil {
		return Decision{}, err
	}
	if !scope.Enabled {
		return Decision{Reason: "scope_paused", ScopeRevision: scope.Revision}, nil
	}
	if !methodAllowed(scope.AllowedMethods, method) {
		return Decision{Reason: "method_not_allowed", ScopeRevision: scope.Revision}, nil
	}
	if !portAllowed(scope.Ports, port) {
		return Decision{Reason: "port_not_allowed", ScopeRevision: scope.Revision}, nil
	}
	addr, err := netip.ParseAddr(destination)
	if err != nil {
		return Decision{Reason: "destination_must_be_literal_address", ScopeRevision: scope.Revision}, nil
	}
	if excluded(scope.Exclusions, destination, addr) {
		return Decision{Reason: "excluded", ScopeRevision: scope.Revision}, nil
	}
	inside := false
	for _, raw := range scope.Ranges {
		if prefix, parseErr := netip.ParsePrefix(raw); parseErr == nil && prefix.Contains(addr) {
			inside = true
		}
		if parsed, parseErr := netip.ParseAddr(raw); parseErr == nil && parsed == addr {
			inside = true
		}
	}
	if !inside {
		return Decision{Reason: "outside_scope", ScopeRevision: scope.Revision}, nil
	}
	credentialVersion, credentialErr := e.Store.ScopeCredentialVersion(ctx, scopeID)
	if credentialErr != nil && scope.CredentialRef != "" {
		return Decision{Reason: "needs_credentials", ScopeRevision: scope.Revision}, nil
	}
	return Decision{Allowed: true, Reason: "eligible", ScopeRevision: scope.Revision, CredentialVersion: credentialVersion}, nil
}

func methodAllowed(methods []string, target string) bool {
	for _, method := range methods {
		if method == target {
			return true
		}
	}
	return false
}
func portAllowed(ports []int, target int) bool {
	for _, port := range ports {
		if port == target {
			return true
		}
	}
	return false
}
func excluded(exclusions []string, raw string, addr netip.Addr) bool {
	for _, item := range exclusions {
		if strings.EqualFold(strings.TrimSpace(item), raw) {
			return true
		}
		if prefix, err := netip.ParsePrefix(item); err == nil && prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func ValidateRanges(ranges []string) error {
	if len(ranges) == 0 || len(ranges) > 128 {
		return store.ErrInvalid
	}
	for _, raw := range ranges {
		if _, err := netip.ParsePrefix(raw); err != nil {
			if _, addrErr := netip.ParseAddr(raw); addrErr != nil {
				return store.ErrInvalid
			}
		}
	}
	return nil
}
