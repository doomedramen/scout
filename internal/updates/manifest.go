package updates

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"scout.local/scout/internal/store"
)

var ErrTampered = errors.New("release artifact tampered")

const (
	ManifestSchemaVersion = 1
	MaxArtifactBytes      = 128 << 20
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Version       string `json:"version"`
	Generation    int64  `json:"generation"`
	Platform      string `json:"platform"`
	Architecture  string `json:"architecture"`
	Digest        string `json:"digest"`
	Bytes         int64  `json:"bytes"`
	ProtocolMin   int    `json:"protocolMin"`
	ProtocolMax   int    `json:"protocolMax"`
	KeyID         string `json:"keyId"`
	Signature     string `json:"signature"`
}

type manifestPayload struct {
	SchemaVersion int    `json:"schemaVersion"`
	Version       string `json:"version"`
	Generation    int64  `json:"generation"`
	Platform      string `json:"platform"`
	Architecture  string `json:"architecture"`
	Digest        string `json:"digest"`
	Bytes         int64  `json:"bytes"`
	ProtocolMin   int    `json:"protocolMin"`
	ProtocolMax   int    `json:"protocolMax"`
	KeyID         string `json:"keyId"`
}

func (m Manifest) payload() ([]byte, error) {
	return json.Marshal(manifestPayload{SchemaVersion: m.SchemaVersion, Version: m.Version, Generation: m.Generation, Platform: m.Platform, Architecture: m.Architecture, Digest: m.Digest, Bytes: m.Bytes, ProtocolMin: m.ProtocolMin, ProtocolMax: m.ProtocolMax, KeyID: m.KeyID})
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != ManifestSchemaVersion || !versionPattern.MatchString(m.Version) || m.Generation < 1 || m.Platform != "linux" || (m.Architecture != "amd64" && m.Architecture != "arm64") || m.Bytes < 1 || m.Bytes > MaxArtifactBytes || m.ProtocolMin < 1 || m.ProtocolMax < m.ProtocolMin || m.KeyID == "" || m.Signature == "" {
		return store.ErrInvalid
	}
	if !strings.HasPrefix(m.Digest, "sha256:") || len(strings.TrimPrefix(m.Digest, "sha256:")) != sha256.Size*2 {
		return store.ErrInvalid
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(m.Digest, "sha256:")); err != nil {
		return store.ErrInvalid
	}
	return nil
}

func ArtifactDigest(artifact []byte) string {
	sum := sha256.Sum256(artifact)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func SignManifest(manifest Manifest, privateKey ed25519.PrivateKey, keyID string) (Manifest, error) {
	if len(privateKey) != ed25519.PrivateKeySize || keyID == "" {
		return Manifest{}, store.ErrInvalid
	}
	manifest.KeyID = keyID
	manifest.Signature = ""
	if err := manifest.validateUnsigned(); err != nil {
		return Manifest{}, err
	}
	payload, err := manifest.payload()
	if err != nil {
		return Manifest{}, err
	}
	manifest.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return manifest, nil
}

func (m Manifest) validateUnsigned() error {
	copy := m
	copy.Signature = "signed"
	return copy.Validate()
}

func (m Manifest) Verify(artifact []byte, trust *TrustStore) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if int64(len(artifact)) != m.Bytes || ArtifactDigest(artifact) != m.Digest {
		return ErrTampered
	}
	if trust == nil {
		return store.ErrUnauthorized
	}
	publicKey, err := trust.Key(m.KeyID)
	if err != nil {
		return err
	}
	signature, err := base64.RawStdEncoding.DecodeString(m.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrTampered
	}
	payload, err := m.payload()
	if err != nil || !ed25519.Verify(publicKey, payload, signature) {
		return ErrTampered
	}
	return nil
}

func ManifestHash(manifest Manifest) (string, error) {
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func Compatible(m Manifest, platform, architecture string, protocol int) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.Platform != platform || m.Architecture != architecture || protocol < m.ProtocolMin || protocol > m.ProtocolMax {
		return fmt.Errorf("release is incompatible: %w", store.ErrInvalid)
	}
	return nil
}

func IsDowngrade(generation, installedGeneration int64) bool {
	return installedGeneration > 0 && generation < installedGeneration
}
