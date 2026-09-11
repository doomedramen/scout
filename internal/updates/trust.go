package updates

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"scout.local/scout/internal/store"
)

type TrustStore struct {
	mu      sync.RWMutex
	keys    map[string]ed25519.PublicKey
	revoked map[string]bool
}

func NewTrustStore() *TrustStore {
	return &TrustStore{keys: map[string]ed25519.PublicKey{}, revoked: map[string]bool{}}
}

func KeyID(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:8])
}

func (t *TrustStore) Add(id string, publicKey ed25519.PublicKey) error {
	if t == nil || id == "" || len(publicKey) != ed25519.PublicKeySize {
		return store.ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keys[id] = append(ed25519.PublicKey(nil), publicKey...)
	delete(t.revoked, id)
	return nil
}

func (t *TrustStore) Revoke(id string) error {
	if t == nil || id == "" {
		return store.ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.keys[id]; !ok {
		return store.ErrNotFound
	}
	t.revoked[id] = true
	return nil
}

func (t *TrustStore) Key(id string) (ed25519.PublicKey, error) {
	if t == nil {
		return nil, store.ErrUnauthorized
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	key, ok := t.keys[id]
	if !ok || t.revoked[id] {
		return nil, store.ErrUnauthorized
	}
	return append(ed25519.PublicKey(nil), key...), nil
}

func LoadTrustFile(path string) (*TrustStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read release trust file: %w", err)
	}
	result := NewTrustStore()
	for lineNumber, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("release trust line %d: %w", lineNumber+1, store.ErrInvalid)
		}
		publicKey, decodeErr := base64.RawStdEncoding.DecodeString(fields[1])
		if decodeErr != nil {
			publicKey, decodeErr = base64.StdEncoding.DecodeString(fields[1])
		}
		if decodeErr != nil || len(publicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("release trust line %d: %w", lineNumber+1, store.ErrInvalid)
		}
		if err := result.Add(fields[0], ed25519.PublicKey(publicKey)); err != nil {
			return nil, err
		}
	}
	return result, nil
}
