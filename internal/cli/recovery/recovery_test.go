package recovery

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	_ "mediahub_oss/internal/repository/migrations/sqlite"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/storage/localstorage"

	"github.com/pressly/goose/v3"
)

func TestEntryStatusCorrection_ZeroStatsScan(t *testing.T) {
	ctx := context.Background()

	// Create temp dir for storage
	tempDir := t.TempDir()
	storageRoot := filepath.Join(tempDir, "storage")
	_ = os.MkdirAll(storageRoot, 0755)

	// In-memory sqlite repo
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

	dbModel := repo.Database{
		Name:        "test_db",
		ContentType: "file",
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Create an entry in 'processing' status
	createdEntry, err := r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName: "test.txt",
		MimeType: "text/plain",
		Size:     12,
		Status:   repo.EntryStatusProcessing,
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Create the physical file in storage so it gets marked 'ready'
	localStorage := &localstorage.LocalStorage{RootPath: storageRoot}
	_, err = localStorage.Write(ctx, createdDB.ID.String(), createdEntry.ID, bytes.NewReader([]byte("hello world!")))
	if err != nil {
		t.Fatalf("failed to write storage file: %v", err)
	}

	// Deliberately set stats.EntryCount to 0 in database metadata (simulating corrupted / zero stats)
	createdDB.Stats.EntryCount = 0
	err = r.UpdateDatabaseStats(ctx, createdDB.ID, createdDB.Stats)
	if err != nil {
		t.Fatalf("failed to update db stats: %v", err)
	}

	// Verify stats.EntryCount is indeed 0
	stats, err := r.GetDatabaseStats(ctx, createdDB.ID)
	if err != nil || stats.EntryCount != 0 {
		t.Fatalf("expected stats.EntryCount == 0, got %d, err: %v", stats.EntryCount, err)
	}

	service := &RecoveryService{
		repo:    r,
		storage: localStorage,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		dryRun:  false,
	}

	// Run EntryStatusCorrection - should scan despite stats.EntryCount == 0 and correct status to 'ready'
	if err := service.EntryStatusCorrection(ctx); err != nil {
		t.Fatalf("EntryStatusCorrection failed: %v", err)
	}

	// Verify entry status is now 'ready'
	updatedEntry, err := r.GetEntry(ctx, createdDB.ID, createdEntry.ID)
	if err != nil {
		t.Fatalf("failed to fetch entry: %v", err)
	}
	if updatedEntry.Status != repo.EntryStatusReady {
		t.Errorf("expected entry status 'ready', got %q", updatedEntry.Status)
	}
}

func TestIntegrityCheck_QueuedCountReconciliation(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	storageRoot := filepath.Join(tempDir, "storage")
	_ = os.MkdirAll(storageRoot, 0755)

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

	dbModel := repo.Database{
		Name:        "queued_test_db",
		ContentType: "file",
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Create 1 entry with Queued status
	_, err = r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName: "queued1.txt",
		MimeType: "text/plain",
		Size:     10,
		Status:   repo.EntryStatusQueued,
	})
	if err != nil {
		t.Fatalf("failed to create queued entry: %v", err)
	}

	// Verify initial QueuedCount is 1
	stats, err := r.GetDatabaseStats(ctx, createdDB.ID)
	if err != nil || stats.QueuedCount != 1 {
		t.Fatalf("expected QueuedCount == 1, got %d, err: %v", stats.QueuedCount, err)
	}

	// Corrupt QueuedCount to 99
	createdDB.Stats.QueuedCount = 99
	if err := r.UpdateDatabaseStats(ctx, createdDB.ID, createdDB.Stats); err != nil {
		t.Fatalf("failed to corrupt queued_count: %v", err)
	}

	stats, err = r.GetDatabaseStats(ctx, createdDB.ID)
	if err != nil || stats.QueuedCount != 99 {
		t.Fatalf("expected corrupted QueuedCount == 99, got %d, err: %v", stats.QueuedCount, err)
	}

	localStorage := &localstorage.LocalStorage{RootPath: storageRoot}
	service := &RecoveryService{
		repo:    r,
		storage: localStorage,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		dryRun:  false,
	}

	// Run IntegrityCheck
	if err := service.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck failed: %v", err)
	}

	// Verify QueuedCount has been reconciled to 1
	reconciledStats, err := r.GetDatabaseStats(ctx, createdDB.ID)
	if err != nil {
		t.Fatalf("failed to fetch stats: %v", err)
	}
	if reconciledStats.QueuedCount != 1 {
		t.Errorf("expected reconciled QueuedCount == 1, got %d", reconciledStats.QueuedCount)
	}
}

