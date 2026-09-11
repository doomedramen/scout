package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStoreEnforcesSingletonOwnerAndSessionExpiry(t *testing.T) {
	s := NewMemory()
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	owner := Owner{ID: NewID(), PasswordHash: "argon2id$fixture"}
	if err := s.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOwner(ctx, owner); !errors.Is(err, ErrConflict) {
		t.Fatalf("second owner setup: %v", err)
	}
	if err := s.CreateSession(ctx, Session{TokenHash: "token", CSRFHash: "csrf", OwnerID: owner.ID, CreatedAt: now, LastSeen: now, AbsoluteExpiry: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, "token"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Minute)
	if _, err := s.Session(ctx, "token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired idle session: %v", err)
	}
}

func TestStoreSerializesConcurrentEnrollmentJobs(t *testing.T) {
	s := NewMemory()
	ctx := context.Background()
	site, err := s.CreateSite(ctx, Site{Name: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := s.CreateDevice(ctx, Device{DisplayName: "target", SiteID: site.ID})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateJob(ctx, Job{Kind: "enrollment", DeviceID: device.ID})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	created := 0
	duplicates := 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrDuplicate):
			duplicates++
		default:
			t.Fatalf("unexpected job error: %v", err)
		}
	}
	if created != 1 || duplicates != 19 {
		t.Fatalf("created=%d duplicates=%d", created, duplicates)
	}
}
