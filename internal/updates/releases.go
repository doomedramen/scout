package updates

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scout.local/scout/internal/store"
)

type Bundle struct {
	Manifest Manifest `json:"manifest"`
	Artifact string   `json:"artifactBase64"`
}

type ReleaseService struct {
	Store          *store.Store
	Trust          *TrustStore
	ArtifactRoot   string
	MaxBundleBytes int64
}

func NewReleaseService(repository *store.Store, trust *TrustStore, artifactRoot string) *ReleaseService {
	if artifactRoot == "" {
		artifactRoot = "/var/lib/scout/releases"
	}
	return &ReleaseService{Store: repository, Trust: trust, ArtifactRoot: artifactRoot, MaxBundleBytes: MaxArtifactBytes + 1<<20}
}

func DecodeBundle(data []byte, maxBytes int64) (Bundle, []byte, error) {
	if maxBytes <= 0 {
		maxBytes = MaxArtifactBytes + 1<<20
	}
	if int64(len(data)) > maxBytes {
		return Bundle{}, nil, store.ErrBackpressure
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, nil, store.ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Bundle{}, nil, store.ErrInvalid
	}
	artifact, err := base64.RawStdEncoding.DecodeString(bundle.Artifact)
	if err != nil {
		artifact, err = base64.StdEncoding.DecodeString(bundle.Artifact)
	}
	if err != nil {
		return Bundle{}, nil, ErrTampered
	}
	return bundle, artifact, nil
}

func EncodeBundle(manifest Manifest, artifact []byte) ([]byte, error) {
	return json.Marshal(Bundle{Manifest: manifest, Artifact: base64.RawStdEncoding.EncodeToString(artifact)})
}

func (s *ReleaseService) ImportBundle(ctx context.Context, data []byte) (store.Release, error) {
	if s == nil || s.Store == nil || s.Trust == nil {
		return store.Release{}, store.ErrInvalid
	}
	bundle, artifact, err := DecodeBundle(data, s.MaxBundleBytes)
	if err != nil {
		return store.Release{}, err
	}
	if err := bundle.Manifest.Verify(artifact, s.Trust); err != nil {
		return store.Release{}, err
	}
	if err := os.MkdirAll(s.ArtifactRoot, 0o700); err != nil {
		return store.Release{}, err
	}
	digestHex := strings.TrimPrefix(bundle.Manifest.Digest, "sha256:")
	path := filepath.Join(s.ArtifactRoot, digestHex+".artifact")
	if filepath.Dir(path) != filepath.Clean(s.ArtifactRoot) {
		return store.Release{}, store.ErrInvalid
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if ArtifactDigest(existing) != bundle.Manifest.Digest {
			return store.Release{}, ErrTampered
		}
	} else if os.IsNotExist(readErr) {
		if err := atomicArtifactWrite(path, artifact); err != nil {
			return store.Release{}, err
		}
	} else {
		return store.Release{}, readErr
	}
	manifestHash, err := ManifestHash(bundle.Manifest)
	if err != nil {
		return store.Release{}, err
	}
	manifestData, _ := json.Marshal(bundle.Manifest)
	var manifestMap map[string]any
	_ = json.Unmarshal(manifestData, &manifestMap)
	release, err := s.Store.PutRelease(ctx, store.Release{ManifestHash: manifestHash, Version: bundle.Manifest.Version, Generation: bundle.Manifest.Generation, Platform: bundle.Manifest.Platform, Architecture: bundle.Manifest.Architecture, Digest: bundle.Manifest.Digest, Bytes: bundle.Manifest.Bytes, TrustKeyID: bundle.Manifest.KeyID, ImmutablePath: path, Manifest: manifestMap})
	if err == nil {
		return release, nil
	}
	if err == store.ErrConflict {
		items, listErr := s.Store.ListReleases(ctx, bundle.Manifest.Platform)
		if listErr == nil {
			for _, item := range items {
				if item.ManifestHash == manifestHash {
					return item, nil
				}
			}
		}
	}
	return store.Release{}, err
}

func atomicArtifactWrite(path string, artifact []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".release-*.partial")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryPath) }
	defer cleanup()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(artifact); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *ReleaseService) Artifact(ctx context.Context, releaseID, digest string) ([]byte, error) {
	if s == nil || s.Store == nil {
		return nil, store.ErrInvalid
	}
	release, err := s.Store.GetRelease(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	if release.RevokedAt != nil || digest == "" || release.Digest != digest {
		return nil, store.ErrForbidden
	}
	if filepath.Base(release.ImmutablePath) != strings.TrimPrefix(digest, "sha256:")+".artifact" {
		return nil, store.ErrForbidden
	}
	file, err := os.Open(release.ImmutablePath)
	if err != nil {
		return nil, store.ErrNotFound
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxArtifactBytes+1))
	if err != nil || int64(len(data)) != release.Bytes || ArtifactDigest(data) != release.Digest {
		return nil, ErrTampered
	}
	return data, nil
}

func ArtifactFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func (s *ReleaseService) Revoke(ctx context.Context, releaseID string) error {
	if s == nil || s.Store == nil {
		return fmt.Errorf("release store unavailable")
	}
	return s.Store.RevokeRelease(ctx, releaseID)
}
