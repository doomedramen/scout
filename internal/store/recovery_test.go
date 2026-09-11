package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackupRestoreAndRecoveryPauseAuthority(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	s := NewMemory()
	s.SetClock(func() time.Time { return now })
	owner := Owner{ID: NewID(), PasswordHash: "argon2id$fixture"}
	if err := s.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, Session{TokenHash: "session", CSRFHash: "csrf", OwnerID: owner.ID, CreatedAt: now, LastSeen: now, AbsoluteExpiry: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	site, _ := s.CreateSite(ctx, Site{Name: "restore-lab"})
	device, _ := s.CreateDevice(ctx, Device{DisplayName: "restore-target", SiteID: site.ID})
	agent := AgentIdentity{ID: NewID(), DeviceID: device.ID, ExpiresAt: now.Add(-time.Minute)}
	if err := s.CreateAgentIdentity(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateJob(ctx, Job{Kind: "enrollment", DeviceID: device.ID}); err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBackup(backup); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreMemory(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Owner(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, "session"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("recovery did not invalidate sessions: %v", err)
	}
	state, err := s.ReconcileRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.RecoveryMode || !state.EnrollmentPaused || !state.UpdatesPaused {
		t.Fatalf("recovery did not leave authority paused: %+v", state)
	}
	agents, _ := s.Agent(ctx, agent.ID)
	if agents.RevokedAt == nil {
		t.Fatal("expired agent was not revoked during reconciliation")
	}
}
