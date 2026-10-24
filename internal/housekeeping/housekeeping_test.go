package housekeeping_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"mediahub_oss/internal/housekeeping"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/storage"
)

type mockHousekeepingRepo struct {
	repo.Repository
	getEntriesFunc            func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error)
	updateEntriesStatusFunc   func(ctx context.Context, dbID repo.ULID, ids []int64, status repo.EntryStatus) error
	deleteEntriesFunc         func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error)
	getDBTimeFunc             func(ctx context.Context) (time.Time, error)
	acquireLockFunc           func(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error)
	releaseLockFunc           func(ctx context.Context, lockName string, ownerID string) (bool, error)
	houseKeepingWasCalledFunc func(ctx context.Context, dbID repo.ULID) (time.Time, error)
}

func (m *mockHousekeepingRepo) GetEntries(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
	if m.getEntriesFunc != nil {
		return m.getEntriesFunc(ctx, dbID, opts)
	}
	return nil, nil
}

func (m *mockHousekeepingRepo) UpdateEntriesStatus(ctx context.Context, dbID repo.ULID, ids []int64, status repo.EntryStatus) error {
	if m.updateEntriesStatusFunc != nil {
		return m.updateEntriesStatusFunc(ctx, dbID, ids, status)
	}
	return nil
}

func (m *mockHousekeepingRepo) DeleteEntries(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
	if m.deleteEntriesFunc != nil {
		return m.deleteEntriesFunc(ctx, dbID, ids)
	}
	return nil, nil
}

func (m *mockHousekeepingRepo) GetDBTime(ctx context.Context) (time.Time, error) {
	if m.getDBTimeFunc != nil {
		return m.getDBTimeFunc(ctx)
	}
	return time.Now(), nil
}

func (m *mockHousekeepingRepo) AcquireLock(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error) {
	if m.acquireLockFunc != nil {
		return m.acquireLockFunc(ctx, lockName, ownerID, ttl)
	}
	return true, nil
}

func (m *mockHousekeepingRepo) ReleaseLock(ctx context.Context, lockName string, ownerID string) (bool, error) {
	if m.releaseLockFunc != nil {
		return m.releaseLockFunc(ctx, lockName, ownerID)
	}
	return true, nil
}

func (m *mockHousekeepingRepo) HouseKeepingWasCalled(ctx context.Context, dbID repo.ULID) (time.Time, error) {
	if m.houseKeepingWasCalledFunc != nil {
		return m.houseKeepingWasCalledFunc(ctx, dbID)
	}
	return time.Now(), nil
}

type mockStorageProvider struct {
	storage.StorageProvider
	deleteMultipleFunc func(ctx context.Context, dbID string, ids []int64) (storage.BulkDeleteResult, error)
}

func (m *mockStorageProvider) DeleteMultiple(ctx context.Context, dbID string, ids []int64) (storage.BulkDeleteResult, error) {
	if m.deleteMultipleFunc != nil {
		return m.deleteMultipleFunc(ctx, dbID, ids)
	}
	return storage.BulkDeleteResult{Success: ids}, nil
}

func (m *mockStorageProvider) DeleteMultiplePreviews(ctx context.Context, dbID string, ids []int64) (storage.BulkDeleteResult, error) {
	return storage.BulkDeleteResult{Success: ids}, nil
}

func TestRunDBHousekeeping_MaxAge_FailedDeletionTerminatesLoop(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	callCount := 0
	mockRepo := &mockHousekeepingRepo{
		getEntriesFunc: func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
			callCount++
			if callCount > 5 {
				t.Fatalf("infinite loop detected in MaxAge: called %d times", callCount)
			}
			return []repo.Entry{
				{ID: 1, Size: 100, Status: repo.EntryStatusError},
			}, nil
		},
		deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
			return nil, nil // No DB rows deleted
		},
	}

	mockStorage := &mockStorageProvider{
		deleteMultipleFunc: func(ctx context.Context, dbID string, ids []int64) (storage.BulkDeleteResult, error) {
			// Simulate storage deletion failure (all failed)
			return storage.BulkDeleteResult{Failed: ids}, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)

	db := repo.Database{
		ID:   "01HGFB9Z5W7ABCDEFGHJKMNPQR",
		Name: "test_db",
		Housekeeping: repo.DatabaseHK{
			MaxAge: 1 * time.Hour,
		},
	}

	delCount, freed, err := hk.RunDBHousekeeping(ctx, db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if delCount != 0 {
		t.Errorf("expected 0 deleted, got %d", delCount)
	}
	if freed != 0 {
		t.Errorf("expected 0 freed, got %d", freed)
	}
	if callCount != 1 {
		t.Errorf("expected GetEntries to be called exactly once, called %d times", callCount)
	}
}

func TestRunDBHousekeeping_DiskSpace_ZeroFreedTerminatesLoop(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	callCount := 0
	mockRepo := &mockHousekeepingRepo{
		getEntriesFunc: func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
			callCount++
			if callCount > 5 {
				t.Fatalf("infinite loop detected in DiskSpace: called %d times", callCount)
			}
			return []repo.Entry{
				{ID: int64(callCount), Size: 0, PreviewSize: 0},
			}, nil
		},
		deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
			return []repo.DeletedEntryMeta{
				{ID: ids[0], Filesize: 0, PreviewSize: 0},
			}, nil
		},
	}

	mockStorage := &mockStorageProvider{}
	hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)

	db := repo.Database{
		ID:   "01HGFB9Z5W7ABCDEFGHJKMNPQR",
		Name: "test_db",
		Housekeeping: repo.DatabaseHK{
			DiskSpace: 1000, // Limit 1000 bytes
		},
		Stats: repo.DatabaseStats{
			TotalDiskSpaceBytes: 5000, // Currently 5000 bytes
		},
	}

	_, freed, err := hk.RunDBHousekeeping(ctx, db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if freed != 0 {
		t.Errorf("expected 0 freed, got %d", freed)
	}
	if callCount != 1 {
		t.Errorf("expected loop to terminate after 1 batch when freed is 0, but called %d times", callCount)
	}
}

func TestRunDBHousekeeping_FiltersDeletableStatusesInSQL(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var recordedOpts []repo.QueryOptions
	mockRepo := &mockHousekeepingRepo{
		getEntriesFunc: func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
			recordedOpts = append(recordedOpts, opts)
			// Return one ready entry on first call of each sweep, then empty
			if len(recordedOpts) == 1 {
				return []repo.Entry{
					{ID: 1, Size: 1000, Status: repo.EntryStatusReady},
				}, nil
			}
			if len(recordedOpts) == 3 {
				return []repo.Entry{
					{ID: 2, Size: 3000, Status: repo.EntryStatusError},
				}, nil
			}
			return nil, nil
		},
		deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
			var metas []repo.DeletedEntryMeta
			for _, id := range ids {
				size := uint64(1000)
				if id == 2 {
					size = 3000
				}
				metas = append(metas, repo.DeletedEntryMeta{ID: id, Filesize: size})
			}
			return metas, nil
		},
	}

	mockStorage := &mockStorageProvider{}
	hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)

	db := repo.Database{
		ID:   "01HGFB9Z5W7ABCDEFGHJKMNPQR",
		Name: "test_db",
		Housekeeping: repo.DatabaseHK{
			MaxAge:    1 * time.Hour,
			DiskSpace: 1500,
		},
		Stats: repo.DatabaseStats{
			TotalDiskSpaceBytes: 5000,
		},
	}

	delCount, freed, err := hk.RunDBHousekeeping(ctx, db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if delCount != 2 {
		t.Errorf("expected 2 deleted, got %d", delCount)
	}
	if freed != 4000 {
		t.Errorf("expected 4000 freed, got %d", freed)
	}
	if len(recordedOpts) < 3 {
		t.Fatalf("expected at least 3 GetEntries calls (MaxAge + DiskSpace), got %d", len(recordedOpts))
	}

	expectedStatuses := []repo.EntryStatus{repo.EntryStatusReady, repo.EntryStatusError}
	for i, opts := range recordedOpts {
		if len(opts.Statuses) != len(expectedStatuses) ||
			opts.Statuses[0] != expectedStatuses[0] ||
			opts.Statuses[1] != expectedStatuses[1] {
			t.Errorf("call %d: expected Statuses=%v, got %v", i, expectedStatuses, opts.Statuses)
		}
	}
}
