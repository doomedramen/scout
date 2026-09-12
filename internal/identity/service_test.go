package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func csrForTest(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "test"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestEnrollmentConsumesInvitationAndPreventsSecondAgent(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	site, _ := s.CreateSite(ctx, store.Site{Name: "lab"})
	device, _ := s.CreateDevice(ctx, store.Device{DisplayName: "host", SiteID: site.ID})
	token := "test-invitation-token-123456"
	if err := s.ReserveInvitation(ctx, store.BootstrapInvitation{TokenHash: store.HashToken(token), DeviceID: device.ID, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	authority, err := NewAuthority()
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: s, Authority: authority}
	result, err := service.Enroll(ctx, EnrollmentRequest{Invitation: token, CSRPEM: csrForTest(t), AgentVersion: "0.1.0", Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentID == "" || result.AgentToken == "" {
		t.Fatal("bootstrap did not issue identity")
	}
	updated, err := s.GetDevice(ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Platform != "linux" || updated.Architecture != "amd64" {
		t.Fatalf("enrollment did not record platform: %+v", updated)
	}
	if _, err := service.Enroll(ctx, EnrollmentRequest{Invitation: token, CSRPEM: csrForTest(t), AgentVersion: "0.1.0", Platform: "linux", Architecture: "amd64"}); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("reused invitation: %v", err)
	}
}

func TestExpiredInvitationAndUnsupportedPlatformFailClosed(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	site, _ := s.CreateSite(ctx, store.Site{Name: "lab"})
	device, _ := s.CreateDevice(ctx, store.Device{DisplayName: "host", SiteID: site.ID})
	token := "expired-invitation-token-123456"
	s.ReserveInvitation(ctx, store.BootstrapInvitation{TokenHash: store.HashToken(token), DeviceID: device.ID, ExpiresAt: time.Now().Add(-time.Minute)})
	authority, _ := NewAuthority()
	service := &Service{Store: s, Authority: authority}
	if _, err := service.Enroll(ctx, EnrollmentRequest{Invitation: token, CSRPEM: csrForTest(t), AgentVersion: "0.1.0", Platform: "windows", Architecture: "amd64"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unsupported platform: %v", err)
	}
	if _, err := service.Enroll(ctx, EnrollmentRequest{Invitation: token, CSRPEM: csrForTest(t), AgentVersion: "0.1.0", Platform: "linux", Architecture: "amd64"}); !errors.Is(err, store.ErrExpired) {
		t.Fatalf("expired invitation: %v", err)
	}
}
