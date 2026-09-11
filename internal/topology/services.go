package topology

import (
	"context"
	"strings"
	"time"

	"scout.local/scout/internal/store"
)

// AssociateServices links a provider entity only when one inventory device
// has an exact site-local address match. Ambiguous or absent matches remain
// unassociated for owner review; provider/cluster identity is never inferred
// from a display name.
func AssociateServices(ctx context.Context, repository *store.Store, entities []store.ServiceEntity) ([]store.ServiceEntity, error) {
	if repository == nil {
		return nil, store.ErrInvalid
	}
	devices, err := repository.ListDevices(ctx, store.DeviceFilter{})
	if err != nil {
		return nil, err
	}
	result := make([]store.ServiceEntity, 0, len(entities))
	for _, entity := range entities {
		matches := map[string]bool{}
		for _, device := range devices {
			if device.SiteID != "" && entity.Labels["siteId"] != "" && device.SiteID != entity.Labels["siteId"] {
				continue
			}
			for _, candidate := range []string{entity.Labels["address"], entity.Labels["ip"], entity.Labels["host"]} {
				if candidate == "" {
					continue
				}
				for _, address := range device.Addresses {
					if strings.TrimSpace(strings.Split(address, "/")[0]) == strings.TrimSpace(candidate) {
						matches[device.ID] = true
					}
				}
			}
		}
		if len(matches) == 1 {
			for deviceID := range matches {
				entity.DeviceID = deviceID
			}
		}
		if entity.ObservedAt.IsZero() {
			entity.ObservedAt = time.Now().UTC()
		}
		if err := repository.PutServiceEntity(ctx, entity); err != nil {
			return nil, err
		}
		result = append(result, entity)
	}
	return result, nil
}
