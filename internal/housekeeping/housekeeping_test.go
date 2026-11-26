package housekeeping_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mediahub_oss/internal/housekeeping"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
)

type mockHousekeepingRepo struct {
	repo.Repository
	getDatabaseFunc           func(ctx context.Context, dbID repo.ULID) (repo.Database, error)
	getDatabaseStatsFunc      func(ctx context.Context, dbID repo.ULID) (repo.DatabaseStats, error)
	getEntriesFunc            func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error)
	updateEntriesStatusFunc   func(ctx context.Context, dbID repo.ULID, ids []int64, status repo.EntryStatus) error
	deleteEntriesFunc         func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error)
	getDBTimeFunc             func(ctx context.Context) (time.Time, error)
	acquireLockFunc                func(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error)
	releaseLockFunc                func(ctx context.Context, lockName string, ownerID string) (bool, error)
	houseKeepingRequiredFunc       func(ctx context.Context) ([]repo.Database, error)
	houseKeepingWasCalledFunc      func(ctx context.Context, dbID repo.ULID) (time.Time, error)
	deleteExpiredRefreshTokensFunc func(ctx context.Context) (int64, error)
	deleteExpiredAPIKeysFunc       func(ctx context.Context) (int64, error)
	deleteLogsFunc                 func(ctx context.Context, maxAge time.Duration) error
}

func (m *mockHousekeepingRepo) GetDatabase(ctx context.Context, dbID repo.ULID) (repo.Database, error) {
	if m.getDatabaseFunc != nil {
		return m.getDatabaseFunc(ctx, dbID)
	}
	return repo.Database{}, customerrors.ErrNotFound
}

func (m *mockHousekeepingRepo) GetDatabaseStats(ctx context.Context, dbID repo.ULID) (repo.DatabaseStats, error) {
	if m.getDatabaseStatsFunc != nil {
		return m.getDatabaseStatsFunc(ctx, dbID)
	}
	return repo.DatabaseStats{}, customerrors.ErrNotFound
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

func (m *mockHousekeepingRepo) HouseKeepingRequired(ctx context.Context) ([]repo.Database, error) {
	if m.houseKeepingRequiredFunc != nil {
		return m.houseKeepingRequiredFunc(ctx)
	}
	return nil, nil
}

func (m *mockHousekeepingRepo) HouseKeepingWasCalled(ctx context.Context, dbID repo.ULID) (time.Time, error) {
	if m.houseKeepingWasCalledFunc != nil {
		return m.houseKeepingWasCalledFunc(ctx, dbID)
	}
	return time.Now(), nil
}

func (m *mockHousekeepingRepo) DeleteExpiredRefreshTokens(ctx context.Context) (int64, error) {
	if m.deleteExpiredRefreshTokensFunc != nil {
		return m.deleteExpiredRefreshTokensFunc(ctx)
	}
	return 0, nil
}

func (m *mockHousekeepingRepo) DeleteExpiredAPIKeys(ctx context.Context) (int64, error) {
	if m.deleteExpiredAPIKeysFunc != nil {
		return m.deleteExpiredAPIKeysFunc(ctx)
	}
	return 0, nil
}

func (m *mockHousekeepingRepo) DeleteLogs(ctx context.Context, maxAge time.Duration) error {
	if m.deleteLogsFunc != nil {
		return m.deleteLogsFunc(ctx, maxAge)
	}
	return nil
}

type mockStorageProvider struct {
	storage.StorageProvider
	deleteMultipleFunc func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error)
}

func (m *mockStorageProvider) DeleteMultiple(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
	if m.deleteMultipleFunc != nil {
		return m.deleteMultipleFunc(ctx, dbID, ids)
	}
	return storage.BulkDeleteResult{Success: ids}, nil
}

func (m *mockStorageProvider) DeleteMultiplePreviews(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
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
		deleteMultipleFunc: func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
			// Simulate storage deletion failure (all failed)
			return storage.BulkDeleteResult{Failed: ids}, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)

	db := repo.Database{
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
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
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
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
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
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

func TestRunDBHousekeeping_FreshStatsReFetchAvoidsStaleRun(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	getEntriesCalled := false
	mockRepo := &mockHousekeepingRepo{
		getDatabaseFunc: func(ctx context.Context, dbID repo.ULID) (repo.Database, error) {
			// Return fresh stats indicating disk space is already below the limit
			return repo.Database{
				ID:   dbID,
				Name: "test_db",
				Stats: repo.DatabaseStats{
					TotalDiskSpaceBytes: 2000, // Below limit of 5000
				},
			}, nil
		},
		getDatabaseStatsFunc: func(ctx context.Context, dbID repo.ULID) (repo.DatabaseStats, error) {
			return repo.DatabaseStats{
				TotalDiskSpaceBytes: 2000,
			}, nil
		},
		getEntriesFunc: func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
			getEntriesCalled = true
			return nil, nil
		},
	}

	mockStorage := &mockStorageProvider{}
	hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)

	// Pre-lock stale db snapshot has 10000 bytes (exceeding limit of 5000)
	staleDB := repo.Database{
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Name: "test_db",
		Housekeeping: repo.DatabaseHK{
			DiskSpace: 5000,
		},
		Stats: repo.DatabaseStats{
			TotalDiskSpaceBytes: 10000,
		},
	}

	delCount, freed, err := hk.RunDBHousekeeping(ctx, staleDB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if delCount != 0 || freed != 0 {
		t.Errorf("expected 0 deletions, got %d del, %d freed", delCount, freed)
	}
	if getEntriesCalled {
		t.Errorf("expected GetEntries not to be called because fresh stats are below limit")
	}
}

func TestRunDBHousekeeping_ConcurrentSameHouseKeeperReturnsErrLockNotAcquired(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	r, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create sqlite repository: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to migrate sqlite repository: %v", err)
	}

	createdDB, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "concurrent_hk_db",
		ContentType: "file",
		Housekeeping: repo.DatabaseHK{
			MaxAge: 1 * time.Hour,
		},
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	_, err = r.CreateEntry(ctx, createdDB, repo.Entry{
		FileName:  "expired.txt",
		MimeType:  "text/plain",
		Size:      100,
		Status:    repo.EntryStatusReady,
		Timestamp: time.Now().Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	var startOnce sync.Once

	blockingStorage := &mockStorageProvider{
		deleteMultipleFunc: func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
			startOnce.Do(func() {
				close(deleteStarted)
			})
			<-releaseDelete
			return storage.BulkDeleteResult{Success: ids}, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(r, blockingStorage, logger, 24*time.Hour)

	type hkResult struct {
		delCount int
		freed    uint64
		err      error
	}
	firstDone := make(chan hkResult, 1)
	go func() {
		del, freed, err := hk.RunDBHousekeeping(ctx, createdDB)
		firstDone <- hkResult{delCount: del, freed: freed, err: err}
	}()

	select {
	case <-deleteStarted:
	case <-time.After(5 * time.Second):
		close(releaseDelete)
		t.Fatal("timed out waiting for first RunDBHousekeeping to reach DeleteMultiple")
	}

	// Second concurrent RunDBHousekeeping on the SAME HouseKeeper must fail to acquire the lock.
	_, _, err2 := hk.RunDBHousekeeping(ctx, createdDB)
	if !errors.Is(err2, customerrors.ErrLockNotAcquired) {
		close(releaseDelete)
		t.Fatalf("expected customerrors.ErrLockNotAcquired for concurrent run, got %v", err2)
	}

	close(releaseDelete)

	res := <-firstDone
	if res.err != nil {
		t.Fatalf("first RunDBHousekeeping failed: %v", res.err)
	}
	if res.delCount != 1 || res.freed != 100 {
		t.Errorf("expected first run to delete 1 entry and free 100 bytes, got del=%d freed=%d", res.delCount, res.freed)
	}

	// Lock must be released after first run finishes.
	if _, _, err3 := hk.RunDBHousekeeping(ctx, createdDB); err3 != nil {
		t.Fatalf("expected subsequent RunDBHousekeeping after lock release to succeed, got %v", err3)
	}
}

func TestRunDBHousekeeping_DistinctOwnerIDPerRunAndMatchedRelease(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var acquiredOwners []string
	var releasedOwners []string

	mockRepo := &mockHousekeepingRepo{
		acquireLockFunc: func(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error) {
			acquiredOwners = append(acquiredOwners, ownerID)
			return true, nil
		},
		releaseLockFunc: func(ctx context.Context, lockName string, ownerID string) (bool, error) {
			releasedOwners = append(releasedOwners, ownerID)
			return true, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(mockRepo, &mockStorageProvider{}, logger, 24*time.Hour)
	db := repo.Database{
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Name: "owner_test_db",
	}

	if _, _, err := hk.RunDBHousekeeping(ctx, db); err != nil {
		t.Fatalf("first RunDBHousekeeping failed: %v", err)
	}
	if _, _, err := hk.RunDBHousekeeping(ctx, db); err != nil {
		t.Fatalf("second RunDBHousekeeping failed: %v", err)
	}

	if len(acquiredOwners) != 2 || len(releasedOwners) != 2 {
		t.Fatalf("expected 2 acquire and 2 release calls, got %d acquire, %d release", len(acquiredOwners), len(releasedOwners))
	}
	for i := 0; i < 2; i++ {
		if acquiredOwners[i] == "" {
			t.Errorf("run %d: expected non-empty ownerID", i)
		}
		if acquiredOwners[i] != releasedOwners[i] {
			t.Errorf("run %d: AcquireLock ownerID %q != ReleaseLock ownerID %q", i, acquiredOwners[i], releasedOwners[i])
		}
	}
	if acquiredOwners[0] == acquiredOwners[1] {
		t.Errorf("expected distinct ownerID per run, both runs used %q", acquiredOwners[0])
	}
}

func TestRunDBHousekeeping_LockOwnerIDBoundedTo128Bytes(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var acquiredOwner string
	var releasedOwner string

	mockRepo := &mockHousekeepingRepo{
		acquireLockFunc: func(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error) {
			acquiredOwner = ownerID
			return true, nil
		},
		releaseLockFunc: func(ctx context.Context, lockName string, ownerID string) (bool, error) {
			releasedOwner = ownerID
			return true, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(mockRepo, &mockStorageProvider{}, logger, 24*time.Hour)
	hk.InstanceID = strings.Repeat("a", 250)

	db := repo.Database{
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Name: "bounded_owner_db",
	}

	if _, _, err := hk.RunDBHousekeeping(ctx, db); err != nil {
		t.Fatalf("RunDBHousekeeping failed: %v", err)
	}

	if acquiredOwner == "" {
		t.Fatal("expected non-empty ownerID")
	}
	if len(acquiredOwner) > 128 {
		t.Errorf("expected len(ownerID) <= 128, got %d (%q)", len(acquiredOwner), acquiredOwner)
	}
	if acquiredOwner != releasedOwner {
		t.Errorf("AcquireLock ownerID %q != ReleaseLock ownerID %q", acquiredOwner, releasedOwner)
	}

	// Empty InstanceID should fall back to "housekeeper-<ulid>" and still be <= 128 bytes.
	hk.InstanceID = ""
	if _, _, err := hk.RunDBHousekeeping(ctx, db); err != nil {
		t.Fatalf("RunDBHousekeeping with empty InstanceID failed: %v", err)
	}
	if !strings.HasPrefix(acquiredOwner, "housekeeper-") {
		t.Errorf("expected empty InstanceID fallback to start with 'housekeeper-', got %q", acquiredOwner)
	}
	if len(acquiredOwner) > 128 {
		t.Errorf("expected fallback len(ownerID) <= 128, got %d", len(acquiredOwner))
	}
}

func TestRunDBHousekeeping_DetachedReleaseLockOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var releaseCalled bool
	var releaseCtxErr error
	var hkWasCalled bool

	mockRepo := &mockHousekeepingRepo{
		getEntriesFunc: func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
			return []repo.Entry{{ID: 1, Size: 100, Status: repo.EntryStatusReady}}, nil
		},
		deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
			return []repo.DeletedEntryMeta{{ID: 1, Filesize: 100}}, nil
		},
		releaseLockFunc: func(releaseCtx context.Context, lockName string, ownerID string) (bool, error) {
			releaseCalled = true
			releaseCtxErr = releaseCtx.Err()
			return true, nil
		},
		houseKeepingWasCalledFunc: func(ctx context.Context, dbID repo.ULID) (time.Time, error) {
			hkWasCalled = true
			return time.Now(), nil
		},
	}

	mockStorage := &mockStorageProvider{
		deleteMultipleFunc: func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
			cancel()
			return storage.BulkDeleteResult{Success: ids}, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)
	hk.LockRenewInterval = 0

	db := repo.Database{
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Name: "detached_unlock_db",
		Housekeeping: repo.DatabaseHK{
			MaxAge: 1 * time.Hour,
		},
	}

	_, _, err := hk.RunDBHousekeeping(ctx, db)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error when sweep aborted mid-run, got %v", err)
	}
	if !releaseCalled {
		t.Fatal("expected ReleaseLock to be called on canceled sweep")
	}
	if releaseCtxErr != nil {
		t.Fatalf("expected detached ReleaseLock context to be non-canceled, got %v", releaseCtxErr)
	}
	if hkWasCalled {
		t.Errorf("expected HouseKeepingWasCalled not to be called after canceled sweep")
	}
}

func TestRunDBHousekeeping_PeriodicLockRenewal(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	runRenewalCase := func(t *testing.T, lockTTL, renewInterval time.Duration) {
		t.Helper()
		ctx := context.Background()

		var mu sync.Mutex
		type acquireCall struct {
			owner string
			ttl   time.Duration
		}
		var acquireCalls []acquireCall
		var releasedOwner string
		renewedTwice := make(chan struct{})
		var once sync.Once
		var getCalls atomic.Int32

		mockRepo := &mockHousekeepingRepo{
			acquireLockFunc: func(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error) {
				mu.Lock()
				acquireCalls = append(acquireCalls, acquireCall{owner: ownerID, ttl: ttl})
				count := len(acquireCalls)
				mu.Unlock()
				if count >= 3 {
					once.Do(func() { close(renewedTwice) })
				}
				return true, nil
			},
			releaseLockFunc: func(ctx context.Context, lockName string, ownerID string) (bool, error) {
				mu.Lock()
				releasedOwner = ownerID
				mu.Unlock()
				return true, nil
			},
			getEntriesFunc: func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
				if getCalls.Add(1) == 1 {
					return []repo.Entry{{ID: 1, Size: 100, Status: repo.EntryStatusReady}}, nil
				}
				return nil, nil
			},
			deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
				return []repo.DeletedEntryMeta{{ID: 1, Filesize: 100}}, nil
			},
		}

		mockStorage := &mockStorageProvider{
			deleteMultipleFunc: func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
				select {
				case <-renewedTwice:
				case <-time.After(2 * time.Second):
					return storage.BulkDeleteResult{}, errors.New("timed out waiting for lock renewals")
				}
				return storage.BulkDeleteResult{Success: ids}, nil
			},
		}

		hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)
		hk.LockTTL = lockTTL
		if renewInterval >= 0 {
			hk.LockRenewInterval = renewInterval
		}

		db := repo.Database{
			ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
			Name: "renewal_db",
			Housekeeping: repo.DatabaseHK{
				MaxAge: 1 * time.Hour,
			},
		}

		delCount, freed, err := hk.RunDBHousekeeping(ctx, db)
		if err != nil {
			t.Fatalf("RunDBHousekeeping failed: %v", err)
		}
		if delCount != 1 || freed != 100 {
			t.Fatalf("expected 1 deleted / 100 freed, got %d / %d", delCount, freed)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(acquireCalls) < 3 {
			t.Fatalf("expected >= 3 AcquireLock calls (initial + >= 2 renewals), got %d", len(acquireCalls))
		}
		expectedOwner := acquireCalls[0].owner
		for i, c := range acquireCalls {
			if c.owner != expectedOwner {
				t.Errorf("call %d: expected owner %q, got %q", i, expectedOwner, c.owner)
			}
			if c.ttl != lockTTL {
				t.Errorf("call %d: expected ttl %v, got %v", i, lockTTL, c.ttl)
			}
		}
		if releasedOwner != expectedOwner {
			t.Errorf("expected ReleaseLock owner %q, got %q", expectedOwner, releasedOwner)
		}
	}

	t.Run("explicit short renewal interval", func(t *testing.T) {
		runRenewalCase(t, 200*time.Millisecond, 40*time.Millisecond)
	})

	t.Run("caps default 10m renewal interval below short LockTTL", func(t *testing.T) {
		runRenewalCase(t, 150*time.Millisecond, -1)
	})
}

func TestRunDBHousekeeping_RenewalLostOwnershipAbortsSweep(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var acquireCalls atomic.Int32
	var getEntriesCalls atomic.Int32
	var deleteCalls atomic.Int32
	var hkWasCalled atomic.Bool

	mockRepo := &mockHousekeepingRepo{
		acquireLockFunc: func(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error) {
			if acquireCalls.Add(1) == 1 {
				return true, nil
			}
			// Simulate expired lock stolen by another pod on first renewal tick.
			return false, nil
		},
		getEntriesFunc: func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
			call := getEntriesCalls.Add(1)
			return []repo.Entry{{ID: int64(call), Size: 100, Status: repo.EntryStatusReady}}, nil
		},
		deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
			return []repo.DeletedEntryMeta{{ID: ids[0], Filesize: 100}}, nil
		},
		houseKeepingWasCalledFunc: func(ctx context.Context, dbID repo.ULID) (time.Time, error) {
			hkWasCalled.Store(true)
			return time.Now(), nil
		},
	}

	mockStorage := &mockStorageProvider{
		deleteMultipleFunc: func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
			deleteCalls.Add(1)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				return storage.BulkDeleteResult{}, errors.New("timed out waiting for sweep cancellation after lost lock")
			}
			// Even if storage finished normally right as lock was lost, next loop iteration must abort.
			return storage.BulkDeleteResult{Success: ids}, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)
	hk.LockTTL = 200 * time.Millisecond
	hk.LockRenewInterval = 25 * time.Millisecond

	db := repo.Database{
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Name: "lost_lock_db",
		Housekeeping: repo.DatabaseHK{
			MaxAge:    1 * time.Hour,
			DiskSpace: 10,
		},
		Stats: repo.DatabaseStats{
			TotalDiskSpaceBytes: 5000,
		},
	}

	_, _, err := hk.RunDBHousekeeping(ctx, db)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected error wrapping context.Canceled after losing lock, got %v", err)
	}
	if getEntriesCalls.Load() != 1 {
		t.Errorf("expected sweep to stop after 1 GetEntries call, got %d", getEntriesCalls.Load())
	}
	if deleteCalls.Load() != 1 {
		t.Errorf("expected sweep to stop after 1 DeleteMultiple call, got %d", deleteCalls.Load())
	}
	if hkWasCalled.Load() {
		t.Errorf("expected HouseKeepingWasCalled not to be called after losing lock")
	}
}

func TestStartScheduler_ShutdownDrainsInFlightRun(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	inFlightStarted := make(chan struct{})
	releaseInFlight := make(chan struct{})
	var startOnce sync.Once
	var getCalls atomic.Int32

	db := repo.Database{
		ID:   ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Name: "scheduler_drain_db",
		Housekeeping: repo.DatabaseHK{
			MaxAge: 1 * time.Hour,
		},
	}

	mockRepo := &mockHousekeepingRepo{
		houseKeepingRequiredFunc: func(ctx context.Context) ([]repo.Database, error) {
			return []repo.Database{db}, nil
		},
		getEntriesFunc: func(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
			if getCalls.Add(1) == 1 {
				return []repo.Entry{{ID: 1, Size: 100, Status: repo.EntryStatusReady}}, nil
			}
			return nil, nil
		},
		deleteEntriesFunc: func(ctx context.Context, dbID repo.ULID, ids []int64) ([]repo.DeletedEntryMeta, error) {
			return []repo.DeletedEntryMeta{{ID: 1, Filesize: 100}}, nil
		},
	}

	mockStorage := &mockStorageProvider{
		deleteMultipleFunc: func(ctx context.Context, dbID ulid.ULID, ids []int64) (storage.BulkDeleteResult, error) {
			startOnce.Do(func() { close(inFlightStarted) })
			<-releaseInFlight
			return storage.BulkDeleteResult{Success: ids}, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(mockRepo, mockStorage, logger, 24*time.Hour)
	hk.SchedulerInterval = 15 * time.Millisecond
	hk.LockRenewInterval = 0

	schedCtx, cancelSched := context.WithCancel(context.Background())
	hk.StartScheduler(schedCtx)

	select {
	case <-inFlightStarted:
	case <-time.After(2 * time.Second):
		cancelSched()
		close(releaseInFlight)
		t.Fatal("timed out waiting for scheduled run to start")
	}

	// Cancel scheduler and immediately begin Shutdown: it must wait until releaseInFlight closes.
	cancelSched()
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelShutdown()
		shutdownDone <- hk.Shutdown(shutdownCtx)
	}()

	select {
	case err := <-shutdownDone:
		close(releaseInFlight)
		t.Fatalf("Shutdown returned before in-flight scheduled run finished (err=%v)", err)
	case <-time.After(60 * time.Millisecond):
	}

	close(releaseInFlight)
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Shutdown after releasing in-flight run")
	}
}

func TestStartScheduler_ShutdownRespectsContextTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	inFlightStarted := make(chan struct{})
	releaseInFlight := make(chan struct{})
	var startOnce sync.Once

	mockRepo := &mockHousekeepingRepo{
		deleteExpiredRefreshTokensFunc: func(ctx context.Context) (int64, error) {
			startOnce.Do(func() { close(inFlightStarted) })
			<-releaseInFlight
			return 0, nil
		},
	}

	hk := housekeeping.NewHouseKeeper(mockRepo, &mockStorageProvider{}, logger, 24*time.Hour)
	hk.SchedulerInterval = 15 * time.Millisecond

	schedCtx, cancelSched := context.WithCancel(context.Background())
	hk.StartScheduler(schedCtx)

	select {
	case <-inFlightStarted:
	case <-time.After(2 * time.Second):
		cancelSched()
		close(releaseInFlight)
		t.Fatal("timed out waiting for scheduled global task")
	}

	cancelSched()
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelShort()

	if err := hk.Shutdown(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		close(releaseInFlight)
		t.Fatalf("expected context.DeadlineExceeded from Shutdown, got %v", err)
	}

	close(releaseInFlight)
	if err := hk.Shutdown(context.Background()); err != nil {
		t.Fatalf("expected clean drain after releasing blocked task, got %v", err)
	}
}
