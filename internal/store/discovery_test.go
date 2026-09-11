package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCandidatesDeduplicateAndPreserveExclusions(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	site, err := s.CreateSite(ctx, Site{Name: "discovery"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.CreateScope(ctx, Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, Exclusions: []string{"192.0.2.9"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.UpsertCandidate(ctx, Candidate{ScopeID: scope.ID, Address: "192.0.2.8", Source: "vantage-a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.UpsertCandidate(ctx, Candidate{ScopeID: scope.ID, Address: "192.0.2.8", Source: "vantage-b", LastSeen: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate sighting created two candidates: %s %s", first.ID, second.ID)
	}
	excluded, err := s.UpsertCandidate(ctx, Candidate{ScopeID: scope.ID, Address: "192.0.2.9", Source: "vantage-a"})
	if err != nil {
		t.Fatal(err)
	}
	if !excluded.Excluded || excluded.State != "excluded" {
		t.Fatalf("scope exclusion was not persistent: %+v", excluded)
	}
	items, err := s.ListCandidates(ctx, scope.ID, "")
	if err != nil || len(items) != 2 {
		t.Fatalf("candidate list: %d %v", len(items), err)
	}
}

func TestWorkerClaimIsSiteScopedAndRecoveryAware(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	site, _ := s.CreateSite(ctx, Site{Name: "worker-site"})
	other, _ := s.CreateSite(ctx, Site{Name: "other-site"})
	device, _ := s.CreateDevice(ctx, Device{DisplayName: "target", SiteID: site.ID})
	if _, err := s.CreateJob(ctx, Job{Kind: "enrollment", DeviceID: device.ID, ScopeID: "scope", ScopeRevision: 1}); err != nil {
		t.Fatal(err)
	}
	worker := WorkerIdentity{ID: "worker-1", Name: "local", Kind: "enroller", AuthTokenHash: HashToken("worker-token"), SiteIDs: []string{other.ID}}
	if err := s.CreateWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimWorkerJob(ctx, worker); !errors.Is(err, ErrNotFound) {
		t.Fatalf("worker claimed a job outside its site: %v", err)
	}
	if _, err := s.SetWorkspace(ctx, func(state *WorkspaceState) error { state.EnrollmentPaused = true; return nil }); err != nil {
		t.Fatal(err)
	}
	worker.SiteIDs = []string{site.ID}
	if _, err := s.ClaimWorkerJob(ctx, worker); !errors.Is(err, ErrConflict) {
		t.Fatalf("paused enrollment was not fenced: %v", err)
	}
}
