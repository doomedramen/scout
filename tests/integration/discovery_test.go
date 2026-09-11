package integration

import (
	"context"
	"testing"

	"scout.local/scout/internal/discovery"
	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/store"
)

func TestDiscoveryIntegrationReconcilesVantagesAndNeverQueuesExcludedTargets(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	site, err := repository.CreateSite(ctx, store.Site{Name: "discovery-integration"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := repository.CreateScope(ctx, store.Scope{
		SiteID:         site.ID,
		Ranges:         []string{"192.0.2.0/29"},
		Exclusions:     []string{"192.0.2.3"},
		AllowedMethods: []string{"tcp"},
		Ports:          []int{22},
		Enabled:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := &discovery.Service{Store: repository, Policy: &policy.Engine{Store: repository}}
	items, err := service.ReconcileSightings(ctx, scope.ID, []discovery.Sight{
		{Address: "192.0.2.2", Source: "vantage-a", Reachable: true},
		{Address: "192.0.2.2", Source: "vantage-b", Reachable: true},
		{Address: "192.0.2.3", Source: "vantage-a", Reachable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected one duplicate and one excluded candidate, got %d", len(items))
	}
	jobs, err := repository.ListJobs(ctx, "enrollment", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("excluded target created enrollment work: %+v", jobs)
	}
	candidates, err := repository.ListCandidates(ctx, scope.ID, "excluded")
	if err != nil || len(candidates) != 1 || !candidates[0].Excluded {
		t.Fatalf("excluded candidate was not retained: %+v %v", candidates, err)
	}
}
