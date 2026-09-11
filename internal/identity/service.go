package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

type Authority struct {
	certificate *x509.Certificate
	privateKey  *ecdsa.PrivateKey
	CAPEM       []byte
}

func NewAuthority() (*Authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	certificate, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Scout local CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}, &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Scout local CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParseCertificate(certificate)
	if err != nil {
		return nil, err
	}
	return &Authority{certificate: parsed, privateKey: key, CAPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})}, nil
}

// LoadAuthority loads the persistent CA used to sign agent certificates. The
// server's client-CA trust bundle must contain the same certificate.
func LoadAuthority(certificatePath, privateKeyPath string) (*Authority, error) {
	if strings.TrimSpace(certificatePath) == "" || strings.TrimSpace(privateKeyPath) == "" {
		return nil, fmt.Errorf("agent CA certificate and private key are required")
	}
	certificatePEM, err := os.ReadFile(certificatePath)
	if err != nil {
		return nil, fmt.Errorf("read agent CA certificate: %w", err)
	}
	privateKeyPEM, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read agent CA private key: %w", err)
	}
	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse agent CA key pair: %w", err)
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse agent CA certificate: %w", err)
	}
	key, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok || !certificate.IsCA || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, fmt.Errorf("agent CA must be an ECDSA certificate-signing CA")
	}
	if err := certificate.CheckSignatureFrom(certificate); err != nil {
		return nil, fmt.Errorf("agent CA self-signature invalid")
	}
	return &Authority{certificate: certificate, privateKey: key, CAPEM: append([]byte(nil), certificatePEM...)}, nil
}

func randomSerial() (*big.Int, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(raw), nil
}

func (a *Authority) IssueCSR(csr *x509.CertificateRequest, deviceID string, validity time.Duration) (certPEM, token string, serial string, pubHash string, err error) {
	if a == nil || a.certificate == nil || a.privateKey == nil {
		return "", "", "", "", fmt.Errorf("identity authority unavailable")
	}
	if csr == nil {
		return "", "", "", "", fmt.Errorf("CSR required")
	}
	if err := csr.CheckSignature(); err != nil {
		return "", "", "", "", fmt.Errorf("invalid CSR signature")
	}
	if validity <= 0 {
		validity = 30 * 24 * time.Hour
	}
	certSerial, err := randomSerial()
	if err != nil {
		return "", "", "", "", err
	}
	now := time.Now().UTC()
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: certSerial, Subject: pkix.Name{CommonName: "scout-agent/" + deviceID}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(validity), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}, a.certificate, csr.PublicKey, a.privateKey)
	if err != nil {
		return "", "", "", "", err
	}
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return "", "", "", "", err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if err != nil {
		return "", "", "", "", err
	}
	sum := sha256.Sum256(pubDER)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), base64.RawURLEncoding.EncodeToString(rawToken), certSerial.String(), fmt.Sprintf("sha256:%x", sum[:]), nil
}

type EnrollmentRequest struct {
	Invitation   string
	CSRPEM       string
	AgentVersion string
	Platform     string
	Architecture string
}
type EnrollmentResult struct {
	DeviceID        string    `json:"deviceId"`
	AgentID         string    `json:"agentId"`
	AgentToken      string    `json:"agentToken,omitempty"`
	CertificatePEM  string    `json:"certificatePem"`
	CABundlePEM     string    `json:"caBundlePem"`
	ExpiresAt       time.Time `json:"expiresAt"`
	ProtocolVersion int       `json:"protocolVersion"`
}

type Service struct {
	Store     *store.Store
	Authority *Authority
	Now       func() time.Time
}

func (s *Service) Enroll(ctx context.Context, request EnrollmentRequest) (EnrollmentResult, error) {
	if s == nil || s.Store == nil || s.Authority == nil {
		return EnrollmentResult{}, fmt.Errorf("identity service unavailable")
	}
	if request.Invitation == "" || request.CSRPEM == "" {
		return EnrollmentResult{}, store.ErrUnauthorized
	}
	if request.Platform != "linux" || (request.Architecture != "amd64" && request.Architecture != "arm64") {
		return EnrollmentResult{}, store.ErrInvalid
	}
	invitation, err := s.Store.ConsumeInvitation(ctx, store.HashToken(request.Invitation))
	if err != nil {
		return EnrollmentResult{}, err
	}
	device, err := s.Store.GetDevice(ctx, invitation.DeviceID)
	if err != nil {
		return EnrollmentResult{}, err
	}
	if device.Excluded || device.Lifecycle == "decommissioned" {
		return EnrollmentResult{}, store.ErrForbidden
	}
	block, _ := pem.Decode([]byte(request.CSRPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return EnrollmentResult{}, store.ErrInvalid
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return EnrollmentResult{}, store.ErrInvalid
	}
	if err := csr.CheckSignature(); err != nil {
		return EnrollmentResult{}, store.ErrInvalid
	}
	certPEM, token, serial, pubHash, err := s.Authority.IssueCSR(csr, device.ID, 30*24*time.Hour)
	if err != nil {
		return EnrollmentResult{}, err
	}
	now := s.clock()
	identity := store.AgentIdentity{ID: store.NewID(), DeviceID: device.ID, PublicKeyHash: pubHash, CertSerial: serial, AuthTokenHash: store.HashToken(token), CertificatePEM: string(certPEM), ExpiresAt: now.Add(30 * 24 * time.Hour), InstalledVersion: request.AgentVersion}
	if err := s.Store.CreateAgentIdentity(ctx, identity); err != nil {
		return EnrollmentResult{}, err
	}
	return EnrollmentResult{DeviceID: device.ID, AgentID: identity.ID, AgentToken: token, CertificatePEM: string(certPEM), CABundlePEM: string(s.Authority.CAPEM), ExpiresAt: identity.ExpiresAt, ProtocolVersion: 1}, nil
}

func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) Renew(ctx context.Context, agentID, csrPEM string) (EnrollmentResult, error) {
	identity, err := s.Store.Agent(ctx, agentID)
	if err != nil {
		return EnrollmentResult{}, err
	}
	if identity.RevokedAt != nil || !s.clock().Before(identity.ExpiresAt) {
		return EnrollmentResult{}, store.ErrRevoked
	}
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return EnrollmentResult{}, store.ErrInvalid
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return EnrollmentResult{}, store.ErrInvalid
	}
	certPEM, token, serial, pubHash, err := s.Authority.IssueCSR(csr, identity.DeviceID, 30*24*time.Hour)
	if err != nil {
		return EnrollmentResult{}, err
	}
	expires := s.clock().Add(30 * 24 * time.Hour)
	updated, err := s.Store.UpdateAgent(ctx, agentID, func(item *store.AgentIdentity) error {
		item.CertSerial = serial
		item.PublicKeyHash = pubHash
		item.CertificatePEM = string(certPEM)
		item.AuthTokenHash = store.HashToken(token)
		item.ExpiresAt = expires
		return nil
	})
	if err != nil {
		return EnrollmentResult{}, err
	}
	return EnrollmentResult{DeviceID: updated.DeviceID, AgentID: updated.ID, AgentToken: token, CertificatePEM: string(certPEM), CABundlePEM: string(s.Authority.CAPEM), ExpiresAt: updated.ExpiresAt, ProtocolVersion: 1}, nil
}

func (s *Service) AgentFromRequest(ctx context.Context, token string, state *tls.ConnectionState) (store.AgentIdentity, error) {
	if state != nil {
		serial, err := ValidateAgentPeer(*state)
		if err != nil {
			return store.AgentIdentity{}, store.ErrUnauthorized
		}
		return s.Store.AgentBySerial(ctx, serial)
	}
	if token == "" {
		return store.AgentIdentity{}, store.ErrUnauthorized
	}
	return s.Store.AgentByTokenHash(ctx, store.HashToken(token))
}

func (s *Service) CertificatePool() (*x509.CertPool, error) {
	if s == nil || s.Authority == nil {
		return nil, fmt.Errorf("authority unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(s.Authority.CAPEM) {
		return nil, fmt.Errorf("invalid CA")
	}
	return pool, nil
}

func PublicKeyHash(publicKey any) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return fmt.Sprintf("sha256:%x", sum[:]), nil
}
func IsAgentCertificate(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			return strings.HasPrefix(cert.Subject.CommonName, "scout-agent/")
		}
	}
	return false
}
