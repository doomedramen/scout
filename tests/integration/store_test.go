package integration

import (
	"context"
	"testing"
	"time"

	"scout.local/scout/internal/store"
)

func TestPostgresWorkspaceStateSurvivesRepositoryRestart(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	first := store.NewSQL(db)
	owner := store.Owner{ID: store.NewID(), PasswordHash: "argon2id$integration"}
	if err := first.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	second := store.NewSQL(db)
	got, err := second.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != owner.ID {
		t.Fatalf("owner changed across repository restart: got %s want %s", got.ID, owner.ID)
	}
}
