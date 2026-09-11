package topology

import (
	"context"
	"testing"

	"scout.local/scout/internal/store"
)

func TestServiceAssociationRequiresOneExactAddress(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	site, _ := repository.CreateSite(ctx, store.Site{Name: "services"})
	device, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "host", SiteID: site.ID, Addresses: []string{"192.0.2.20"}})
	items, err := AssociateServices(ctx, repository, []store.ServiceEntity{{ID: "container", Provider: "docker", Kind: "container", Name: "web", Labels: map[string]string{"address": "192.0.2.20", "siteId": site.ID}}})
	if err != nil || len(items) != 1 || items[0].DeviceID != device.ID {
		t.Fatalf("exact association failed: %+v %v", items, err)
	}
	second, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "duplicate", SiteID: site.ID, Addresses: []string{"192.0.2.20"}})
	items, err = AssociateServices(ctx, repository, []store.ServiceEntity{{ID: "ambiguous", Provider: "docker", Kind: "container", Name: "ambiguous", Labels: map[string]string{"address": "192.0.2.20", "siteId": site.ID}}})
	if err != nil || len(items) != 1 || items[0].DeviceID != "" {
		t.Fatalf("ambiguous association was asserted: %+v %v (second=%s)", items, err, second.ID)
	}
}
