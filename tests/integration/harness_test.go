package integration

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"scout.local/scout/internal/store"
)

func openDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SCOUT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SCOUT_TEST_DATABASE_URL is not set; use scripts/test-integration.sh")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestHarnessRunsMigrationsAndCanRestart(t *testing.T) {
	db := openDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM scout_schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 1 {
		t.Fatalf("expected migration version, got %d", version)
	}
}
