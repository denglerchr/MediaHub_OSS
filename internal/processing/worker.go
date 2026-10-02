package processing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// StartQueueMonitor periodically checks for queued entries across all databases
// and spawns background workers if concurrency limits allow.
// It runs an initial check at startup and continues on a 15-second ticker until ctx is cancelled.
func (p *Processor) StartQueueMonitor(ctx context.Context) {
	p.Logger.Info("Starting background queue monitor to scan for queued entries...")

	// Initial scan at startup
	p.TriggerQueueWorkersIfPossible(ctx)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.Logger.Info("Stopping background queue monitor")
			return
		case <-ticker.C:
			p.TriggerQueueWorkersIfPossible(ctx)
		}
	}
}

// if slots are available, try to claim a queued entry and spawn a background worker to process it.
func (p *Processor) tryAcquireAndSpawn(ctx context.Context, dbID repo.ULID, entry repo.Entry) (bool, bool) {
	if !p.tryReserveAsyncSlot() {
		return false, false
	}

	claimed, err := p.Repo.ClaimQueuedEntry(ctx, dbID, entry.ID)
	if err != nil {
		p.Logger.Error("Failed to claim queued entry", "database_id", dbID.String(), "entry_id", entry.ID, "error", err)
		p.releaseAsyncSlotWithoutTrigger()
		return false, false
	}

	if !claimed {
		p.releaseAsyncSlotWithoutTrigger()
		return false, true // continue scanning next entry without triggering cascading dispatchers
	}

	p.Logger.Debug("Worker: Spawned background queue worker for claimed entry", "database_id", dbID.String(), "entry_id", entry.ID)
	go func() {
		defer p.releaseAsyncSlot()
		p.runWorkerForClaimedEntry(context.Background(), dbID, entry)
	}()
	return true, true
}

func (p *Processor) requeueClaimedEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) {
	entry.Status = repo.EntryStatusQueued
	if _, updateErr := p.Repo.UpdateEntry(ctx, dbID, entry); updateErr != nil {
		p.Logger.Error("Worker: CRITICAL: Failed to requeue claimed entry", "entry", entry.ID, "error", updateErr)
	} else {
		p.markEntryQueued()
	}
}

func (p *Processor) failWorkerEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) {
	_ = p.Storage.DeletePreview(ctx, dbID.String(), entry.ID)
	entry.PreviewSize = 0
	entry.Status = repo.EntryStatusError
	if _, updateErr := p.Repo.UpdateEntry(ctx, dbID, entry); updateErr != nil {
		p.Logger.Error("Worker: CRITICAL: Failed to set status error", "entry", entry.ID, "error", updateErr)
	}
}

func (p *Processor) runWorkerForClaimedEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) {
	db, err := p.Repo.GetDatabase(ctx, dbID)
	if err != nil {
		p.Logger.Error("Worker: Failed to retrieve database for claimed entry", "database_id", dbID.String(), "entry_id", entry.ID, "error", err)
		p.requeueClaimedEntry(ctx, dbID, entry)
		return
	}

	// get the file locally on disk
	tempFile, err := os.CreateTemp(os.TempDir(), "mh-worker-queued-*")
	if err != nil {
		p.Logger.Error("Worker: Failed to create temp file for queued entry", "entry", entry.ID, "error", err)
		p.requeueClaimedEntry(ctx, dbID, entry)
		return
	}
	tempFilePath := tempFile.Name()
	defer os.Remove(tempFilePath)

	stream, err := p.Storage.Read(ctx, dbID.String(), entry.ID, 0, -1)
	if err != nil {
		p.Logger.Error("Worker: Failed to read queued file from storage", "entry", entry.ID, "error", err)
		tempFile.Close()
		if errors.Is(err, customerrors.ErrNotFound) {
			p.failWorkerEntry(ctx, dbID, entry)
		} else {
			p.requeueClaimedEntry(ctx, dbID, entry)
		}
		return
	}

	_, err = io.Copy(tempFile, stream)
	stream.Close()
	tempFile.Close()

	if err != nil {
		p.Logger.Error("Worker: Failed to copy queued file to temp path", "entry", entry.ID, "error", err)
		p.requeueClaimedEntry(ctx, dbID, entry)
		return
	}

	// Handle this file
	plan := DeterminePlanForEntry(p.MediaConverter, db, entry)
	p.runConversionAndFinalize(ctx, db, entry, tempFilePath, plan)
}

// run conversion on a file on disk
func (p *Processor) runConversionAndFinalize(
	ctx context.Context,
	db repo.Database,
	entry repo.Entry,
	originalTempPath string,
	plan ProcessingPlan,
) {
	p.Logger.Debug("Worker: Starting conversion and finalize", "entry", entry.ID)

	var processErr error
	var fileSize int64 = 0

	currentPath := originalTempPath
	cleanupPaths := []string{originalTempPath}

	defer func() {
		if processErr != nil {
			p.Logger.Error("Worker: FAILED processing", "entry", entry.ID, "error", processErr)
			p.failWorkerEntry(ctx, db.ID, entry)
		}
		for _, path := range cleanupPaths {
			os.Remove(path)
		}
	}()

	if plan.WantsConversion && plan.NeedsConversion {
		if !plan.CanConvert {
			processErr = fmt.Errorf("cannot convert %v to the database mime type %v", plan.InitMimeType, db.Config.AutoConversion)
			return
		}

		convertedTempFile, err := os.CreateTemp(os.TempDir(), "mh-converted-*")
		if err != nil {
			processErr = fmt.Errorf("failed to create converted temp file: %w", err)
			return
		}
		convertedTempPath := convertedTempFile.Name()
		convertedTempFile.Close()
		cleanupPaths = append(cleanupPaths, convertedTempPath)

		err = p.MediaConverter.ConvertFile(ctx, currentPath, convertedTempPath, plan.InitMimeType, media.ConversionOptions{
			TargetMimeType: plan.TargetMimeType,
		})
		if err != nil {
			processErr = fmt.Errorf("conversion to file failed: %w", err)
			return
		}

		currentPath = convertedTempPath
	}

	if mf, err := media.GetMetadataFields(db.ContentType); err == nil && len(mf) > 0 {
		if extractedMeta, err := p.MediaConverter.ReadMediaFieldsFromFile(ctx, currentPath, db.ContentType); err == nil {
			entry.MediaFields = extractedMeta
		} else {
			p.Logger.Warn("Worker: Failed to extract metadata", "entry", entry.ID, "error", err)
		}
	}

	if plan.WantsPreview && plan.CanGenPreview {
		if previewSize, err := p.generateAndStorePreviewFromFile(ctx, db, entry.ID, currentPath, plan.ResultMimeType); err != nil {
			p.Logger.Error("Worker: Failed to generate or store preview", "entry", entry.ID, "error", err)
		} else {
			entry.PreviewSize = previewSize
		}
	}

	finalFile, err := os.Open(currentPath)
	if err != nil {
		processErr = fmt.Errorf("failed to open final file for storage: %w", err)
		return
	}

	fileSize, err = p.Storage.Write(ctx, db.ID.String(), entry.ID, finalFile)
	finalFile.Close()

	if err != nil {
		processErr = fmt.Errorf("failed to stream file to storage: %w", err)
		return
	}

	entry.Status = repo.EntryStatusReady
	entry.Size = uint64(fileSize)
	entry.MimeType = plan.ResultMimeType
	entry.FileName = plan.FinalFileName

	if _, err := p.Repo.UpdateEntry(ctx, db.ID, entry); err != nil {
		processErr = fmt.Errorf("failed to update final database stats: %w", err)
		return
	}

	p.Logger.Info("Worker: Successfully processed large entry", "entry", entry.ID)
}

// TriggerQueueWorkersIfPossible checks for queued entries across all databases,
// synchronizes the processor's in-memory hasQueuedEntries flag, and spawns background
// workers in parallel up to available conversion limits (n_ffmpeg_async and n_ffmpeg_total).
// It uses isScanningQueue to prevent multiple concurrent scan loops and stampedes.
func (p *Processor) TriggerQueueWorkersIfPossible(ctx context.Context) {
	if !p.isScanningQueue.CompareAndSwap(false, true) {
		return
	}
	defer p.isScanningQueue.Store(false)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		dbIDs, err := p.Repo.GetDatabaseULIDsWithQueuedEntries(ctx)
		if err != nil {
			p.Logger.Error("TriggerQueueWorkers: Failed to query database ULIDs with queued entries", "error", err)
			return
		}

		hasEntries := len(dbIDs) > 0
		p.mu.Lock()
		p.hasQueuedEntries = hasEntries
		freeAsync := p.NFfmpegAsync - p.activeAsync
		freeTotal := p.NFfmpegTotal - p.activeTotal
		p.mu.Unlock()

		if !hasEntries || freeAsync <= 0 || freeTotal <= 0 {
			return
		}

		spawnedCount := 0
		limit := freeAsync
		if freeTotal < limit {
			limit = freeTotal
		}

		for _, dbID := range dbIDs {
			if limit <= 0 {
				break
			}

			entries, err := p.Repo.GetEntriesByStatus(ctx, dbID, repo.EntryStatusQueued, uint64(limit))
			if err != nil {
				p.Logger.Error("TriggerQueueWorkers: Failed to get queued entries", "database_id", dbID.String(), "error", err)
				continue
			}

			for _, entry := range entries {
				claimed, ok := p.tryAcquireAndSpawn(ctx, dbID, entry)
				if !ok {
					// Concurrency limit reached or fatal error
					return
				}
				if claimed {
					spawnedCount++
					limit--
					if limit <= 0 {
						break
					}
				}
			}
		}

		// If no workers could be spawned in this pass, exit to avoid busy looping
		if spawnedCount == 0 || limit <= 0 {
			return
		}
	}
}
