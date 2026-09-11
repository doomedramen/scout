package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"scout.local/scout/internal/jobs"
	"scout.local/scout/internal/store"
)

func TestMemoryQueueFencesStaleWorkerReports(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	site, _ := s.CreateSite(ctx, store.Site{Name: "lab"})
	device, _ := s.CreateDevice(ctx, store.Device{DisplayName: "target", SiteID: site.ID})
	q := jobs.Queue{Store: s}
	queued, _ := q.Enqueue(ctx, store.Job{Kind: "enrollment", DeviceID: device.ID, Deadline: ptr(time.Now().Add(time.Minute))})
	claimed, err := q.Claim(ctx, "worker-a")
	if err != nil || claimed.ID != queued.ID {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	if err := s.ReportJob(ctx, claimed.ID, "worker-b", claimed.Epoch, "enrolled", nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale report: %v", err)
	}
	if err := q.Progress(ctx, claimed, "enrolled", map[string]string{"confirmed": "true"}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryQueueHonorsGlobalDiscoveryPause(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	site, _ := s.CreateSite(ctx, store.Site{Name: "lab"})
	device, _ := s.CreateDevice(ctx, store.Device{DisplayName: "target", SiteID: site.ID})
	if _, err := s.CreateJob(ctx, store.Job{Kind: "discovery", DeviceID: device.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspace(ctx, func(state *store.WorkspaceState) error { state.DiscoveryPaused = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := (jobs.Queue{Store: s}).Claim(ctx, "paused-worker"); !errors.Is(err, store.ErrBackpressure) {
		t.Fatalf("paused discovery was claimable: %v", err)
	}
}

func TestMemoryQueueStopsRetryingAfterBound(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	site, _ := s.CreateSite(ctx, store.Site{Name: "lab"})
	device, _ := s.CreateDevice(ctx, store.Device{DisplayName: "target", SiteID: site.ID})
	queue := jobs.Queue{Store: s}
	if _, err := queue.Enqueue(ctx, store.Job{Kind: "enrollment", DeviceID: device.ID}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 8; attempt++ {
		claimed, err := queue.Claim(ctx, "bounded-worker")
		if err != nil {
			t.Fatalf("claim attempt %d: %v", attempt, err)
		}
		if err := queue.Progress(ctx, claimed, "retry", map[string]string{"code": "temporary"}); err != nil {
			t.Fatalf("retry attempt %d: %v", attempt, err)
		}
		now = now.Add(10 * time.Minute)
	}
	items, err := s.ListJobs(ctx, "enrollment", "failed", device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Result["code"] != "retry_limit" {
		t.Fatalf("retry limit not enforced: %+v", items)
	}
}

func TestMemoryQueueRejectsReportsAfterLeaseExpiry(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	site, err := repository.CreateSite(ctx, store.Site{Name: "lease-expiry"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repository.CreateDevice(ctx, store.Device{DisplayName: "device-1", SiteID: site.ID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := repository.CreateJob(ctx, store.Job{Kind: "enrollment", DeviceID: device.ID, State: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimJob(ctx, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed unexpected job: got %s want %s", claimed.ID, job.ID)
	}
	repository.SetClock(func() time.Time { return claimed.LeaseExpiry.Add(time.Second) })
	if err := repository.ReportJob(ctx, job.ID, "worker-1", claimed.Epoch, "failed", map[string]string{"code": "late"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expired lease was accepted: %v", err)
	}
}

func ptr(value time.Time) *time.Time { return &value }
