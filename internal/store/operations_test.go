package store

import (
	"context"
	"errors"
	"testing"
)

func TestReenableRequiresCurrentEnabledScopeAndPreservesHistory(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	site, err := s.CreateSite(ctx, Site{Name: "reenable"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := s.CreateDevice(ctx, Device{DisplayName: "host", SiteID: site.ID, Addresses: []string{"192.0.2.10"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecommissionDevice(ctx, device.ID, "owner requested removal"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReenableDevice(ctx, device.ID, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reenable without current scope was accepted: %v", err)
	}
	if _, err := s.CreateScope(ctx, Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	reenabled, err := s.ReenableDevice(ctx, device.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reenabled.Excluded || reenabled.Lifecycle != "candidate" || reenabled.Availability != AvailabilityConnecting {
		t.Fatalf("reenable state was not reset: %+v", reenabled)
	}
}

func TestDecommissionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	device, err := s.CreateDevice(ctx, Device{DisplayName: "host"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.DecommissionDevice(ctx, device.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.DecommissionDevice(ctx, device.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	if first.DecommissionedAt == nil || second.DecommissionedAt == nil || !first.DecommissionedAt.Equal(*second.DecommissionedAt) {
		t.Fatalf("repeat decommission changed state: first=%+v second=%+v", first, second)
	}
}

func TestCollectorConfigRejectsSecretLikeFieldsAndFencesRevisions(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	device, err := s.CreateDevice(ctx, Device{DisplayName: "collector-host"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := s.PutCollectorConfig(ctx, CollectorConfig{
		DeviceID:    device.ID,
		CollectorID: "docker",
		Provider:    "docker",
		Enabled:     true,
		Config:      map[string]string{"socketPath": "/run/docker.sock"},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if config.Revision != 1 {
		t.Fatalf("expected first collector revision, got %d", config.Revision)
	}
	if _, err := s.PutCollectorConfig(ctx, CollectorConfig{
		DeviceID:    device.ID,
		CollectorID: "docker",
		Provider:    "docker",
		Config:      map[string]string{"apiToken": "must-not-be-stored"},
	}, config.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("secret-like collector config was accepted: %v", err)
	}
	if _, err := s.PutCollectorConfig(ctx, CollectorConfig{
		DeviceID:    device.ID,
		CollectorID: "docker",
		Provider:    "docker",
		Config:      map[string]string{"socketPath": "/run/docker.sock"},
	}, config.Revision+1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale collector revision was accepted: %v", err)
	}
}
