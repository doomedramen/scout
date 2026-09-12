package discovery

import (
	"context"
	"testing"

	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

func TestReconcileSightingsProjectsCandidatesWithoutAddressOnlyEnrollment(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	site, _ := repository.CreateSite(ctx, store.Site{Name: "sightings"})
	scope, err := repository.CreateScope(ctx, store.Scope{SiteID: site.ID, Ranges: []string{"192.0.2.0/24"}, Exclusions: []string{"192.0.2.9"}, AllowedMethods: []string{"tcp"}, Ports: []int{22}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: repository, Policy: &policy.Engine{Store: repository}}
	items, err := service.ReconcileSightings(ctx, scope.ID, []Sight{{Address: "192.0.2.8", Source: "vantage-a", Reachable: true}, {Address: "192.0.2.8", Source: "vantage-b", Reachable: true}, {Address: "192.0.2.9", Source: "vantage-a", Reachable: true}, {Address: "192.0.2.10", Source: "vantage-a", Reachable: false}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("unexpected candidate count: %d", len(items))
	}
	jobs, err := repository.ListJobs(ctx, "enrollment", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("sighting projection created an enrollment job before explicit access checks: %d", len(jobs))
	}
	devices, err := repository.ListDevices(ctx, store.DeviceFilter{SiteID: site.ID})
	if err != nil || len(devices) != 0 {
		t.Fatalf("sighting projection created address-only devices: %+v err=%v", devices, err)
	}
	requests, err := repository.ListAccessRequests(ctx, "open", "no_connectivity")
	if err != nil || len(requests) != 1 {
		t.Fatalf("expected one connectivity request: %d %v", len(requests), err)
	}
}
