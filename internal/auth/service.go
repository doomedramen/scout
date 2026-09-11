package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

type Service struct {
	Store          *store.Store
	KeyRing        *secrets.KeyRing
	SetupTokenHash string
	SetupExpiresAt time.Time
	Now            func() time.Time
	mu             sync.Mutex
	pendingMFA     map[string]string
}

type SessionResult struct {
	SessionToken string      `json:"-"`
	CSRFToken    string      `json:"csrfToken"`
	Owner        store.Owner `json:"owner"`
}

func NewService(repository *store.Store, setupToken string, keyRing *secrets.KeyRing) *Service {
	return &Service{Store: repository, SetupTokenHash: store.HashToken(setupToken), SetupExpiresAt: time.Now().UTC().Add(5 * time.Minute), KeyRing: keyRing, Now: func() time.Time { return time.Now().UTC() }, pendingMFA: map[string]string{}}
}
func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) Setup(ctx context.Context, setupToken, password string) (store.Owner, error) {
	if s == nil || s.Store == nil {
		return store.Owner{}, store.ErrInvalid
	}
	if !strings.EqualFold(store.HashToken(setupToken), s.SetupTokenHash) || !s.clock().Before(s.SetupExpiresAt) {
		return store.Owner{}, store.ErrUnauthorized
	}
	hash, err := HashPassword(password)
	if err != nil {
		return store.Owner{}, store.ErrInvalid
	}
	owner := store.Owner{ID: store.NewID(), PasswordHash: hash, CreatedAt: s.clock()}
	if err := s.Store.CreateOwner(ctx, owner); err != nil {
		return store.Owner{}, err
	}
	return owner, nil
}

func randomSecret(length int) (string, error) {
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Service) SignIn(ctx context.Context, password, totpCode, recoveryCode string) (SessionResult, error) {
	owner, err := s.Store.Owner(ctx)
	if err != nil {
		return SessionResult{}, store.ErrUnauthorized
	}
	if !CheckPassword(owner.PasswordHash, password) {
		return SessionResult{}, store.ErrUnauthorized
	}
	if owner.MFAEnabled && !s.verifySecondFactor(ctx, &owner, totpCode, recoveryCode) {
		return SessionResult{}, store.ErrUnauthorized
	}
	token, err := randomSecret(32)
	if err != nil {
		return SessionResult{}, err
	}
	csrf, err := randomSecret(24)
	if err != nil {
		return SessionResult{}, err
	}
	now := s.clock()
	session := store.Session{TokenHash: store.HashToken(token), CSRFHash: store.HashToken(csrf), OwnerID: owner.ID, CreatedAt: now, LastSeen: now, AbsoluteExpiry: now.Add(8 * time.Hour)}
	if owner.MFAEnabled {
		session.RecentMFAAt = &now
	}
	if err := s.Store.CreateSession(ctx, session); err != nil {
		return SessionResult{}, err
	}
	return SessionResult{SessionToken: token, CSRFToken: csrf, Owner: owner}, nil
}

func (s *Service) verifySecondFactor(ctx context.Context, owner *store.Owner, totpCode, recoveryCode string) bool {
	if s.KeyRing == nil {
		return false
	}
	if totpCode != "" {
		secret, err := s.KeyRing.DecryptSecret(owner.ID, "owner-totp", secrets.Envelope{Ciphertext: owner.TOTPSecretCiphertext, Nonce: owner.TOTPSecretNonce, WrappedDataKey: owner.TOTPWrappedDataKey, KeyVersion: owner.TOTPKeyVersion})
		if err == nil && VerifyTOTP(string(secret), totpCode, s.clock()) {
			return true
		}
	}
	if recoveryCode != "" {
		sum := sha256.Sum256([]byte(strings.TrimSpace(recoveryCode)))
		encoded := hex.EncodeToString(sum[:])
		for _, storedHash := range owner.RecoveryCodeHashes {
			if subtleEqual(encoded, storedHash) {
				return s.consumeRecovery(ctx, owner, storedHash)
			}
		}
	}
	return false
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
func (s *Service) consumeRecovery(ctx context.Context, owner *store.Owner, used string) bool {
	remaining := make([]string, 0, len(owner.RecoveryCodeHashes))
	for _, item := range owner.RecoveryCodeHashes {
		if item != used {
			remaining = append(remaining, item)
		}
	}
	owner.RecoveryCodeHashes = remaining
	return s.Store.UpdateOwner(ctx, *owner) == nil
}

func (s *Service) ValidateSession(ctx context.Context, token string) (store.Session, error) {
	if token == "" {
		return store.Session{}, store.ErrUnauthorized
	}
	session, err := s.Store.Session(ctx, store.HashToken(token))
	if err != nil {
		return store.Session{}, err
	}
	_ = s.Store.TouchSession(ctx, session.TokenHash, s.clock())
	return session, nil
}
func (s *Service) RevokeSession(ctx context.Context, token string) error {
	return s.Store.RevokeSession(ctx, store.HashToken(token))
}
func (s *Service) Reauth(ctx context.Context, token, password, totpCode string) error {
	session, err := s.ValidateSession(ctx, token)
	if err != nil {
		return err
	}
	owner, err := s.Store.Owner(ctx)
	if err != nil || owner.ID != session.OwnerID || !CheckPassword(owner.PasswordHash, password) {
		return store.ErrUnauthorized
	}
	if !owner.MFAEnabled || !s.verifySecondFactor(ctx, &owner, totpCode, "") {
		return store.ErrUnauthorized
	}
	return s.Store.SetSessionMFA(ctx, session.TokenHash, s.clock())
}
func (s *Service) RequireRecentMFA(ctx context.Context, token string) (store.Session, error) {
	session, err := s.ValidateSession(ctx, token)
	if err != nil {
		return store.Session{}, err
	}
	if !s.Store.HasRecentMFA(session) {
		return store.Session{}, store.ErrRecentMFA
	}
	return session, nil
}

func (s *Service) BeginMFASetup(ctx context.Context, token, password string) (string, error) {
	session, err := s.ValidateSession(ctx, token)
	if err != nil {
		return "", err
	}
	owner, err := s.Store.Owner(ctx)
	if err != nil || owner.ID != session.OwnerID || !CheckPassword(owner.PasswordHash, password) {
		return "", store.ErrUnauthorized
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.pendingMFA[owner.ID] = secret
	s.mu.Unlock()
	return secret, nil
}

func (s *Service) ConfirmMFA(ctx context.Context, token, code string) ([]string, error) {
	session, err := s.ValidateSession(ctx, token)
	if err != nil {
		return nil, err
	}
	owner, err := s.Store.Owner(ctx)
	if err != nil || owner.ID != session.OwnerID {
		return nil, store.ErrUnauthorized
	}
	s.mu.Lock()
	secret := s.pendingMFA[owner.ID]
	delete(s.pendingMFA, owner.ID)
	s.mu.Unlock()
	if secret == "" || !VerifyTOTP(secret, code, s.clock()) {
		return nil, store.ErrUnauthorized
	}
	if s.KeyRing == nil {
		return nil, secrets.ErrKeyUnavailable
	}
	envelope, err := s.KeyRing.EncryptSecret(owner.ID, "owner-totp", []byte(secret))
	if err != nil {
		return nil, err
	}
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		codes[i], err = randomSecret(6)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(codes[i]))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	owner.TOTPSecretCiphertext = envelope.Ciphertext
	owner.TOTPSecretNonce = envelope.Nonce
	owner.TOTPWrappedDataKey = envelope.WrappedDataKey
	owner.TOTPKeyVersion = envelope.KeyVersion
	owner.MFAEnabled = true
	owner.RecoveryCodeHashes = hashes
	if err := s.Store.UpdateOwner(ctx, owner); err != nil {
		return nil, err
	}
	_ = s.Store.SetSessionMFA(ctx, session.TokenHash, s.clock())
	return codes, nil
}

func (s *Service) RecoveryCodes(ctx context.Context, token, password, totpCode string) ([]string, error) {
	session, err := s.ValidateSession(ctx, token)
	if err != nil {
		return nil, err
	}
	owner, err := s.Store.Owner(ctx)
	if err != nil || owner.ID != session.OwnerID || !CheckPassword(owner.PasswordHash, password) || !s.verifySecondFactor(ctx, &owner, totpCode, "") {
		return nil, store.ErrUnauthorized
	}
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		codes[i], err = randomSecret(6)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(codes[i]))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	owner.RecoveryCodeHashes = hashes
	if err := s.Store.UpdateOwner(ctx, owner); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) LocalRecover(ctx context.Context, password string) error {
	owner, err := s.Store.Owner(ctx)
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	owner.PasswordHash = hash
	owner.MFAEnabled = false
	owner.TOTPSecretCiphertext = nil
	owner.TOTPSecretNonce = nil
	owner.TOTPWrappedDataKey = nil
	owner.RecoveryCodeHashes = nil
	if err := s.Store.UpdateOwner(ctx, owner); err != nil {
		return err
	}
	return s.Store.RevokeAllSessions(ctx)
}

func (s *Service) SessionCookieName() string { return "scout_session" }
func (s *Service) CSRFHeaderName() string    { return "X-CSRF-Token" }
