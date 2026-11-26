package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
)

const (
	maxLockOwnerLen          = 128
	defaultSchedulerInterval = 5 * time.Minute
	defaultLockTTL           = 30 * time.Minute
	defaultLockRenewInterval = 10 * time.Minute
	lockOpTimeout            = 5 * time.Second
	maxConsecutiveRenewFails = 3
)

// HouseKeeper manages both scheduled and manual housekeeping tasks.
type HouseKeeper struct {
	Repo              repository.Repository
	Storage           storage.StorageProvider
	Logger            *slog.Logger
	InstanceID        string // Unique identifier for the pod/node
	AuditRetention    time.Duration
	SchedulerInterval time.Duration
	LockTTL           time.Duration
	LockRenewInterval time.Duration
	wg                sync.WaitGroup
}

// NewHouseKeeper creates a new Housekeeping Service.
func NewHouseKeeper(repo repository.Repository, storage storage.StorageProvider, logger *slog.Logger, auditRetention time.Duration) *HouseKeeper {
	// Use the hostname (Pod name in K8s) as the base instance ID.
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	if len(hostname) > 64 {
		hostname = hostname[:64]
	}
	// Append startup timestamp and a random suffix to ensure uniqueness even if
	// several instances start on the same host within the same second (lock
	// ownership must never collide).
	instanceID := fmt.Sprintf("%s-%d-%s", hostname, time.Now().Unix(), ulid.New().String())

	return &HouseKeeper{
		Repo:              repo,
		Storage:           storage,
		Logger:            logger,
		InstanceID:        instanceID,
		AuditRetention:    auditRetention,
		SchedulerInterval: defaultSchedulerInterval,
		LockTTL:           defaultLockTTL,
		LockRenewInterval: defaultLockRenewInterval,
	}
}

// newLockOwnerID generates a unique per-run lock owner identifier bounded to maxLockOwnerLen bytes.
func (s *HouseKeeper) newLockOwnerID() string {
	runID := ulid.New().String()
	base := s.InstanceID
	if base == "" {
		base = "housekeeper"
	}
	maxBaseLen := maxLockOwnerLen - 1 - len(runID)
	if maxBaseLen < 0 {
		maxBaseLen = 0
	}
	if len(base) > maxBaseLen {
		base = base[:maxBaseLen]
	}
	if maxBaseLen == 0 {
		return runID[:min(len(runID), maxLockOwnerLen)]
	}
	return base + "-" + runID
}

// StartScheduler launches a background goroutine that periodically checks all databases
// to see if their housekeeping interval has passed.
func (s *HouseKeeper) StartScheduler(ctx context.Context) {
	interval := s.SchedulerInterval
	if interval <= 0 {
		interval = defaultSchedulerInterval
	}
	ticker := time.NewTicker(interval)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				s.Logger.Info("Stopping housekeeping scheduler")
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					s.Logger.Info("Stopping housekeeping scheduler")
					return
				}
				s.runGlobalTasks(ctx)
				if ctx.Err() != nil {
					s.Logger.Info("Stopping housekeeping scheduler")
					return
				}
				s.runDBTasks(ctx)
			}
		}
	}()
}

// Shutdown waits for the housekeeping scheduler and any active scheduled run
// to complete, or returns ctx.Err() if the shutdown context expires first.
func (s *HouseKeeper) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *HouseKeeper) releaseLockDetached(ctx context.Context, lockName, ownerID, scope string) {
	unlockCtx, unlockCancel := context.WithTimeout(context.WithoutCancel(ctx), lockOpTimeout)
	defer unlockCancel()

	released, err := s.Repo.ReleaseLock(unlockCtx, lockName, ownerID)
	if err != nil {
		s.Logger.Error("Failed to release distributed lock", "scope", scope, "lock_name", lockName, "owner_id", ownerID, "error", err)
	} else if !released {
		s.Logger.Warn("Distributed lock was not released; lock expired or was claimed by another instance", "scope", scope, "lock_name", lockName, "owner_id", ownerID)
	}
}

func (s *HouseKeeper) resolveLockTimings() (time.Duration, time.Duration) {
	lockTTL := s.LockTTL
	if lockTTL <= 0 {
		lockTTL = defaultLockTTL
	}
	renewInterval := s.LockRenewInterval
	if renewInterval > 0 && renewInterval >= lockTTL {
		// Cap renewal below TTL so short custom TTLs (e.g. in tests) still renew before expiry.
		renewInterval = lockTTL / 3
		if renewInterval <= 0 {
			renewInterval = lockTTL / 2
		}
	}
	return lockTTL, renewInterval
}

func (s *HouseKeeper) runLockHeartbeat(
	sweepCtx context.Context,
	sweepCancel context.CancelFunc,
	lockName string,
	ownerID string,
	lockTTL time.Duration,
	renewInterval time.Duration,
	db repository.Database,
	lockLost *atomic.Bool,
	done chan<- struct{},
) {
	defer close(done)

	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	consecutiveFailures := 0
	for {
		select {
		case <-sweepCtx.Done():
			return
		case <-ticker.C:
			if sweepCtx.Err() != nil {
				return
			}

			renewCtx, cancel := context.WithTimeout(sweepCtx, lockOpTimeout)
			acquired, err := s.Repo.AcquireLock(renewCtx, lockName, ownerID, lockTTL)
			cancel()

			if sweepCtx.Err() != nil {
				return
			}
			if err == nil && acquired {
				consecutiveFailures = 0
				continue
			}
			if err == nil && !acquired {
				s.Logger.Warn("Housekeeping lock expired or was claimed by another instance; aborting sweep",
					"database_id", db.ID,
					"database_name", db.Name,
					"lock_name", lockName,
					"owner_id", ownerID,
				)
				lockLost.Store(true)
				sweepCancel()
				return
			}

			consecutiveFailures++
			s.Logger.Warn("Failed to renew housekeeping lock",
				"database_id", db.ID,
				"database_name", db.Name,
				"lock_name", lockName,
				"owner_id", ownerID,
				"consecutive_failures", consecutiveFailures,
				"error", err,
			)
			if consecutiveFailures >= maxConsecutiveRenewFails {
				s.Logger.Warn("Aborting housekeeping sweep after repeated lock renewal failures",
					"database_id", db.ID,
					"database_name", db.Name,
					"lock_name", lockName,
					"owner_id", ownerID,
				)
				lockLost.Store(true)
				sweepCancel()
				return
			}
		}
	}
}

func sweepAbortErr(err error, lockLost bool) error {
	if lockLost {
		return fmt.Errorf("housekeeping lock lost: %w", err)
	}
	return err
}

// runGlobalTasks handles maintenance that is not tied to a specific media database.
func (s *HouseKeeper) runGlobalTasks(ctx context.Context) {
	lockName := "global_tasks"
	ownerID := s.newLockOwnerID()

	// Attempt to acquire the lock for 5 minutes
	acquired, err := s.Repo.AcquireLock(ctx, lockName, ownerID, 5*time.Minute)
	if err != nil {
		s.Logger.Error("Failed to acquire lock for global tasks", "error", err)
		return
	}
	if !acquired {
		s.Logger.Debug("Global tasks are already running on another instance")
		return
	}

	// Ensure the lock is released even if ctx is canceled during shutdown
	defer s.releaseLockDetached(ctx, lockName, ownerID, "global_tasks")

	// Execute global maintenance

	// 1. Clean up expired refresh tokens
	deletedCount, err := s.Repo.DeleteExpiredRefreshTokens(ctx)
	if err != nil {
		s.Logger.Error("Failed to clean up expired refresh tokens", "error", err)
	} else if deletedCount > 0 {
		s.Logger.Info("Cleaned up expired refresh tokens", "deleted_count", deletedCount)
	}

	if ctx.Err() != nil {
		return
	}

	// 1b. Clean up expired API keys
	deletedKeysCount, err := s.Repo.DeleteExpiredAPIKeys(ctx)
	if err != nil {
		s.Logger.Error("Failed to clean up expired API keys", "error", err)
	} else if deletedKeysCount > 0 {
		s.Logger.Info("Cleaned up expired API keys", "deleted_count", deletedKeysCount)
	}

	if ctx.Err() != nil {
		return
	}

	// 2. Clean up old audit logs
	if s.AuditRetention > 0 {
		if err := s.Repo.DeleteLogs(ctx, s.AuditRetention); err != nil {
			s.Logger.Error("Failed to clean up old audit logs", "error", err)
		} else {
			s.Logger.Debug("Audit log cleanup routine executed successfully")
		}
	}
}

func (s *HouseKeeper) runDBTasks(ctx context.Context) {
	// Fetch ONLY the databases that need housekeeping, relying on the DB server's clock.
	reqDbs, err := s.Repo.HouseKeepingRequired(ctx)
	if err != nil {
		s.Logger.Error("Housekeeping scheduler failed to fetch required databases", "error", err)
		return
	}

	for _, db := range reqDbs {
		if ctx.Err() != nil {
			return
		}
		s.Logger.Debug("Triggering scheduled housekeeping", "database_id", db.ID, "database_name", db.Name)

		// Run synchronously to avoid spiking CPU/Disk I/O with concurrent sweeps
		_, _, err := s.RunDBHousekeeping(ctx, db)
		if err != nil {
			if errors.Is(err, customerrors.ErrLockNotAcquired) {
				s.Logger.Debug("Skipping scheduled housekeeping; locked by another instance", "database_id", db.ID, "database_name", db.Name)
			} else {
				s.Logger.Error("Scheduled housekeeping failed", "database_id", db.ID, "database_name", db.Name, "error", err)
			}
		}
	}
}

// RunDBHousekeeping executes the cleanup logic for a single database.
// This can be called by the scheduler or manually via the API. Batch failures
// are collected and returned so API callers can distinguish "nothing to do"
// from "the sweep failed".
func (s *HouseKeeper) RunDBHousekeeping(ctx context.Context, db repository.Database) (int, uint64, error) {
	lockName := "hk_" + db.ID.String()
	ownerID := s.newLockOwnerID()
	lockTTL, renewInterval := s.resolveLockTimings()
	var totalDeleted int
	var totalFreed uint64
	var runErr error

	// 1. Acquire Distributed Lock
	acquired, err := s.Repo.AcquireLock(ctx, lockName, ownerID, lockTTL)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to check lock status: %w", err)
	}
	if !acquired {
		return 0, 0, customerrors.ErrLockNotAcquired
	}

	// Register ReleaseLock FIRST so LIFO executes it LAST, after the heartbeat stops and ctx is detached.
	defer s.releaseLockDetached(ctx, lockName, ownerID, "database:"+db.Name)

	sweepCtx, sweepCancel := context.WithCancel(ctx)
	var lockLost atomic.Bool
	if renewInterval > 0 {
		heartbeatDone := make(chan struct{})
		go s.runLockHeartbeat(sweepCtx, sweepCancel, lockName, ownerID, lockTTL, renewInterval, db, &lockLost, heartbeatDone)
		defer func() {
			sweepCancel()
			<-heartbeatDone
		}()
	} else {
		defer sweepCancel()
	}

	deletableStatuses := []repository.EntryStatus{repository.EntryStatusReady, repository.EntryStatusError}

	// If MaxAge is 0, this check is disabled.
	if db.Housekeeping.MaxAge > 0 {
		maxAgeDur := db.Housekeeping.MaxAge

		// 1. Fetch the unified database time
		dbTime, err := s.Repo.GetDBTime(sweepCtx)
		if err != nil {
			s.Logger.Error("Housekeeper failed to get DB time for MaxAge cutoff", "error", err, "database", db.Name)
			return totalDeleted, totalFreed, sweepAbortErr(err, lockLost.Load())
		}

		// 2. Calculate cutoff using DB time and the MaxAge duration
		cutoff := dbTime.Add(-maxAgeDur)

		for {
			if err := sweepCtx.Err(); err != nil {
				runErr = errors.Join(runErr, sweepAbortErr(err, lockLost.Load()))
				break
			}

			// We process in batches of 100 to prevent memory spikes.
			entries, err := s.Repo.GetEntries(sweepCtx, db.ID, repository.QueryOptions{
				Limit:    100,
				Offset:   0,
				Order:    "asc",
				TEnd:     cutoff,
				Statuses: deletableStatuses,
			})
			if err != nil {
				s.Logger.Error("Housekeeper failed to fetch entries for MaxAge", "error", err, "database_id", db.ID, "database_name", db.Name)
				runErr = errors.Join(runErr, fmt.Errorf("max-age sweep fetch: %w", err))
				break
			}

			// If no more entries are returned, we've deleted all old files!
			if len(entries) == 0 {
				break
			}

			// Never touch entries that are still in flight (processing, queued
			// or mid-deletion); they belong to upload/queue/delete workflows and
			// deleting them would race with their workers and orphan files.
			deletable := filterDeletableEntries(entries)
			if len(deletable) == 0 {
				s.Logger.Debug("Housekeeper skipped in-flight entries during MaxAge sweep", "database_id", db.ID, "database_name", db.Name)
				break
			}

			delCount, freed, err := s.deleteEntriesBatch(sweepCtx, db.ID, deletable)
			totalDeleted += delCount
			totalFreed += freed

			if err != nil {
				s.Logger.Error("Housekeeper failed during MaxAge batch deletion", "error", err, "database_id", db.ID, "database_name", db.Name)
				runErr = errors.Join(runErr, fmt.Errorf("max-age batch deletion: %w", err))
				break
			}
			if delCount == 0 {
				s.Logger.Warn("Housekeeper made no deletion progress during MaxAge batch, aborting loop", "database_id", db.ID, "database_name", db.Name)
				break
			}
		}
	}

	// If DiskSpace is 0, this check is disabled.
	if db.Housekeeping.DiskSpace > 0 && sweepCtx.Err() == nil {
		currentSpace := s.resolveCurrentDiskSpace(sweepCtx, db, totalFreed)
		limit := db.Housekeeping.DiskSpace

		for currentSpace > limit {
			if err := sweepCtx.Err(); err != nil {
				runErr = errors.Join(runErr, sweepAbortErr(err, lockLost.Load()))
				break
			}

			// Fetch the absolute oldest entries in the DB, regardless of age
			entries, err := s.Repo.GetEntries(sweepCtx, db.ID, repository.QueryOptions{
				Limit:    100,
				Offset:   0,
				Order:    "asc",
				Statuses: deletableStatuses,
			})
			if err != nil {
				s.Logger.Error("Housekeeper failed to fetch entries for DiskSpace", "error", err, "database_id", db.ID, "database_name", db.Name)
				runErr = errors.Join(runErr, fmt.Errorf("disk-space sweep fetch: %w", err))
				break
			}
			if len(entries) == 0 {
				break // no entries left
			}

			// Skip entries that are still in flight (see MaxAge loop above).
			deletable := filterDeletableEntries(entries)
			if len(deletable) == 0 {
				s.Logger.Debug("Housekeeper skipped in-flight entries during DiskSpace sweep", "database_id", db.ID, "database_name", db.Name)
				break
			}

			// Accumulate just enough entries to dip below the limit
			var slideEnd int
			var targetSpaceToFree uint64

			for i, e := range deletable {
				targetSpaceToFree += e.Size + e.PreviewSize
				slideEnd = i + 1

				// Check if this entry pushes us under the limit
				if targetSpaceToFree >= currentSpace || currentSpace-targetSpaceToFree <= limit {
					break
				}
			}

			delCount, freed, err := s.deleteEntriesBatch(sweepCtx, db.ID, deletable[:slideEnd])
			totalDeleted += delCount
			totalFreed += freed
			if currentSpace > freed {
				currentSpace -= freed
			} else {
				currentSpace = 0
			}

			if err != nil {
				s.Logger.Error("Housekeeper failed during DiskSpace batch deletion", "error", err, "database_id", db.ID, "database_name", db.Name)
				runErr = errors.Join(runErr, fmt.Errorf("disk-space batch deletion: %w", err))
				break
			}
			// Safety valve: abort when the batch made no progress. Zero-size
			// entries free no space, so continuing would never advance the goal
			// of this loop and could spin indefinitely if such rows keep
			// appearing; the next scheduled run continues where we stopped.
			if delCount == 0 || freed == 0 {
				s.Logger.Warn("Housekeeper made no progress freeing disk space, aborting loop", "database_id", db.ID, "database_name", db.Name, "delCount", delCount, "freed", freed)
				break
			}
		}
	}

	if err := sweepCtx.Err(); err != nil {
		if !errors.Is(runErr, err) {
			runErr = errors.Join(runErr, sweepAbortErr(err, lockLost.Load()))
		}
		s.Logger.Warn("Housekeeping aborted before completion", "database_id", db.ID.String(), "database_name", db.Name, "error", err)
		return totalDeleted, totalFreed, runErr
	}

	// Update LastHkRun utilizing the new atomic database method to prevent stat overwrites
	_, err = s.Repo.HouseKeepingWasCalled(sweepCtx, db.ID)
	if err != nil {
		s.Logger.Error("Housekeeper failed to update LastHkRun", "error", err, "database_id", db.ID, "database_name", db.Name)
		runErr = errors.Join(runErr, fmt.Errorf("update LastHkRun: %w", err))
	}

	s.Logger.Info("Housekeeping completed", "database_id", db.ID.String(), "database_name", db.Name, "deleted", totalDeleted, "freed_bytes", totalFreed)
	return totalDeleted, totalFreed, runErr
}

func (s *HouseKeeper) resolveCurrentDiskSpace(ctx context.Context, db repository.Database, totalFreed uint64) uint64 {
	if freshStats, err := s.Repo.GetDatabaseStats(ctx, db.ID); err == nil {
		return freshStats.TotalDiskSpaceBytes
	}
	if freshDB, err := s.Repo.GetDatabase(ctx, db.ID); err == nil {
		return freshDB.Stats.TotalDiskSpaceBytes
	}
	if db.Stats.TotalDiskSpaceBytes > totalFreed {
		return db.Stats.TotalDiskSpaceBytes - totalFreed
	}
	return 0
}

// filterDeletableEntries returns only the entries housekeeping may delete:
// entries in an in-flight state (processing, queued, mid-deletion) are owned by
// upload/queue/delete workflows and must never be deleted from underneath them,
// or their workers would write files for rows that no longer exist.
func filterDeletableEntries(entries []repository.Entry) []repository.Entry {
	deletable := make([]repository.Entry, 0, len(entries))
	for _, e := range entries {
		if e.Status == repository.EntryStatusReady || e.Status == repository.EntryStatusError {
			deletable = append(deletable, e)
		}
	}
	return deletable
}

// deleteEntriesBatch safely deletes a batch of entries from the DB and storage using a 2-Phase approach.
// returns
// - number of files deleted
// - disk space that was freed
// - error if any
func (s *HouseKeeper) deleteEntriesBatch(ctx context.Context, dbID repository.ULID, entries []repository.Entry) (int, uint64, error) {
	if len(entries) == 0 {
		return 0, 0, nil
	}

	// 1. Extract IDs
	ids := make([]int64, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}

	// 2. Delete the files and entries
	deletedMeta, err := shared.DeleteMultipleSafe(ctx, s.Repo, s.Storage, dbID, ids)

	// 3. Calculate disk space freed
	var freed uint64 = 0
	for _, e := range deletedMeta {
		freed += e.Filesize + e.PreviewSize
	}

	return len(deletedMeta), freed, err
}
