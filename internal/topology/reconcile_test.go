package topology

import (
	"context"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestReconcilerKeepsAddressOnlyEvidenceConservativeAndExpiresIt(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	repository := store.NewMemory()
	left, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "left"})
	right, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "right"})
	reconciler := &Reconciler{Store: repository, Now: func() time.Time { return now }}
	items, err := reconciler.Apply(ctx, []Evidence{{FromEntity: left.ID, ToEntity: right.ID, Type: "neighbor", Source: "address-only", Confidence: 0.9, ObservedAt: now, ExpiresAt: now.Add(time.Minute)}})
	if err != nil || len(items) != 1 {
		t.Fatalf("apply evidence: %+v %v", items, err)
	}
	if items[0].Confidence != 0.5 {
		t.Fatalf("address-only confidence was not capped: %v", items[0].Confidence)
	}
	now = now.Add(2 * time.Minute)
	removed, err := reconciler.Expire(ctx)
	if err != nil || removed != 1 {
		t.Fatalf("expiry: removed=%d err=%v", removed, err)
	}
}

func TestReconcilerDoesNotProjectRevokedInventory(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	left, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "revoked", Lifecycle: "decommissioned"})
	right, _ := repository.CreateDevice(ctx, store.Device{DisplayName: "other"})
	items, err := (&Reconciler{Store: repository}).Apply(ctx, []Evidence{{FromEntity: left.ID, ToEntity: right.ID, Type: "neighbor", Source: "agent", Confidence: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("revoked device generated relationship: %+v", items)
	}
}
