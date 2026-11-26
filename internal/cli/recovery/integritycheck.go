package recovery

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/storage"
)

// IntegrityCheck ensures that database records and physical files match.
func (s *RecoveryService) IntegrityCheck(ctx context.Context) error {
	databases, err := s.repo.GetDatabases(ctx)
	if err != nil {
		return fmt.Errorf("could not fetch databases: %w", err)
	}

	var allErrs []error
	for _, db := range databases {
		fmt.Printf("Database %q\n", db.Name)
		stats := db.Stats

		// --- PHASE 2.1: DB -> Storage (Find missing files) ---
		missingFileIDs, entryCount, existingIDs, err := s.checkMissingFiles(ctx, db)
		if err != nil {
			return err
		}

		// --- PHASE 2.2: Storage -> DB (Find orphan main files & calculate sizes) ---
		var calculatedTotalBytes uint64
		orphanFileIDs, calculatedTotalBytes, err := s.checkOrphanFiles(ctx, db, stats, existingIDs)
		if err != nil {
			fmt.Println() // Break the \r progress line
			s.logger.Error("Failed walking main storage", "database_id", db.ID.String(), "database_name", db.Name, "error", err)
			return err // Abort on error to prevent saving corrupted stats
		}

		// --- PHASE 2.3: Storage -> DB (Find orphan preview files) ---
		orphanPreviewIDs, calculatedPreviewBytes, err := s.checkOrphanPreviewFiles(ctx, db, stats, existingIDs)
		calculatedTotalBytes += calculatedPreviewBytes
		if err != nil {
			fmt.Println() // Break the \r progress line
			s.logger.Error("Failed walking preview storage", "database_id", db.ID.String(), "database_name", db.Name, "error", err)
			return err // Abort on error to prevent saving corrupted stats
		}

		// --- PHASE 2.4: entry DB --> database DB (verify entry count stat)
		if entryCount != db.Stats.EntryCount {
			fmt.Printf("Found discrepance in entry count. Stats: %v, Database: %v\n", db.Stats.EntryCount, entryCount)
		}

		actualQueued, err := s.repo.CountEntriesByStatus(ctx, db.ID, repository.EntryStatusQueued)
		if err != nil {
			s.logger.Error("Failed to count queued entries", "database_id", db.ID.String(), "database_name", db.Name, "error", err)
		} else if uint64(actualQueued) != db.Stats.QueuedCount {
			fmt.Printf("Found discrepancy in queued count. Stats: %v, Database: %v\n", db.Stats.QueuedCount, actualQueued)
		}

		fmt.Printf("\r- Step 2: Integrity check: 100%% (Applying DB<->Disk fixes)            \n")

		// --- PHASE 2.5: Action ---
		if !s.dryRun {
			if err := s.applyIntegrityFixes(ctx, db, entryCount, calculatedTotalBytes, missingFileIDs, orphanFileIDs, orphanPreviewIDs); err != nil {
				allErrs = append(allErrs, err)
			}
		}

		// Print the final summary, including stats differences
		fmt.Printf("\tSummary: %d missing files removed from DB, %d orphan media files and %d orphan previews removed from disk.\n",
			len(missingFileIDs), len(orphanFileIDs), len(orphanPreviewIDs))
	}

	return errors.Join(allErrs...)
}

func (s *RecoveryService) applyIntegrityFixes(
	ctx context.Context,
	db repository.Database,
	entryCount uint64,
	calculatedTotalBytes uint64,
	missingFileIDs []int64,
	orphanFileIDs []int64,
	orphanPreviewIDs []int64,
) error {
	var actionErrs []error
	var deletedDBCount uint64
	hasDBDeleteErr := false

	// Delete the database entries for files that no longer exist on disk in bounded batches.
	for _, batch := range chunkIDs(missingFileIDs, recoveryDeleteBatchSize) {
		if _, err := s.storage.DeleteMultiplePreviews(ctx, db.ID, batch); err != nil {
			s.logger.Warn("Failed to delete previews for missing files", "database_id", db.ID.String(), "database_name", db.Name, "batch_size", len(batch), "error", err)
			actionErrs = append(actionErrs, fmt.Errorf("delete missing file previews (%s): %w", db.Name, err))
		}
		deletedMeta, err := s.repo.DeleteEntries(ctx, db.ID, batch)
		deletedDBCount += uint64(len(deletedMeta))
		if err != nil {
			hasDBDeleteErr = true
			s.logger.Error("Failed to delete missing file entries from DB", "database_id", db.ID.String(), "database_name", db.Name, "batch_size", len(batch), "error", err)
			actionErrs = append(actionErrs, fmt.Errorf("delete missing entries (%s): %w", db.Name, err))
		}
	}

	// Delete physical files that have no database record in bounded batches.
	for _, batch := range chunkIDs(orphanFileIDs, recoveryDeleteBatchSize) {
		if _, err := s.storage.DeleteMultiple(ctx, db.ID, batch); err != nil {
			s.logger.Error("Failed to delete orphan main files", "database_id", db.ID.String(), "database_name", db.Name, "batch_size", len(batch), "error", err)
			actionErrs = append(actionErrs, fmt.Errorf("delete orphan files (%s): %w", db.Name, err))
		}
	}

	// Delete physical preview files that have no database record in bounded batches.
	for _, batch := range chunkIDs(orphanPreviewIDs, recoveryDeleteBatchSize) {
		if _, err := s.storage.DeleteMultiplePreviews(ctx, db.ID, batch); err != nil {
			s.logger.Error("Failed to delete orphan preview files", "database_id", db.ID.String(), "database_name", db.Name, "batch_size", len(batch), "error", err)
			actionErrs = append(actionErrs, fmt.Errorf("delete orphan previews (%s): %w", db.Name, err))
		}
	}

	if hasDBDeleteErr {
		s.logger.Warn("Skipping database stats reconciliation because entry deletion failed", "database_id", db.ID.String(), "database_name", db.Name)
		return errors.Join(actionErrs...)
	}

	// --- PHASE 2.5: Sync Statistics ---
	var trueEntryCount uint64
	if entryCount > deletedDBCount {
		trueEntryCount = entryCount - deletedDBCount
	}

	db.Stats.EntryCount = trueEntryCount
	db.Stats.TotalDiskSpaceBytes = calculatedTotalBytes

	// Recalculate true queued count after any missing file deletions
	if finalQueued, err := s.repo.CountEntriesByStatus(ctx, db.ID, repository.EntryStatusQueued); err != nil {
		s.logger.Error("Failed to count queued entries after cleanup", "database_id", db.ID.String(), "database_name", db.Name, "error", err)
		actionErrs = append(actionErrs, fmt.Errorf("count queued entries (%s): %w", db.Name, err))
	} else {
		db.Stats.QueuedCount = uint64(finalQueued)
	}

	// Save it back (dedicated stats reconciliation; UpdateDatabase does
	// not write statistics columns)
	if err := s.repo.UpdateDatabaseStats(ctx, db.ID, db.Stats); err != nil {
		s.logger.Error("Failed to sync database stats", "database_id", db.ID.String(), "database_name", db.Name, "error", err)
		actionErrs = append(actionErrs, fmt.Errorf("update database stats (%s): %w", db.Name, err))
	}

	return errors.Join(actionErrs...)
}

// Loop over entries and check if a file exists in the storage. Return entry IDs without a file, total entry count, and the sorted slice of all existing entry IDs in the database.
func (s *RecoveryService) checkMissingFiles(ctx context.Context, db repository.Database) ([]int64, uint64, []int64, error) {

	var missingFileIDs []int64
	var totalEntries uint64 = 0
	var expectedEntryCount = db.Stats.EntryCount

	// avoid division by 0
	if expectedEntryCount == 0 {
		expectedEntryCount = 1
	}

	existingIDs := make([]int64, 0, expectedEntryCount)
	limit := 1000
	processedDB := uint64(0)

	// DB -> Storage (Find missing files)
	for offset := 0; true; offset += limit {
		entries, err := s.repo.GetEntries(ctx, db.ID, repository.QueryOptions{
			Limit:  limit,
			Offset: offset,
			Order:  "asc",
			SortBy: "id",
		})
		if err != nil {
			return missingFileIDs, totalEntries, nil, fmt.Errorf("failed to fetch entries for %s: %w", db.Name, err)
		}
		totalEntries += uint64(len(entries))

		// stop if no more entries returned (offset too large)
		if len(entries) == 0 {
			break
		}

		for _, entry := range entries {
			processedDB++
			percent := (processedDB * 100) / expectedEntryCount
			fmt.Printf("\r- Step 2: Integrity check: %d%% (Scanning DB->Disk...)", percent)

			isMissing := false
			// Only verify "ready" files, as Step 1 already handled "processing" and "deleting"
			if entry.Status == repository.EntryStatusReady {
				info, err := s.storage.Stat(ctx, db.ID, entry.ID)

				if errors.Is(err, customerrors.ErrNotFound) {
					missingFileIDs = append(missingFileIDs, entry.ID)
					isMissing = true
				} else if err != nil {
					fmt.Println() // Break the \r progress line
					s.logger.Error("Storage stat failed", "database_id", db.ID.String(), "database_name", db.Name, "id", entry.ID, "error", err)
				} else {
					// File exists! Cross-check the recorded size against the actual physical size
					if entry.Size != uint64(info.Size) {
						fmt.Println() // Break the \r progress line
						s.logger.Warn("File size mismatch detected (Possible corruption)",
							"database_id", db.ID.String(),
							"database_name", db.Name,
							"id", entry.ID,
							"repo_size_bytes", entry.Size,
							"storage_size_bytes", info.Size,
						)
					}
				}
			}

			if !isMissing {
				existingIDs = append(existingIDs, entry.ID)
			}
		}
	}

	// Ensure IDs are strictly sorted in case the database returned entries out of order.
	// slices.Sort uses Pattern-Defeating Quicksort (pdqsort), which completes in O(N) linear time on already-sorted data.
	if !slices.IsSorted(existingIDs) {
		slices.Sort(existingIDs)
	}

	return missingFileIDs, totalEntries, existingIDs, nil
}

// walk over files in the storage and check if a database entry exists. Return file ids without entry,
// the accumulated file sizes and possible errors.
func (s *RecoveryService) checkOrphanFiles(ctx context.Context, db repository.Database, stats repository.DatabaseStats, existingIDs []int64) ([]int64, uint64, error) {
	var orphanFileIDs []int64
	var calculatedTotalBytes uint64 = 0

	// number of entries for calculating progress
	divisor := stats.EntryCount
	if divisor == 0 {
		divisor = 1
	}

	processedStorage := uint64(0)
	err := s.storage.Walk(ctx, db.ID, func(id int64, info storage.FileInfo) error {
		processedStorage++
		percent := (processedStorage * 100) / divisor
		if percent > 99 {
			percent = 99
		} // Cap at 99% until finished
		fmt.Printf("\r- Step 2: Integrity check: %d%% (Scanning Disk->DB Main)...", percent)

		// Check if the file exists in the database using binary search on ordered IDs
		if _, found := slices.BinarySearch(existingIDs, id); !found {
			orphanFileIDs = append(orphanFileIDs, id)
		} else {
			// Valid file! Add to our true physical size calculation
			calculatedTotalBytes += uint64(info.Size)
		}
		return nil
	})
	return orphanFileIDs, calculatedTotalBytes, err
}

func (s *RecoveryService) checkOrphanPreviewFiles(ctx context.Context, db repository.Database, stats repository.DatabaseStats, existingIDs []int64) ([]int64, uint64, error) {
	var orphanFileIDs []int64
	var calculatedTotalBytes uint64 = 0

	// number of entries for calculating progress
	divisor := stats.EntryCount
	if divisor == 0 {
		divisor = 1
	}

	processedStorage := uint64(0)
	err := s.storage.WalkPreview(ctx, db.ID, func(id int64, info storage.FileInfo) error {
		processedStorage++
		percent := (processedStorage * 100) / divisor
		if percent > 99 {
			percent = 99
		} // Cap at 99% until finished
		fmt.Printf("\r- Step 2: Integrity check: %d%% (Scanning Disk->DB Previews)...", percent)

		// Check if the file exists in the database using binary search on ordered IDs
		if _, found := slices.BinarySearch(existingIDs, id); !found {
			orphanFileIDs = append(orphanFileIDs, id)
		} else {
			// Valid file! Add to our true physical size calculation
			calculatedTotalBytes += uint64(info.Size)
		}
		return nil
	})
	return orphanFileIDs, calculatedTotalBytes, err
}
