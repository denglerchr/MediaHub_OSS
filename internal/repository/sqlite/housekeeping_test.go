package sqlite_test

import (
	"context"
	"testing"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	"mediahub_oss/internal/repository/sqlite"

	"github.com/pressly/goose/v3"
)

// Regression test: HouseKeepingRequired must select exactly the columns that
// scanDatabaseRow consumes. A mismatch (queued_count was missing) made every
// scheduled housekeeping run fail with "expected 13 destination arguments in
// Scan, not 12", silently disabling max-age/disk-space retention.
func TestHouseKeepingRequired_ScanSuccess(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Create database with hk_interval enabled and last run in past
	dbModel := repo.Database{
		Name:        "test_hk_db",
		ContentType: "image",
		Housekeeping: repo.DatabaseHK{
			Interval:  1 * time.Second,
			LastHkRun: time.Now().Add(-10 * time.Minute),
		},
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	// Test HouseKeepingRequired executes and scans without column mismatch error
	reqDBs, err := r.HouseKeepingRequired(ctx)
	if err != nil {
		t.Fatalf("HouseKeepingRequired failed: %v", err)
	}
	if len(reqDBs) != 1 {
		t.Fatalf("expected 1 database requiring housekeeping, got %d", len(reqDBs))
	}
	if reqDBs[0].ID != createdDB.ID {
		t.Fatalf("expected db ID %s, got %s", createdDB.ID, reqDBs[0].ID)
	}
}
