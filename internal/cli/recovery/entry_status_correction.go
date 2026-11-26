package recovery

import (
	"context"
	"errors"
	"fmt"

	"mediahub_oss/internal/media"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
)

// asFloat64 converts integer and floating-point metadata values to float64.
func asFloat64(val any) (float64, bool) {
	switch v := val.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}

// isMediaMetaMissing checks if required media fields for the content type were never extracted or have placeholder values.
func isMediaMetaMissing(contentType string, mediaFields map[string]any) bool {
	fields, err := media.GetMetadataFields(contentType)
	if err != nil || len(fields) == 0 {
		return false
	}
	if len(mediaFields) == 0 {
		return true
	}
	for _, f := range fields {
		val, exists := mediaFields[f.Name]
		if !exists || val == nil {
			return true
		}
		num, ok := asFloat64(val)
		if !ok {
			continue
		}
		switch f.Type {
		case "uint64", "uint8":
			if num <= 0 {
				return true
			}
		case "int64", "float64":
			if num < 0 {
				return true
			}
		}
	}
	return false
}

// EntryStatusCorrection scans for entries stuck in 'processing' or 'deleting' and rectifies their state.
func (s *RecoveryService) EntryStatusCorrection(ctx context.Context) error {
	databases, err := s.repo.GetDatabases(ctx)
	if err != nil {
		return fmt.Errorf("could not fetch databases: %w", err)
	}

	for _, db := range databases {
		if err := s.correctDatabaseEntryStatuses(ctx, db); err != nil {
			return err
		}
	}

	return nil
}

type processingAction int

const (
	processingSkip processingAction = iota
	processingMarkReady
	processingRequeue
	processingDeleteZombie
)

func (s *RecoveryService) inspectProcessingEntry(ctx context.Context, db repository.Database, entry repository.Entry) (repository.Entry, processingAction) {
	stat, err := s.storage.Stat(ctx, db.ID, entry.ID)
	if errors.Is(err, customerrors.ErrNotFound) {
		return entry, processingDeleteZombie
	}
	if err != nil {
		fmt.Println() // Break the \r progress line so the log prints cleanly
		s.logger.Error("Storage stat failed", "database_id", db.ID.String(), "database_name", db.Name, "id", entry.ID, "error", err)
		return entry, processingSkip
	}

	// File exists in storage! Reconcile file size
	entry.Size = uint64(stat.Size)

	needsConversion := db.Config.AutoConversion != "" &&
		media.NormalizeMimeType(entry.MimeType) != media.NormalizeMimeType(db.Config.AutoConversion)

	needsPreview := false
	if db.Config.CreatePreview && (db.ContentType == "image" || db.ContentType == "video") {
		pStat, pErr := s.storage.StatPreview(ctx, db.ID, entry.ID)
		if pErr == nil {
			entry.PreviewSize = uint64(pStat.Size)
		} else if errors.Is(pErr, customerrors.ErrNotFound) {
			needsPreview = true
			entry.PreviewSize = 0
		} else {
			fmt.Println() // Break the \r progress line so the log prints cleanly
			s.logger.Error("Storage preview stat failed", "database_id", db.ID.String(), "database_name", db.Name, "id", entry.ID, "error", pErr)
			return entry, processingSkip
		}
	}

	if needsConversion || needsPreview || isMediaMetaMissing(db.ContentType, entry.MediaFields) {
		entry.Status = repository.EntryStatusQueued
		return entry, processingRequeue
	}

	entry.Status = repository.EntryStatusReady
	return entry, processingMarkReady
}

func (s *RecoveryService) correctDatabaseEntryStatuses(ctx context.Context, db repository.Database) error {
	fmt.Printf("Database %q\n", db.Name)

	stats, err := s.repo.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		s.logger.Error("Failed to get database stats", "database_id", db.ID.String(), "database_name", db.Name, "error", err)
		return nil
	}

	var markReadyEntries []repository.Entry
	var requeueEntries []repository.Entry
	var deleteZombiesIDs []int64
	var deleteStuckIDs []int64

	expectedEntries := stats.EntryCount
	if expectedEntries == 0 {
		expectedEntries = 1
	}

	limit := 1000
	processed := uint64(0)

	// --- PHASE 1: Scanning ---
	for offset := 0; true; offset += limit {
		entries, err := s.repo.GetEntries(ctx, db.ID, repository.QueryOptions{
			Limit:  limit,
			Offset: offset,
			Order:  "asc",
			SortBy: "id",
		})
		if err != nil {
			return fmt.Errorf("failed to fetch entries for %s: %w", db.Name, err)
		}

		if len(entries) == 0 {
			break
		}

		for _, entry := range entries {
			processed++

			// Update console with carriage return (\r) to overwrite the line
			percent := (processed * 100) / expectedEntries
			if percent > 99 {
				percent = 99
			}
			fmt.Printf("\r- Step 1: Entry Status Corrections: %d%% (Scanning...)", percent)

			if entry.Status == repository.EntryStatusProcessing {
				updated, action := s.inspectProcessingEntry(ctx, db, entry)
				switch action {
				case processingDeleteZombie:
					deleteZombiesIDs = append(deleteZombiesIDs, entry.ID)
				case processingRequeue:
					requeueEntries = append(requeueEntries, updated)
				case processingMarkReady:
					markReadyEntries = append(markReadyEntries, updated)
				case processingSkip:
				}
			} else if entry.Status == repository.EntryStatusDeleting {
				// Entry stuck deleting -> mark for full cleanup
				deleteStuckIDs = append(deleteStuckIDs, entry.ID)
			}
		}
	}

	// Overwrite the scanning line with the final completion state
	fmt.Printf("\r- Step 1: Entry Status Corrections: 100%% (Applying fixes)    \n")

	// --- PHASE 2: Action & Summarize ---
	var actionErrs []error
	if !s.dryRun {
		// 1. Mark valid processing files as ready (and reconcile sizes/stats)
		for _, entry := range markReadyEntries {
			if _, err := s.repo.UpdateEntry(ctx, db.ID, entry); err != nil {
				s.logger.Error("Failed to update entry to ready", "database_id", db.ID.String(), "id", entry.ID, "error", err)
				actionErrs = append(actionErrs, fmt.Errorf("mark ready entry %d (%s): %w", entry.ID, db.Name, err))
			}
		}

		// 2. Requeue entries needing conversion, preview generation, or metadata extraction
		for _, entry := range requeueEntries {
			if _, err := s.repo.UpdateEntry(ctx, db.ID, entry); err != nil {
				s.logger.Error("Failed to requeue entry", "database_id", db.ID.String(), "id", entry.ID, "error", err)
				actionErrs = append(actionErrs, fmt.Errorf("requeue entry %d (%s): %w", entry.ID, db.Name, err))
			}
		}

		// 3. Delete entries where the file was never uploaded (Zombies) in bounded batches
		for _, batch := range chunkIDs(deleteZombiesIDs, recoveryDeleteBatchSize) {
			if _, err := s.repo.DeleteEntries(ctx, db.ID, batch); err != nil {
				s.logger.Error("Failed to delete zombie entries", "database_id", db.ID.String(), "database_name", db.Name, "batch_size", len(batch), "error", err)
				actionErrs = append(actionErrs, fmt.Errorf("delete zombie entries (%s): %w", db.Name, err))
			}
		}

		// 4. Fix stuck deleting in bounded batches (Attempt storage cleanup, then remove DB entry)
		for _, batch := range chunkIDs(deleteStuckIDs, recoveryDeleteBatchSize) {
			if _, err := shared.DeleteMultipleSafe(ctx, s.repo, s.storage, db.ID, batch); err != nil {
				s.logger.Error("Failed to delete stuck deleting entries", "database_id", db.ID.String(), "database_name", db.Name, "batch_size", len(batch), "error", err)
				actionErrs = append(actionErrs, fmt.Errorf("delete stuck entries (%s): %w", db.Name, err))
			}
		}
	}

	// Print brief summary for this step as requested
	if len(requeueEntries) > 0 {
		fmt.Printf("\tSummary: %d marked ready, %d requeued, %d entries without files removed from DB, %d stuck in deleting state removed.\n",
			len(markReadyEntries),
			len(requeueEntries),
			len(deleteZombiesIDs),
			len(deleteStuckIDs),
		)
	} else {
		fmt.Printf("\tSummary: %d marked ready, %d entries without files removed from DB, %d stuck in deleting state removed.\n",
			len(markReadyEntries),
			len(deleteZombiesIDs),
			len(deleteStuckIDs),
		)
	}

	return errors.Join(actionErrs...)
}
