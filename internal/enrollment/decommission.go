package enrollment

import (
	"context"
	"strings"

	"scout.local/scout/internal/store"
)

// Decommissioner is the narrow boundary used by control-plane workflows to
// revoke a device without exposing repository mutation details to handlers.
// Store.DecommissionDevice performs the identity, exclusion, and job changes
// in one transaction.
type Decommissioner struct {
	Store *store.Store
}

func (d *Decommissioner) Decommission(ctx context.Context, deviceID, reason string) (store.Device, error) {
	if d == nil || d.Store == nil || strings.TrimSpace(deviceID) == "" || strings.TrimSpace(reason) == "" {
		return store.Device{}, store.ErrInvalid
	}
	return d.Store.DecommissionDevice(ctx, deviceID, strings.TrimSpace(reason))
}

func (d *Decommissioner) Reenable(ctx context.Context, deviceID string, expectedRevision int64) (store.Device, error) {
	if d == nil || d.Store == nil || strings.TrimSpace(deviceID) == "" {
		return store.Device{}, store.ErrInvalid
	}
	return d.Store.ReenableDevice(ctx, deviceID, expectedRevision)
}
