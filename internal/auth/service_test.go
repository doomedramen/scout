package auth

import (
	"bytes"
	"context"
	"testing"
	"time"

	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
)

func TestSetupIsSingleUseAndSignInReturnsOpaqueSession(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	keyring, _ := secrets.NewKeyRing(bytes.Repeat([]byte{4}, 32))
	service := NewService(repository, "setup-secret-123456789", keyring)
	owner, err := service.Setup(ctx, "setup-secret-123456789", "ScoutAa1")
	if err != nil {
		t.Fatal(err)
	}
	if owner.PasswordHash == "ScoutAa1" {
		t.Fatal("password stored in plaintext")
	}
	if _, err := service.Setup(ctx, "setup-secret-123456789", "OtherAa1"); err == nil {
		t.Fatal("second setup succeeded")
	}
	session, err := service.SignIn(ctx, "ScoutAa1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionToken == "" || session.CSRFToken == "" {
		t.Fatal("session tokens missing")
	}
	if session.SessionToken == store.HashToken(session.SessionToken) {
		t.Fatal("session token returned as hash")
	}
}

func TestMFAProtectsSensitiveReauthenticationAndRecoveryCodeIsSingleUse(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	keyring, _ := secrets.NewKeyRing(bytes.Repeat([]byte{5}, 32))
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	service := NewService(repository, "setup-secret-123456789", keyring)
	service.Now = func() time.Time { return now }
	repository.SetClock(func() time.Time { return now })
	_, err := service.Setup(ctx, "setup-secret-123456789", "ScoutAa1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.SignIn(ctx, "ScoutAa1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := service.BeginMFASetup(ctx, session.SessionToken, "ScoutAa1")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := TOTPCode(secret, now)
	codes, err := service.ConfirmMFA(ctx, session.SessionToken, code)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("recovery codes=%d", len(codes))
	}
	if _, err := service.SignIn(ctx, "ScoutAa1", "", codes[0]); err != nil {
		t.Fatalf("recovery sign-in: %v", err)
	}
	if _, err := service.SignIn(ctx, "ScoutAa1", "", codes[0]); err == nil {
		t.Fatal("recovery code reused")
	}
	if err := service.Reauth(ctx, session.SessionToken, "ScoutAa1", "000000"); err == nil {
		t.Fatal("invalid MFA accepted")
	}
}
