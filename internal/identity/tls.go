package identity

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

type TLSSettings struct {
	CertificateFile string
	PrivateKeyFile  string
	ClientCAFile    string
	RequireClient   bool
	Production      bool
}

func (s TLSSettings) Validate() error {
	if !s.Production {
		return nil
	}
	if s.CertificateFile == "" || s.PrivateKeyFile == "" || s.ClientCAFile == "" {
		return fmt.Errorf("production TLS requires server certificate, key, and client CA")
	}
	if _, err := os.Stat(s.CertificateFile); err != nil {
		return fmt.Errorf("server certificate unavailable")
	}
	if _, err := os.Stat(s.PrivateKeyFile); err != nil {
		return fmt.Errorf("server private key unavailable")
	}
	if _, err := os.Stat(s.ClientCAFile); err != nil {
		return fmt.Errorf("client CA unavailable")
	}
	return nil
}

func LoadServerTLS(settings TLSSettings) (*tls.Config, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if settings.CertificateFile == "" || settings.PrivateKeyFile == "" {
		return nil, fmt.Errorf("TLS certificate and key are required")
	}
	certificate, err := tls.LoadX509KeyPair(settings.CertificateFile, settings.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server certificate: %w", err)
	}
	pool := x509.NewCertPool()
	ca, err := os.ReadFile(settings.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("client CA contains no certificates")
	}
	clientAuth := tls.RequireAndVerifyClientCert
	if !settings.RequireClient && !settings.Production {
		clientAuth = tls.VerifyClientCertIfGiven
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: clientAuth, ClientCAs: pool, CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}}, nil
}

func ValidateAgentPeer(state tls.ConnectionState) (string, error) {
	if len(state.PeerCertificates) != 1 {
		return "", fmt.Errorf("agent client certificate required")
	}
	certificate := state.PeerCertificates[0]
	if len(certificate.SerialNumber.Bytes()) == 0 {
		return "", fmt.Errorf("agent certificate has no serial")
	}
	return certificate.SerialNumber.String(), nil
}
