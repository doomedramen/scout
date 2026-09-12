package secrets

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"scout.local/scout/internal/store"
)

type Broker struct {
	Store   *store.Store
	KeyRing *KeyRing
}

// RedeemForTarget is the only credential redemption path. It requires an
// explicit operation and exact target binding; ordinary agents never receive
// a Broker and therefore cannot redeem owner credentials.
func (b *Broker) RedeemForTarget(ctx context.Context, credentialID, operation, target string) ([]byte, error) {
	if b == nil || b.Store == nil || b.KeyRing == nil || credentialID == "" || operation == "" || target == "" {
		return nil, store.ErrForbidden
	}
	credential, err := b.Store.Credential(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	if credential.RevokedAt != nil || !contains(credential.AllowedUse, operation) || !targetAllowed(credential.Targets, target) {
		return nil, store.ErrForbidden
	}
	return b.KeyRing.DecryptSecret(credential.ID, credential.Kind, Envelope{Ciphertext: credential.Ciphertext, Nonce: credential.Nonce, WrappedDataKey: credential.WrappedDataKey, KeyVersion: credential.KeyVersion})
}

func targetAllowed(targets []string, target string) bool {
	normalized := normalizeTarget(target)
	for _, item := range targets {
		if normalizeTarget(item) == normalized {
			return true
		}
	}
	return false
}

// TargetAllowed checks a credential's target binding without decrypting its
// secret. Enrollment re-evaluation uses this to reject unrelated credentials
// while keeping secret material inside the broker boundary.
func TargetAllowed(targets []string, target string) bool {
	return targetAllowed(targets, target)
}

func normalizeTarget(value string) string {
	value = strings.TrimSpace(value)
	if host, port, err := net.SplitHostPort(value); err == nil {
		if address, parseErr := netip.ParseAddr(host); parseErr == nil {
			return net.JoinHostPort(address.String(), port)
		}
		return net.JoinHostPort(strings.ToLower(host), port)
	}
	if address, err := netip.ParseAddr(value); err == nil {
		return address.String()
	}
	return strings.ToLower(value)
}

func Target(host string, port int) string {
	if port < 1 || port > 65535 {
		return ""
	}
	if address, err := netip.ParseAddr(strings.TrimSpace(host)); err == nil {
		return net.JoinHostPort(address.String(), strconv.Itoa(port))
	}
	return net.JoinHostPort(strings.ToLower(strings.TrimSpace(host)), strconv.Itoa(port))
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
