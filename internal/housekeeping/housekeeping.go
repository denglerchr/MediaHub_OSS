package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/storage"
)

// HouseKeeper manages both scheduled and manual housekeeping tasks.
type HouseKeeper struct {
	Repo           repository.Repository
	Storage        storage.StorageProvider
	Logger         *slog.Logger
	InstanceID     string // Unique identifier for the pod/node
	AuditRetention time.Duration
}

// NewHouseKeeper creates a new Housekeeping Service.
func NewHouseKeeper(repo repository.Repository, storage storage.StorageProvider, logger *slog.Logger, auditRetention time.Duration) *HouseKeeper {
	// Use the hostname (Pod name in K8s) as the base instance ID.
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}
	// Append startup timestamp and a random suffix to ensure uniqueness even if
	// several instances start on the same host within the same second (lock
	// ownership must never collide).
	instanceID := fmt.Sprintf("%s-%d-%s", hostname, time.Now().Unix(), shared.GenerateULID())

	return &HouseKeeper{
		Repo:           repo,
		Storage:        storage,
		Logger:         logger,
		InstanceID:     instanceID,
		AuditRetention: auditRetention,
	}
}

// StartScheduler launches a background goroutine that periodically checks all databases
// to see if their housekeeping interval has passed.
func (s *HouseKeeper) StartScheduler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute) // Check every 5 minutes

	go func() {
		for {
			select {
			case <-ctx.Done():
				s.Logger.Info("Stopping housekeeping scheduler")
				ticker.Stop()
				return
			case <-ticker.C:
				s.runGlobalTasks(ctx)
				s.runDBTasks(ctx)
			}
		}
	}()
}

// runGlobalTasks handles maintenance that is not tied to a specific media database.
func (s *HouseKeeper) runGlobalTasks(ctx context.Context) {
	lockName := "global_tasks"

	// Attempt to acquire the lock for 5 minutes
	acquired, err := s.Repo.AcquireLock(ctx, lockName, s.InstanceID, 5*time.Minute)
	if err != nil {
		s.Logger.Error("Failed to acquire lock for global tasks", "error", err)
		return
	}
	if !acquired {
		s.Logger.Debug("Global tasks are already running on another instance")
		return
	}

	// Ensure the lock is released when we finish
	defer func() {
		released, err := s.Repo.ReleaseLock(ctx, lockName, s.InstanceID)
		if err != nil {
			s.Logger.Error("Failed to release global tasks lock", "error", err)
		} else if !released {
			s.Logger.Warn("Global tasks lock was not released; lock expired or was claimed by another instance", "lock_name", lockName, "instance_id", s.InstanceID)
		}
	}()

	// Execute global maintenance

	// 1. Clean up expired refresh tokens
	deletedCount, err := s.Repo.DeleteExpiredRefreshTokens(ctx)
	if err != nil {
		s.Logger.Error("Failed to clean up expired refresh tokens", "error", err)
	} else if deletedCount > 0 {
		s.Logger.Info("Cleaned up expired refresh tokens", "deleted_count", deletedCount)
	}

	// 1b. Clean up expired API keys
	deletedKeysCount, err := s.Repo.DeleteExpiredAPIKeys(ctx)
	if err != nil {
		s.Logger.Error("Failed to clean up expired API keys", "error", err)
	} else if deletedKeysCount > 0 {
		s.Logger.Info("Cleaned up expired API keys", "deleted_count", deletedKeysCount)
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
	reqDbs, err := s.Repo.HouseKeepingRequired(ctx) //
	if err != nil {
		s.Logger.Error("Housekeeping scheduler failed to fetch required databases", "error", err)
		return
	}

	for _, db := range reqDbs {
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
	var lockName = "hk_" + db.ID.String()
	var totalDeleted int = 0
	var totalFreed uint64 = 0
	var runErr error
	var err error

	// 1. Acquire Distributed Lock (30-minute TTL as a safety net for large deletions)
	acquired, err := s.Repo.AcquireLock(ctx, lockName, s.InstanceID, 30*time.Minute)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to check lock status: %w", err)
	}
	if !acquired {
		return 0, 0, customerrors.ErrLockNotAcquired
	}

	// Ensure lock is released regardless of panics or errors
	defer func() {
		released, err := s.Repo.ReleaseLock(ctx, lockName, s.InstanceID)
		if err != nil {
			s.Logger.Error("Failed to release lock after housekeeping", "database", db.Name, "error", err)
		} else if !released {
			s.Logger.Warn("Housekeeping lock was not released; lock expired or was claimed by another instance", "database_id", db.ID, "database_name", db.Name, "instance_id", s.InstanceID)
		}
	}()

	// If MaxAge is 0, this check is disabled.
	if db.Housekeeping.MaxAge > 0 {
		maxAgeDur := db.Housekeeping.MaxAge

		// 1. Fetch the unified database time
		dbTime, err := s.Repo.GetDBTime(ctx)
		if err != nil {
			s.Logger.Error("Housekeeper failed to get DB time for MaxAge cutoff", "error", err, "database", db.Name)
			return totalDeleted, totalFreed, err // Or handle gracefully depending on your preference
		}

		// 2. Calculate cutoff using DB time and the MaxAge duration
		cutoff := dbTime.Add(-maxAgeDur)

		for {
			// We process in batches of 100 to prevent memory spikes.
			entries, err := s.Repo.GetEntries(ctx, db.ID, repository.QueryOptions{
				Limit:  100,
				Offset: 0,
				Order:  "asc",
				TEnd:   cutoff,
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

			delCount, freed, err := s.deleteEntriesBatch(ctx, db.ID, deletable)
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
	if db.Housekeeping.DiskSpace > 0 {
		// Calculate current space using the initial stats minus what we just freed, guarded against underflow
		var currentSpace uint64 = 0
		if db.Stats.TotalDiskSpaceBytes > totalFreed {
			currentSpace = db.Stats.TotalDiskSpaceBytes - totalFreed
		}
		limit := db.Housekeeping.DiskSpace

		for currentSpace > limit {
			// Fetch the absolute oldest entries in the DB, regardless of age
			entries, err := s.Repo.GetEntries(ctx, db.ID, repository.QueryOptions{
				Limit:  100,
				Offset: 0,
				Order:  "asc",
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
			var slideEnd int = 0
			var targetSpaceToFree uint64

			for i, e := range deletable {
				targetSpaceToFree += e.Size + e.PreviewSize
				slideEnd = i + 1

				// Check if this entry pushes us under the limit
				if targetSpaceToFree >= currentSpace || currentSpace-targetSpaceToFree <= limit {
					break
				}
			}

			delCount, freed, err := s.deleteEntriesBatch(ctx, db.ID, deletable[:slideEnd])
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

	// Update LastHkRun utilizing the new atomic database method to prevent stat overwrites
	_, err = s.Repo.HouseKeepingWasCalled(ctx, db.ID)
	if err != nil {
		s.Logger.Error("Housekeeper failed to update LastHkRun", "error", err, "database_id", db.ID, "database_name", db.Name)
		runErr = errors.Join(runErr, fmt.Errorf("update LastHkRun: %w", err))
	}

	s.Logger.Info("Housekeeping completed", "database_id", db.ID.String(), "database_name", db.Name, "deleted", totalDeleted, "freed_bytes", totalFreed)
	return totalDeleted, totalFreed, runErr
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
