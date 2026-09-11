package identity

import (
	"crypto/tls"
	"testing"
)

func TestProductionTLSRequiresExplicitTrustMaterial(t *testing.T) {
	if err := (TLSSettings{Production: true}).Validate(); err == nil {
		t.Fatal("production TLS accepted missing trust material")
	}
}
func TestAgentPeerCannotBeDerivedFromMissingCertificate(t *testing.T) {
	if _, err := ValidateAgentPeer(tls.ConnectionState{}); err == nil {
		t.Fatal("missing peer certificate accepted")
	}
}
