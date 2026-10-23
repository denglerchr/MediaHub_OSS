package processing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// ProcessIncomingEntry is the entry point for newly uploaded files ("incoming" pipeline).
//
// The upload is routed based on the processing plan and the upload's spool location:
//
//   - Small files (held in memory by the HTTP layer) are processed synchronously
//     and return synchronous=true. Conversion or preview generation occupies a
//     sync slot (held until background preview generation completes if configured).
//   - Large files (spooled to disk by the HTTP layer, i.e. file is an *os.File)
//     are processed by a background worker occupying an async slot and return
//     synchronous=false with an entry in "processing" status.
//   - If neither conversion nor preview generation is required, the file is stored
//     as-is without occupying a processing slot.
//   - If the applicable concurrency limit is reached, the file is queued instead
//     (entry status "queued") as long as the database's NMaxQueued limit allows it.
//
// Returns customerrors.ErrBadMimeType for files not matching the database content
// type and customerrors.ErrUnavailable when all slots are occupied and the queue
// is full.
func (p *Processor) ProcessIncomingEntry(
	ctx context.Context,
	db repo.Database,
	req EntryRequest,
	file io.ReadSeeker,
	originalMimeType string,
	originalFileName string,
) (entry repo.Entry, synchronous bool, err error) {
	plan, err := DetermineConversionPlan(p.MediaConverter, db, originalMimeType, originalFileName, req.FileName)
	if err != nil {
		return repo.Entry{}, false, err
	}

	// The HTTP layer spools uploads larger than max_sync_upload_size to disk;
	// those arrive as *os.File and are processed asynchronously.
	diskFile, isOnDisk := file.(*os.File)

	if isOnDisk {
		entry, err = p.processAsynchronously(ctx, diskFile, db, req, plan)
	} else {
		entry, err = p.processSynchronously(ctx, file, db, req, plan)
	}

	if err == nil {
		return entry, !isOnDisk, nil
	}
	if !errors.Is(err, customerrors.ErrResourceExhausted) {
		return repo.Entry{}, false, err
	}

	// Conversion capacity exhausted: fall back to queueing the file.
	queuedEntry, err := p.queueIncomingFile(ctx, file, db, req, plan)
	if err != nil {
		return repo.Entry{}, false, err
	}
	return queuedEntry, false, nil
}

// processSynchronously converts (if required) and stores an in-memory upload
// within the request scope. A sync processing slot is held while converting or
// generating a preview; if a preview is configured, it is generated in the
// background afterwards and releases the slot upon completion.
func (p *Processor) processSynchronously(
	ctx context.Context,
	file io.ReadSeeker,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	if plan.NeedsConversion && !plan.CanConvert {
		return repo.Entry{}, fmt.Errorf("cannot convert %v to the database mime type %v", plan.InitMimeType, db.Config.AutoConversion)
	}

	needsSlot := plan.NeedsSlot()
	if needsSlot && !p.tryReserveSyncSlot() {
		return repo.Entry{}, customerrors.ErrResourceExhausted
	}
	slotHandedOff := false
	if needsSlot {
		defer func() {
			if !slotHandedOff {
				p.releaseSyncSlot()
			}
		}()
	}

	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, true)
	if err != nil {
		return repo.Entry{}, err
	}

	streamToStore := file
	var tempStream *TempFileStream
	if plan.NeedsConversion {
		tempStream, _, err = p.convertToTempFile(ctx, file, plan.InitMimeType, media.ConversionOptions{
			TargetMimeType: plan.ResultMimeType,
		})
		if err != nil {
			p.failUploadEntry(ctx, db, createdEntry, err)
			return repo.Entry{}, err
		}
		streamToStore = tempStream
	}

	// On success the optional background preview takes over tempStream ownership;
	// otherwise it is closed here.
	keepTempStream := false
	defer func() {
		if !keepTempStream && tempStream != nil {
			tempStream.Close()
		}
	}()

	// Extract media metadata if the database content type defines fields.
	// A converted file is probed from disk, the original stream via loopback.
	if mf, err := media.GetMetadataFields(db.ContentType); err == nil && len(mf) > 0 {
		var meta map[string]any
		var metaErr error
		if tempStream != nil {
			meta, metaErr = p.MediaConverter.ReadMediaFieldsFromFile(ctx, tempStream.Name(), db.ContentType)
		} else {
			if _, err := streamToStore.Seek(0, io.SeekStart); err != nil {
				p.failUploadEntry(ctx, db, createdEntry, err)
				return repo.Entry{}, fmt.Errorf("failed to seek file for probing: %w", err)
			}
			meta, metaErr = p.MediaConverter.ReadMediaFieldsFromStream(ctx, streamToStore, db.ContentType)
		}
		if metaErr != nil {
			p.Logger.Warn("Could not extract metadata from file", "entry", createdEntry.ID, "error", metaErr)
		} else {
			createdEntry.MediaFields = meta
		}
	}

	if err := p.storeProcessedStream(ctx, db, &createdEntry, streamToStore); err != nil {
		return repo.Entry{}, err
	}

	// Hand the stored file off to background preview generation if configured.
	if plan.WantsPreview && plan.CanGenPreview {
		createdEntry.Status = repo.EntryStatusProcessing
		processingEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
		if err != nil {
			p.failUploadEntry(ctx, db, createdEntry, err)
			return repo.Entry{}, fmt.Errorf("failed to finalize entry metadata: %w", err)
		}

		keepTempStream = true // owned by the preview goroutine
		slotHandedOff = true  // released by the preview goroutine
		go p.generatePreviewInBackground(db, processingEntry, tempStream, plan.ResultMimeType)
		return processingEntry, nil
	}

	return p.markEntryReady(ctx, db, createdEntry)
}

// processAsynchronously hands a disk-spooled upload over to a background worker.
// An async processing slot is reserved when conversion or preview generation is
// required. The returned entry is in "processing" status; clients poll until it
// transitions to "ready".
func (p *Processor) processAsynchronously(
	ctx context.Context,
	file *os.File,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	if plan.NeedsConversion && !plan.CanConvert {
		return repo.Entry{}, fmt.Errorf("cannot convert %v to the database mime type %v", plan.InitMimeType, db.Config.AutoConversion)
	}

	needsSlot := plan.NeedsSlot()
	if needsSlot && !p.tryReserveAsyncSlot() {
		return repo.Entry{}, customerrors.ErrResourceExhausted
	}

	workerTempPath, err := claimTempFile(file)
	if err != nil {
		if needsSlot {
			p.unreserveAsyncSlot()
		}
		return repo.Entry{}, err
	}

	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, false)
	if err != nil {
		if needsSlot {
			p.unreserveAsyncSlot()
		}
		os.Remove(workerTempPath)
		return repo.Entry{}, err
	}

	go func() {
		if needsSlot {
			defer p.releaseAsyncSlot()
		}
		p.finalizeEntryFile(context.Background(), db, createdEntry, workerTempPath, plan)
	}()

	return createdEntry, nil
}

// queueIncomingFile stores the uploaded file as-is and records the entry as queued.
// It returns customerrors.ErrUnavailable if the database's NMaxQueued limit is reached.
//
// The entry is created in "processing" status first and only switched to "queued"
// after the file is fully written to storage. This prevents queue workers on any
// node from claiming an entry whose file is not readable yet.
func (p *Processor) queueIncomingFile(
	ctx context.Context,
	file io.ReadSeeker,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	if plan.NeedsConversion && !plan.CanConvert {
		return repo.Entry{}, fmt.Errorf("cannot convert %v to the database mime type %v", plan.InitMimeType, db.Config.AutoConversion)
	}

	if db.NMaxQueued <= 0 {
		p.Logger.Warn("Upload rejected: conversion capacity exhausted and queue is full",
			"database_id", db.ID.String(), "queued_count", 0, "max_queued", db.NMaxQueued)
		return repo.Entry{}, customerrors.ErrUnavailable
	}

	queuedCount, err := p.Repo.CountEntriesByStatus(ctx, db.ID, repo.EntryStatusQueued)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to count queued entries: %w", err)
	}
	if int(queuedCount) >= db.NMaxQueued {
		p.Logger.Warn("Upload rejected: conversion capacity exhausted and queue is full",
			"database_id", db.ID.String(), "queued_count", queuedCount, "max_queued", db.NMaxQueued)
		return repo.Entry{}, customerrors.ErrUnavailable
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return repo.Entry{}, fmt.Errorf("failed to seek file: %w", err)
	}

	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, false)
	if err != nil {
		return repo.Entry{}, err
	}

	// On failure remove the entry again; fall back to marking it as erroneous so
	// no entry is ever left in a phantom "processing" state.
	discardEntry := func() {
		if _, delErr := p.Repo.DeleteEntry(ctx, db.ID, createdEntry.ID); delErr != nil {
			_ = p.Repo.UpdateEntriesStatus(ctx, db.ID, []int64{createdEntry.ID}, repo.EntryStatusError)
		}
	}

	fileSize, err := p.Storage.Write(ctx, db.ID.String(), createdEntry.ID, file)
	if err != nil {
		_ = p.Storage.Delete(ctx, db.ID.String(), createdEntry.ID)
		discardEntry()
		return repo.Entry{}, fmt.Errorf("failed to write file to storage: %w", err)
	}

	createdEntry.Size = uint64(fileSize)
	queuedEntry, err := p.Repo.EnqueueEntry(ctx, db.ID, createdEntry, db.NMaxQueued)
	if err != nil {
		_ = p.Storage.Delete(ctx, db.ID.String(), createdEntry.ID)
		discardEntry()
		if errors.Is(err, customerrors.ErrUnavailable) {
			p.Logger.Warn("Upload rejected: conversion capacity exhausted and queue is full",
				"database_id", db.ID.String(), "max_queued", db.NMaxQueued)
			return repo.Entry{}, customerrors.ErrUnavailable
		}
		return repo.Entry{}, fmt.Errorf("failed to mark entry as queued: %w", err)
	}

	p.Logger.Debug("Queued file for background processing", "database_id", db.ID.String(), "entry_id", queuedEntry.ID, "filename", queuedEntry.FileName)
	p.noteQueuedEntry()
	return queuedEntry, nil
}

// generatePreviewInBackground generates and stores the preview for a synchronously
// stored entry, then marks the entry ready and releases the reserved sync slot.
// If tempStream is non-nil (converted file on disk), it is used directly and closed
// on completion; otherwise the stored original is streamed back from storage.
func (p *Processor) generatePreviewInBackground(db repo.Database, entry repo.Entry, tempStream *TempFileStream, mimeType string) {
	defer p.releaseSyncSlot()

	ctx := context.Background()
	var previewSize uint64
	var err error

	if tempStream != nil {
		defer tempStream.Close()
		previewSize, err = p.generateAndStorePreview(ctx, db, entry.ID, mimeType, func(ctx context.Context, w io.Writer) error {
			return p.MediaConverter.CreatePreviewFromFile(ctx, tempStream.Name(), w, mimeType)
		})
	} else {
		storedStream, readErr := p.Storage.Read(ctx, db.ID.String(), entry.ID, 0, -1)
		if readErr != nil {
			err = fmt.Errorf("failed to read stored file for preview: %w", readErr)
		} else {
			defer storedStream.Close()
			previewSize, err = p.generateAndStorePreview(ctx, db, entry.ID, mimeType, func(ctx context.Context, w io.Writer) error {
				if rs, ok := storedStream.(io.ReadSeeker); ok {
					return p.MediaConverter.CreatePreviewFromStream(ctx, rs, w, mimeType)
				}

				spoolFile, spoolErr := os.CreateTemp("", "mh-previewspool-*")
				if spoolErr != nil {
					return fmt.Errorf("failed to create preview spool file: %w", spoolErr)
				}
				spoolPath := spoolFile.Name()
				defer func() {
					spoolFile.Close()
					os.Remove(spoolPath)
				}()
				if _, copyErr := io.Copy(spoolFile, storedStream); copyErr != nil {
					return fmt.Errorf("failed to spool stored file for preview: %w", copyErr)
				}
				if closeErr := spoolFile.Close(); closeErr != nil {
					return fmt.Errorf("failed to close preview spool file: %w", closeErr)
				}
				return p.MediaConverter.CreatePreviewFromFile(ctx, spoolPath, w, mimeType)
			})
		}
	}
	if err != nil {
		p.Logger.Error("Background preview generation failed", "entry", entry.ID, "error", err)
	}

	entry.Status = repo.EntryStatusReady
	entry.PreviewSize = previewSize
	if _, err := p.Repo.UpdateEntry(ctx, db.ID, entry); err != nil {
		p.Logger.Error("Failed to update status to ready after background preview", "entry", entry.ID, "error", err)
	}
}

// markEntryReady finalizes a stored entry as ready.
func (p *Processor) markEntryReady(ctx context.Context, db repo.Database, entry repo.Entry) (repo.Entry, error) {
	entry.Status = repo.EntryStatusReady
	finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, entry)
	if err != nil {
		p.failUploadEntry(ctx, db, entry, err)
		return repo.Entry{}, fmt.Errorf("failed to finalize entry metadata: %w", err)
	}
	return finalEntry, nil
}

// failUploadEntry marks an entry of the incoming pipeline as erroneous and removes
// any partially stored file. The entry itself is kept for transparency.
func (p *Processor) failUploadEntry(ctx context.Context, db repo.Database, entry repo.Entry, uploadErr error) {
	p.Logger.Error("Upload processing failed", "entry", entry.ID, "error", uploadErr)
	_ = p.Storage.Delete(ctx, db.ID.String(), entry.ID)
	entry.Size = 0
	entry.Status = repo.EntryStatusError
	_, _ = p.Repo.UpdateEntry(ctx, db.ID, entry)
}

// claimTempFile moves an upload spooled to disk by the HTTP layer into a fresh
// worker-owned temporary file and returns its path. The original file handle is
// closed; the caller gains ownership of the returned path.
func claimTempFile(file *os.File) (string, error) {
	uploadTempPath := file.Name()

	workerTempFile, err := os.CreateTemp(os.TempDir(), "mh-worker-*")
	if err != nil {
		return "", fmt.Errorf("failed to create worker temp file: %w", err)
	}
	workerTempPath := workerTempFile.Name()
	workerTempFile.Close()

	file.Close()
	if err := os.Rename(uploadTempPath, workerTempPath); err != nil {
		os.Remove(workerTempPath)
		return "", fmt.Errorf("failed to claim temp file: %w", err)
	}
	return workerTempPath, nil
}
