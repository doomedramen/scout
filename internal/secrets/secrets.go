package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrKeyUnavailable = errors.New("secret key unavailable")
	ErrTampered       = errors.New("secret ciphertext tampered")
	ErrBadContext     = errors.New("secret context mismatch")
)

type Envelope struct {
	Ciphertext     []byte `json:"ciphertext"`
	Nonce          []byte `json:"nonce"`
	WrappedDataKey []byte `json:"wrappedDataKey"`
	KeyVersion     int    `json:"keyVersion"`
}

type KeyRing struct {
	mu      sync.RWMutex
	keys    map[int][]byte
	current int
}

func NewKeyRing(key []byte) (*KeyRing, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("wrapping key must be 32 bytes")
	}
	copyKey := append([]byte(nil), key...)
	return &KeyRing{keys: map[int][]byte{1: copyKey}, current: 1}, nil
}

func LoadKeyFile(path string) (*KeyRing, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrKeyUnavailable
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("key file permissions are too broad")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	if len(data) != 32 {
		return nil, fmt.Errorf("key file must contain 32 raw bytes")
	}
	return NewKeyRing(data)
}

func (r *KeyRing) CurrentVersion() int { r.mu.RLock(); defer r.mu.RUnlock(); return r.current }

func (r *KeyRing) Rotate(key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("wrapping key must be 32 bytes")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current++
	r.keys[r.current] = append([]byte(nil), key...)
	return nil
}

func (r *KeyRing) RemoveVersion(version int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if version != r.current {
		delete(r.keys, version)
	}
}

func (r *KeyRing) key(version int) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, ok := r.keys[version]
	if !ok {
		return nil, ErrKeyUnavailable
	}
	return append([]byte(nil), key...), nil
}

func contextAAD(kind, id string) []byte {
	sum := sha256.Sum256([]byte("scout/secret/" + kind + "/" + id))
	return sum[:]
}
func wrapAAD(id string, version int) []byte {
	return []byte(fmt.Sprintf("scout/data-key/%d/%s", version, id))
}

func seal(key, plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, aad), nonce, nil
}
func open(key, ciphertext, nonce, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, ErrTampered
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrTampered
	}
	return plaintext, nil
}

func (r *KeyRing) Encrypt(id, kind string, plaintext []byte) (Envelope, error) {
	return r.EncryptSecret(id, kind, plaintext)
}

// Encrypt uses one envelope nonce for the wrapped data key and stores the
// wrapped nonce+ciphertext in WrappedDataKey. Ciphertext remains service data.
func (r *KeyRing) EncryptSecret(id, kind string, plaintext []byte) (Envelope, error) {
	if r == nil {
		return Envelope{}, ErrKeyUnavailable
	}
	version := r.CurrentVersion()
	master, err := r.key(version)
	if err != nil {
		return Envelope{}, err
	}
	dataKey := make([]byte, 32)
	if _, err = rand.Read(dataKey); err != nil {
		return Envelope{}, err
	}
	ciphertext, nonce, err := seal(dataKey, plaintext, contextAAD(kind, id))
	if err != nil {
		return Envelope{}, err
	}
	wrapped, wrapNonce, err := seal(master, dataKey, wrapAAD(id, version))
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Ciphertext: ciphertext, Nonce: nonce, WrappedDataKey: append(wrapNonce, wrapped...), KeyVersion: version}, nil
}

func (r *KeyRing) DecryptSecret(id, kind string, envelope Envelope) ([]byte, error) {
	if r == nil {
		return nil, ErrKeyUnavailable
	}
	master, err := r.key(envelope.KeyVersion)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(envelope.WrappedDataKey) < gcm.NonceSize() {
		return nil, ErrTampered
	}
	wrapNonce := envelope.WrappedDataKey[:gcm.NonceSize()]
	wrapped := envelope.WrappedDataKey[gcm.NonceSize():]
	dataKey, err := gcm.Open(nil, wrapNonce, wrapped, wrapAAD(id, envelope.KeyVersion))
	if err != nil {
		return nil, ErrTampered
	}
	plaintext, err := open(dataKey, envelope.Ciphertext, envelope.Nonce, contextAAD(kind, id))
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func (r *KeyRing) RewrapSecret(id, kind string, envelope Envelope) (Envelope, error) {
	plaintext, err := r.DecryptSecret(id, kind, envelope)
	if err != nil {
		return Envelope{}, err
	}
	return r.EncryptSecret(id, kind, plaintext)
}

func (r *KeyRing) Rewrap(id string, envelope Envelope) (Envelope, error) {
	return r.RewrapSecret(id, "", envelope)
}

func WriteProvisionedKey(path string, key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("wrapping key must be 32 bytes")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o400)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(key); err != nil {
		return err
	}
	return file.Chmod(0o400)
}

func IsMissingKey(err error) bool      { return errors.Is(err, ErrKeyUnavailable) }
func IsPermissionError(err error) bool { return errors.Is(err, fs.ErrPermission) }
func Fingerprint(key []byte) string    { sum := sha256.Sum256(key); return hex.EncodeToString(sum[:8]) }
