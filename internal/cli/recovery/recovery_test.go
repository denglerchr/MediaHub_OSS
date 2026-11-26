package recovery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
	"mediahub_oss/internal/storage/localstorage"
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

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	localStorage := &localstorage.LocalStorage{RootPath: storageRoot}
	service := NewRecoveryService(r, localStorage, slog.New(slog.NewTextHandler(io.Discard, nil)), false)

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
	_, err = localStorage.Write(ctx, createdDB.ID, createdEntry.ID, bytes.NewReader([]byte("hello world!")))
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
	_, err = localStorage.WritePreview(ctx, createdDB.ID, entry.ID, bytes.NewReader([]byte("dummy preview 50 bytes long 123456789012345678901234")))
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
	_, err = localStorage.Write(ctx, createdDB.ID, entry.ID, bytes.NewReader([]byte("fourteen bytes")))
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

	_, err = localStorage.Write(ctx, createdDB.ID, entry.ID, bytes.NewReader([]byte("raw image file bytes")))
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

func TestEntryStatusCorrection_NormalizesMimeType(t *testing.T) {
	ctx := context.Background()
	r, localStorage, service := setupRecoveryTest(t)

	createdDB, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "mime_norm_db",
		ContentType: "image",
		Config: repo.DatabaseConfig{
			AutoConversion: "image/jpeg",
			CreatePreview:  false,
		},
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Entry has alias MIME "image/jpg" matching AutoConversion "image/jpeg" and valid metadata.
	entry, err := r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName:    "photo.jpg",
		MimeType:    "image/jpg",
		Size:        0,
		Status:      repo.EntryStatusProcessing,
		MediaFields: map[string]any{"width": 640, "height": 480},
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	if _, err := localStorage.Write(ctx, createdDB.ID, entry.ID, bytes.NewReader([]byte("jpeg bytes"))); err != nil {
		t.Fatalf("failed to write main file: %v", err)
	}

	if err := service.EntryStatusCorrection(ctx); err != nil {
		t.Fatalf("EntryStatusCorrection failed: %v", err)
	}

	updated, err := r.GetEntry(ctx, createdDB.ID, entry.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated entry: %v", err)
	}
	if updated.Status != repo.EntryStatusReady {
		t.Errorf("expected normalized MIME image/jpg == image/jpeg to mark entry ready, got %v", updated.Status)
	}
}

func TestEntryStatusCorrection_PreviewChecks(t *testing.T) {
	ctx := context.Background()

	t.Run("missing preview requeues entry and resets PreviewSize", func(t *testing.T) {
		r, localStorage, service := setupRecoveryTest(t)

		createdDB, err := r.CreateDatabase(ctx, repo.Database{
			Name:        "missing_preview_db",
			ContentType: "image",
			Config: repo.DatabaseConfig{
				CreatePreview: true,
			},
		})
		if err != nil {
			t.Fatalf("failed to create database: %v", err)
		}

		entry, err := r.CreateEntry(ctx, createdDB, repo.Entry{
			FileName:    "nopreview.jpg",
			MimeType:    "image/jpeg",
			Size:        10,
			PreviewSize: 999,
			Status:      repo.EntryStatusProcessing,
			MediaFields: map[string]any{"width": 800, "height": 600},
		})
		if err != nil {
			t.Fatalf("failed to create entry: %v", err)
		}

		if _, err := localStorage.Write(ctx, createdDB.ID, entry.ID, bytes.NewReader([]byte("0123456789"))); err != nil {
			t.Fatalf("failed to write main file: %v", err)
		}

		if err := service.EntryStatusCorrection(ctx); err != nil {
			t.Fatalf("EntryStatusCorrection failed: %v", err)
		}

		updated, err := r.GetEntry(ctx, createdDB.ID, entry.ID)
		if err != nil {
			t.Fatalf("failed to fetch updated entry: %v", err)
		}
		if updated.Status != repo.EntryStatusQueued {
			t.Errorf("expected status queued when preview is missing, got %v", updated.Status)
		}
		if updated.PreviewSize != 0 {
			t.Errorf("expected PreviewSize reset to 0 when preview missing, got %d", updated.PreviewSize)
		}
	})

	t.Run("existing preview marks ready and reconciles PreviewSize", func(t *testing.T) {
		r, localStorage, service := setupRecoveryTest(t)

		createdDB, err := r.CreateDatabase(ctx, repo.Database{
			Name:        "has_preview_db",
			ContentType: "image",
			Config: repo.DatabaseConfig{
				CreatePreview: true,
			},
		})
		if err != nil {
			t.Fatalf("failed to create database: %v", err)
		}

		entry, err := r.CreateEntry(ctx, createdDB, repo.Entry{
			FileName:    "haspreview.jpg",
			MimeType:    "image/jpeg",
			Size:        10,
			PreviewSize: 0,
			Status:      repo.EntryStatusProcessing,
			MediaFields: map[string]any{"width": 800, "height": 600},
		})
		if err != nil {
			t.Fatalf("failed to create entry: %v", err)
		}

		if _, err := localStorage.Write(ctx, createdDB.ID, entry.ID, bytes.NewReader([]byte("0123456789"))); err != nil {
			t.Fatalf("failed to write main file: %v", err)
		}
		if _, err := localStorage.WritePreview(ctx, createdDB.ID, entry.ID, bytes.NewReader(bytes.Repeat([]byte("p"), 1234))); err != nil {
			t.Fatalf("failed to write preview file: %v", err)
		}

		if err := service.EntryStatusCorrection(ctx); err != nil {
			t.Fatalf("EntryStatusCorrection failed: %v", err)
		}

		updated, err := r.GetEntry(ctx, createdDB.ID, entry.ID)
		if err != nil {
			t.Fatalf("failed to fetch updated entry: %v", err)
		}
		if updated.Status != repo.EntryStatusReady {
			t.Errorf("expected status ready when preview exists, got %v", updated.Status)
		}
		if updated.PreviewSize != 1234 {
			t.Errorf("expected PreviewSize == 1234, got %d", updated.PreviewSize)
		}
	})

	t.Run("unexpected StatPreview error skips status change", func(t *testing.T) {
		r, localStorage, _ := setupRecoveryTest(t)

		createdDB, err := r.CreateDatabase(ctx, repo.Database{
			Name:        "preview_err_db",
			ContentType: "image",
			Config: repo.DatabaseConfig{
				CreatePreview: true,
			},
		})
		if err != nil {
			t.Fatalf("failed to create database: %v", err)
		}

		entry, err := r.CreateEntry(ctx, createdDB, repo.Entry{
			FileName:    "errpreview.jpg",
			MimeType:    "image/jpeg",
			Size:        10,
			Status:      repo.EntryStatusProcessing,
			MediaFields: map[string]any{"width": 800, "height": 600},
		})
		if err != nil {
			t.Fatalf("failed to create entry: %v", err)
		}
		if _, err := localStorage.Write(ctx, createdDB.ID, entry.ID, bytes.NewReader([]byte("0123456789"))); err != nil {
			t.Fatalf("failed to write main file: %v", err)
		}

		mockStor := &mockRecoveryStorage{
			statFunc: localStorage.Stat,
			statPreviewFunc: func(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
				return storage.FileInfo{}, errors.New("storage timeout")
			},
		}
		svc := &RecoveryService{
			repo:    r,
			storage: mockStor,
			logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		if err := svc.EntryStatusCorrection(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		unchanged, err := r.GetEntry(ctx, createdDB.ID, entry.ID)
		if err != nil {
			t.Fatalf("failed to fetch entry: %v", err)
		}
		if unchanged.Status != repo.EntryStatusProcessing {
			t.Errorf("expected entry status to remain processing on unexpected StatPreview error, got %v", unchanged.Status)
		}
	})
}

type mockRecoveryRepo struct {
	repo.Repository
	databases                []repo.Database
	entries                  []repo.Entry
	stats                    repo.DatabaseStats
	deleteEntriesFunc        func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error)
	updateEntriesStatusFunc  func(ctx context.Context, dbID repo.ULID, ids []int64, status repo.EntryStatus) error
	updateDatabaseStatsFunc  func(ctx context.Context, dbID repo.ULID, stats repo.DatabaseStats) error
	countEntriesByStatusFunc func(ctx context.Context, dbID repo.ULID, status repo.EntryStatus) (int64, error)
	deleteBatchSizes         []int
	updateStatusBatchSizes   []int
	updateDatabaseStatsCalls int
}

func (m *mockRecoveryRepo) GetDatabases(ctx context.Context) ([]repo.Database, error) {
	return m.databases, nil
}

func (m *mockRecoveryRepo) GetDatabaseStats(ctx context.Context, dbID repo.ULID) (repo.DatabaseStats, error) {
	return m.stats, nil
}

func (m *mockRecoveryRepo) GetEntries(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
	if opts.Offset >= len(m.entries) {
		return nil, nil
	}
	end := len(m.entries)
	if opts.Limit > 0 && opts.Offset+opts.Limit < end {
		end = opts.Offset + opts.Limit
	}
	return m.entries[opts.Offset:end], nil
}

func (m *mockRecoveryRepo) DeleteEntries(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
	m.deleteBatchSizes = append(m.deleteBatchSizes, len(ids))
	if m.deleteEntriesFunc != nil {
		return m.deleteEntriesFunc(ctx, dbID, ids)
	}
	metas := make([]repo.DeletedEntryMeta, len(ids))
	for i, id := range ids {
		metas[i] = repo.DeletedEntryMeta{ID: id}
	}
	return metas, nil
}

func (m *mockRecoveryRepo) UpdateEntriesStatus(ctx context.Context, dbID repo.ULID, ids []int64, status repo.EntryStatus) error {
	m.updateStatusBatchSizes = append(m.updateStatusBatchSizes, len(ids))
	if m.updateEntriesStatusFunc != nil {
		return m.updateEntriesStatusFunc(ctx, dbID, ids, status)
	}
	return nil
}

func (m *mockRecoveryRepo) CountEntriesByStatus(ctx context.Context, dbID repo.ULID, status repo.EntryStatus) (int64, error) {
	if m.countEntriesByStatusFunc != nil {
		return m.countEntriesByStatusFunc(ctx, dbID, status)
	}
	return 0, nil
}

func (m *mockRecoveryRepo) UpdateDatabaseStats(ctx context.Context, dbID repo.ULID, stats repo.DatabaseStats) error {
	m.updateDatabaseStatsCalls++
	if m.updateDatabaseStatsFunc != nil {
		return m.updateDatabaseStatsFunc(ctx, dbID, stats)
	}
	return nil
}

type mockRecoveryStorage struct {
	storage.StorageProvider
	statFunc                   func(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error)
	statPreviewFunc            func(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error)
	walkFunc                   func(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error
	walkPreviewFunc            func(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error
	deleteMultipleFunc         func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error)
	deleteMultiplePreviewsFunc func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error)
	deleteMultipleSizes        []int
	deletePreviewBatchSizes    []int
}

func (m *mockRecoveryStorage) Stat(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
	if m.statFunc != nil {
		return m.statFunc(ctx, dbID, id)
	}
	return storage.FileInfo{}, customerrors.ErrNotFound
}

func (m *mockRecoveryStorage) StatPreview(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
	if m.statPreviewFunc != nil {
		return m.statPreviewFunc(ctx, dbID, id)
	}
	return storage.FileInfo{}, customerrors.ErrNotFound
}

func (m *mockRecoveryStorage) Walk(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error {
	if m.walkFunc != nil {
		return m.walkFunc(ctx, dbID, walkFn)
	}
	return nil
}

func (m *mockRecoveryStorage) WalkPreview(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error {
	if m.walkPreviewFunc != nil {
		return m.walkPreviewFunc(ctx, dbID, walkFn)
	}
	return nil
}

func (m *mockRecoveryStorage) DeleteMultiple(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	m.deleteMultipleSizes = append(m.deleteMultipleSizes, len(ids))
	if m.deleteMultipleFunc != nil {
		return m.deleteMultipleFunc(ctx, dbID, ids)
	}
	return storage.BulkDeleteResult{Success: ids}, nil
}

func (m *mockRecoveryStorage) DeleteMultiplePreviews(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	m.deletePreviewBatchSizes = append(m.deletePreviewBatchSizes, len(ids))
	if m.deleteMultiplePreviewsFunc != nil {
		return m.deleteMultiplePreviewsFunc(ctx, dbID, ids)
	}
	return storage.BulkDeleteResult{Success: ids}, nil
}

func TestRecovery_BatchedDeletionsOver500(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	const totalIDs = 1205
	expectedBatches := []int{500, 500, 205}

	t.Run("IntegrityCheck splits missing, orphan main, and orphan preview deletions into 500-item batches", func(t *testing.T) {
		entries := make([]repo.Entry, totalIDs)
		for i := range entries {
			entries[i] = repo.Entry{ID: int64(i + 1), Status: repo.EntryStatusReady}
		}

		mockRepo := &mockRecoveryRepo{
			databases: []repo.Database{{ID: dbID, Name: "batch_ic_db", Stats: repo.DatabaseStats{EntryCount: totalIDs}}},
			entries:   entries,
		}
		mockStor := &mockRecoveryStorage{
			statFunc: func(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
				return storage.FileInfo{}, customerrors.ErrNotFound
			},
			walkFunc: func(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error {
				for i := 1; i <= totalIDs; i++ {
					if err := walkFn(int64(100000+i), storage.FileInfo{Size: 10}); err != nil {
						return err
					}
				}
				return nil
			},
			walkPreviewFunc: func(ctx context.Context, dbID ulid.ULID, walkFn func(id int64, info storage.FileInfo) error) error {
				for i := 1; i <= totalIDs; i++ {
					if err := walkFn(int64(200000+i), storage.FileInfo{Size: 5}); err != nil {
						return err
					}
				}
				return nil
			},
		}

		svc := &RecoveryService{
			repo:    mockRepo,
			storage: mockStor,
			logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		if err := svc.IntegrityCheck(ctx); err != nil {
			t.Fatalf("IntegrityCheck failed: %v", err)
		}

		if !slices.Equal(mockRepo.deleteBatchSizes, expectedBatches) {
			t.Errorf("expected DeleteEntries batches %v, got %v", expectedBatches, mockRepo.deleteBatchSizes)
		}
		if !slices.Equal(mockStor.deleteMultipleSizes, expectedBatches) {
			t.Errorf("expected DeleteMultiple batches %v, got %v", expectedBatches, mockStor.deleteMultipleSizes)
		}
		// DeleteMultiplePreviews is called for missingFileIDs and for orphanPreviewIDs (2 * 3 batches).
		expectedPreviewBatches := append(append([]int(nil), expectedBatches...), expectedBatches...)
		if !slices.Equal(mockStor.deletePreviewBatchSizes, expectedPreviewBatches) {
			t.Errorf("expected DeleteMultiplePreviews batches %v, got %v", expectedPreviewBatches, mockStor.deletePreviewBatchSizes)
		}
		if mockRepo.updateDatabaseStatsCalls != 1 {
			t.Errorf("expected 1 UpdateDatabaseStats call, got %d", mockRepo.updateDatabaseStatsCalls)
		}
	})

	t.Run("EntryStatusCorrection splits zombie and stuck deleting batches of 500", func(t *testing.T) {
		entries := make([]repo.Entry, 0, totalIDs*2)
		for i := 1; i <= totalIDs; i++ {
			entries = append(entries, repo.Entry{ID: int64(i), Status: repo.EntryStatusProcessing})
		}
		for i := 1; i <= totalIDs; i++ {
			entries = append(entries, repo.Entry{ID: int64(totalIDs + i), Status: repo.EntryStatusDeleting})
		}

		mockRepo := &mockRecoveryRepo{
			databases: []repo.Database{{ID: dbID, Name: "batch_esc_db"}},
			entries:   entries,
			stats:     repo.DatabaseStats{EntryCount: totalIDs * 2},
		}
		mockStor := &mockRecoveryStorage{
			statFunc: func(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
				return storage.FileInfo{}, customerrors.ErrNotFound
			},
		}

		svc := &RecoveryService{
			repo:    mockRepo,
			storage: mockStor,
			logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		if err := svc.EntryStatusCorrection(ctx); err != nil {
			t.Fatalf("EntryStatusCorrection failed: %v", err)
		}

		// DeleteEntries is called for zombies (3 batches) AND inside DeleteMultipleSafe for stuck entries (3 batches).
		expectedRepoDeleteBatches := append(append([]int(nil), expectedBatches...), expectedBatches...)
		if !slices.Equal(mockRepo.deleteBatchSizes, expectedRepoDeleteBatches) {
			t.Errorf("expected DeleteEntries batches %v, got %v", expectedRepoDeleteBatches, mockRepo.deleteBatchSizes)
		}
		if !slices.Equal(mockRepo.updateStatusBatchSizes, expectedBatches) {
			t.Errorf("expected UpdateEntriesStatus batches %v, got %v", expectedBatches, mockRepo.updateStatusBatchSizes)
		}
		if !slices.Equal(mockStor.deleteMultipleSizes, expectedBatches) {
			t.Errorf("expected DeleteMultiple batches %v, got %v", expectedBatches, mockStor.deleteMultipleSizes)
		}
	})
}

func TestRecovery_DeleteEntriesErrorPropagationAndStatsSkip(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	dbErr := errors.New("db delete failure")

	t.Run("IntegrityCheck skips UpdateDatabaseStats and returns error when DeleteEntries fails", func(t *testing.T) {
		mockRepo := &mockRecoveryRepo{
			databases: []repo.Database{{ID: dbID, Name: "fail_ic_db", Stats: repo.DatabaseStats{EntryCount: 2}}},
			entries: []repo.Entry{
				{ID: 1, Status: repo.EntryStatusReady},
				{ID: 2, Status: repo.EntryStatusReady},
			},
			deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
				return nil, dbErr
			},
		}
		mockStor := &mockRecoveryStorage{
			statFunc: func(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
				return storage.FileInfo{}, customerrors.ErrNotFound
			},
		}

		svc := &RecoveryService{
			repo:    mockRepo,
			storage: mockStor,
			logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		err := svc.IntegrityCheck(ctx)
		if !errors.Is(err, dbErr) {
			t.Fatalf("expected IntegrityCheck to return wrapped dbErr, got %v", err)
		}
		if mockRepo.updateDatabaseStatsCalls != 0 {
			t.Errorf("expected UpdateDatabaseStats to be skipped on DeleteEntries error, called %d times", mockRepo.updateDatabaseStatsCalls)
		}
	})

	t.Run("EntryStatusCorrection returns error when zombie DeleteEntries fails", func(t *testing.T) {
		mockRepo := &mockRecoveryRepo{
			databases: []repo.Database{{ID: dbID, Name: "fail_esc_db"}},
			entries:   []repo.Entry{{ID: 1, Status: repo.EntryStatusProcessing}},
			stats:     repo.DatabaseStats{EntryCount: 1},
			deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
				return nil, dbErr
			},
		}
		mockStor := &mockRecoveryStorage{
			statFunc: func(ctx context.Context, dbID ulid.ULID, id int64) (storage.FileInfo, error) {
				return storage.FileInfo{}, customerrors.ErrNotFound
			},
		}

		svc := &RecoveryService{
			repo:    mockRepo,
			storage: mockStor,
			logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		err := svc.EntryStatusCorrection(ctx)
		if !errors.Is(err, dbErr) {
			t.Fatalf("expected EntryStatusCorrection to return wrapped dbErr, got %v", err)
		}
	})
}

