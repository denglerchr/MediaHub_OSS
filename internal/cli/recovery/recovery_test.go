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

func setupRecoveryTest(t *testing.T) (*sqlite.SQLiteRepository, *localstorage.LocalStorage, *RecoveryService) {
	t.Helper()

	tempDir := t.TempDir()
	storageRoot := filepath.Join(tempDir, "storage")
	if err := os.MkdirAll(storageRoot, 0755); err != nil {
		t.Fatalf("failed to create storage root: %v", err)
	}

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	localStorage := &localstorage.LocalStorage{RootPath: storageRoot}
	service := &RecoveryService{
		repo:    r,
		storage: localStorage,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		dryRun:  false,
	}

	return r, localStorage, service
}

func TestEntryStatusCorrection_ZeroStatsScan(t *testing.T) {
	ctx := context.Background()
	r, localStorage, service := setupRecoveryTest(t)

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
	r, _, service := setupRecoveryTest(t)

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

func TestIntegrityCheck_MissingFileExcludesPreviewSize(t *testing.T) {
	ctx := context.Background()
	r, localStorage, service := setupRecoveryTest(t)

	dbModel := repo.Database{
		Name:        "missing_file_db",
		ContentType: "image",
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Create an entry that is Ready, with a preview size recorded
	entry, err := r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName:    "orphan_preview.jpg",
		MimeType:    "image/jpeg",
		Size:        100,
		PreviewSize: 50,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Write ONLY the preview file to storage (no main file!)
	_, err = localStorage.WritePreview(ctx, createdDB.ID.String(), entry.ID, bytes.NewReader([]byte("dummy preview 50 bytes long 123456789012345678901234")))
	if err != nil {
		t.Fatalf("failed to write preview file: %v", err)
	}

	if err := service.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck failed: %v", err)
	}

	// The missing file entry should be deleted from DB and its preview removed
	finalStats, err := r.GetDatabaseStats(ctx, createdDB.ID)
	if err != nil {
		t.Fatalf("failed to fetch final stats: %v", err)
	}
	if finalStats.TotalDiskSpaceBytes != 0 {
		t.Errorf("expected TotalDiskSpaceBytes == 0, got %d", finalStats.TotalDiskSpaceBytes)
	}
	if finalStats.EntryCount != 0 {
		t.Errorf("expected EntryCount == 0, got %d", finalStats.EntryCount)
	}
}

func TestEntryStatusCorrection_ZeroSizeReconciliation(t *testing.T) {
	ctx := context.Background()
	r, localStorage, service := setupRecoveryTest(t)

	dbModel := repo.Database{
		Name:        "reconcile_db",
		ContentType: "file",
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Entry stuck in processing with Size = 0
	entry, err := r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName: "reconcile.txt",
		MimeType: "text/plain",
		Size:     0,
		Status:   repo.EntryStatusProcessing,
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Write 14 bytes to storage
	_, err = localStorage.Write(ctx, createdDB.ID.String(), entry.ID, bytes.NewReader([]byte("fourteen bytes")))
	if err != nil {
		t.Fatalf("failed to write storage: %v", err)
	}

	if err := service.EntryStatusCorrection(ctx); err != nil {
		t.Fatalf("EntryStatusCorrection failed: %v", err)
	}

	updated, err := r.GetEntry(ctx, createdDB.ID, entry.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated entry: %v", err)
	}
	if updated.Status != repo.EntryStatusReady {
		t.Errorf("expected status ready, got %v", updated.Status)
	}
	if updated.Size != 14 {
		t.Errorf("expected reconciled Size == 14, got %d", updated.Size)
	}

	// Database TotalDiskSpaceBytes should also reflect 14 bytes
	stats, err := r.GetDatabaseStats(ctx, createdDB.ID)
	if err != nil {
		t.Fatalf("failed to fetch stats: %v", err)
	}
	if stats.TotalDiskSpaceBytes != 14 {
		t.Errorf("expected TotalDiskSpaceBytes == 14, got %d", stats.TotalDiskSpaceBytes)
	}
}

func TestEntryStatusCorrection_RequeueNeedingConversionOrMetadata(t *testing.T) {
	ctx := context.Background()
	r, localStorage, service := setupRecoveryTest(t)

	// Image database
	dbModel := repo.Database{
		Name:        "image_db",
		ContentType: "image",
	}
	createdDB, err := r.CreateDatabase(ctx, dbModel)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Processing entry without extracted media metadata (width: 0, height: 0)
	entry, err := r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName:    "image.jpg",
		MimeType:    "image/jpeg",
		Size:        0,
		Status:      repo.EntryStatusProcessing,
		MediaFields: map[string]any{"width": 0, "height": 0},
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	_, err = localStorage.Write(ctx, createdDB.ID.String(), entry.ID, bytes.NewReader([]byte("raw image file bytes")))
	if err != nil {
		t.Fatalf("failed to write storage: %v", err)
	}

	if err := service.EntryStatusCorrection(ctx); err != nil {
		t.Fatalf("EntryStatusCorrection failed: %v", err)
	}

	updated, err := r.GetEntry(ctx, createdDB.ID, entry.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated entry: %v", err)
	}
	if updated.Status != repo.EntryStatusQueued {
		t.Errorf("expected status queued for entry needing metadata extraction, got %v", updated.Status)
	}
	if updated.Size != 20 {
		t.Errorf("expected reconciled Size == 20, got %d", updated.Size)
	}
}

