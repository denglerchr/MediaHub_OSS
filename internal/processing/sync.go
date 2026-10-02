package processing

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// TempFileStream wraps an open temporary *os.File and an optional onClose cleanup callback.
// Calling Close() closes the underlying file handle, removes the temporary file from disk,
// and invokes onClose (e.g. to release concurrency slots and trigger background queue workers).
type TempFileStream struct {
	*os.File
	onClose func()
}

// Close closes the file handle, deletes the underlying temp file, and invokes the onClose callback.
func (t *TempFileStream) Close() error {
	var err error
	if t.File != nil {
		name := t.File.Name()
		err = t.File.Close()
		os.Remove(name)
		t.File = nil
	}
	if t.onClose != nil {
		t.onClose()
		t.onClose = nil
	}
	return err
}

// storeSmallFilePassthrough stores a small file synchronously without conversion or reserving an FFmpeg slot.
func (p *Processor) storeSmallFilePassthrough(
	ctx context.Context,
	file io.ReadSeeker,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, repo.EntryStatusProcessing, true)
	if err != nil {
		return repo.Entry{}, err
	}

	return p.finalizeSmallFileSync(ctx, file, nil, db, createdEntry, plan)
}

// handleSmallFileSync converts (if needed) and stores a small file synchronously using a reserved FFmpeg sync slot.
func (p *Processor) handleSmallFileSync(
	ctx context.Context,
	file io.ReadSeeker,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	if !p.tryReserveSyncSlot() {
		return repo.Entry{}, customerrors.ErrResourceExhausted
	}
	defer p.releaseSyncSlot()

	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, repo.EntryStatusProcessing, true)
	if err != nil {
		return repo.Entry{}, err
	}

	var streamToUpload io.ReadSeeker = file
	var tempStream *TempFileStream
	if plan.WantsConversion && plan.NeedsConversion {
		if !plan.CanConvert {
			err := fmt.Errorf("cannot convert %v to the database mime type %v", plan.InitMimeType, db.Config.AutoConversion)
			p.failSyncEntry(ctx, db, createdEntry, err)
			return repo.Entry{}, err
		}

		tempStream, _, err = p.convertStreamToTempFile(ctx, file, plan.InitMimeType, media.ConversionOptions{
			TargetMimeType: plan.ResultMimeType,
		})
		if err != nil {
			p.failSyncEntry(ctx, db, createdEntry, err)
			return repo.Entry{}, err
		}
		streamToUpload = tempStream
	}

	return p.finalizeSmallFileSync(ctx, streamToUpload, tempStream, db, createdEntry, plan)
}

func (p *Processor) failSyncEntry(ctx context.Context, db repo.Database, entry repo.Entry, uploadErr error) {
	p.Logger.Error("Upload failed", "entry", entry.ID, "error", uploadErr)
	_ = p.Storage.Delete(ctx, db.ID.String(), entry.ID)
	entry.Size = 0
	entry.Status = repo.EntryStatusError
	_, _ = p.Repo.UpdateEntry(ctx, db.ID, entry)
}

// finalizeSmallFileSync extracts metadata, writes the stream to storage, and triggers background preview generation if configured.
func (p *Processor) finalizeSmallFileSync(
	ctx context.Context,
	streamToUpload io.ReadSeeker,
	tempStream *TempFileStream,
	db repo.Database,
	createdEntry repo.Entry,
	plan ProcessingPlan,
) (repo.Entry, error) {
	closeTempOnReturn := true
	defer func() {
		if closeTempOnReturn && tempStream != nil {
			tempStream.Close()
		}
	}()

	// 1. Extract media metadata (if the database content type defines metadata fields)
	if mf, err := media.GetMetadataFields(db.ContentType); err == nil && len(mf) > 0 {
		var meta map[string]any
		var metaErr error
		if tempStream != nil {
			meta, metaErr = p.MediaConverter.ReadMediaFieldsFromFile(ctx, tempStream.Name(), db.ContentType)
		} else {
			if _, err := streamToUpload.Seek(0, io.SeekStart); err != nil {
				p.failSyncEntry(ctx, db, createdEntry, err)
				return repo.Entry{}, fmt.Errorf("failed to seek file for probing: %w", err)
			}
			meta, metaErr = p.MediaConverter.ReadMediaFieldsFromStream(ctx, streamToUpload, db.ContentType)
		}
		if metaErr == nil {
			createdEntry.MediaFields = meta
		} else {
			p.Logger.Warn("could not extract metadata from file", "entryID", createdEntry.ID, "error", metaErr)
		}
	}

	// 2. Write file to storage
	if _, err := streamToUpload.Seek(0, io.SeekStart); err != nil {
		p.failSyncEntry(ctx, db, createdEntry, err)
		return repo.Entry{}, fmt.Errorf("failed to seek file stream before storage: %w", err)
	}

	fileSize, err := p.Storage.Write(ctx, db.ID.String(), createdEntry.ID, streamToUpload)
	if err != nil {
		p.failSyncEntry(ctx, db, createdEntry, err)
		return repo.Entry{}, fmt.Errorf("failed to write to storage provider: %w", err)
	}
	createdEntry.Size = uint64(fileSize)

	// 3. Optionally generate preview in the background
	if plan.WantsPreview && plan.CanGenPreview {
		var fileBytes []byte
		if tempStream == nil {
			if _, err := streamToUpload.Seek(0, io.SeekStart); err == nil {
				fileBytes, err = io.ReadAll(streamToUpload)
			}
			if err != nil {
				p.Logger.Error("Failed to read file into memory for preview generation", "entry", createdEntry.ID, "error", err)
				createdEntry.Status = repo.EntryStatusReady
				finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
				if err != nil {
					p.failSyncEntry(ctx, db, createdEntry, err)
					return repo.Entry{}, fmt.Errorf("failed to finalize entry metadata: %w", err)
				}
				return finalEntry, nil
			}
		}

		createdEntry.Status = repo.EntryStatusProcessing
		finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
		if err != nil {
			p.failSyncEntry(ctx, db, createdEntry, err)
			return repo.Entry{}, fmt.Errorf("failed to finalize entry metadata: %w", err)
		}

		closeTempOnReturn = false
		go func(bgEntry repo.Entry, ts *TempFileStream, rawBytes []byte) {
			var previewSize uint64
			var err error
			if ts != nil {
				defer ts.Close()
				previewSize, err = p.generateAndStorePreviewFromFile(context.Background(), db, bgEntry.ID, ts.Name(), plan.ResultMimeType)
			} else {
				previewSize, err = p.generateAndStorePreview(context.Background(), db, bgEntry.ID, bytes.NewReader(rawBytes), plan.ResultMimeType)
			}
			if err != nil {
				p.Logger.Error("Async preview generation failed", "entry", bgEntry.ID, "error", err)
			}

			bgEntry.Status = repo.EntryStatusReady
			bgEntry.PreviewSize = previewSize
			if _, err := p.Repo.UpdateEntry(context.Background(), db.ID, bgEntry); err != nil {
				p.Logger.Error("Failed to update status to ready after async preview", "entry", bgEntry.ID, "error", err)
			}
		}(finalEntry, tempStream, fileBytes)

		return finalEntry, nil
	}

	// 4. Finalize entry as Ready
	createdEntry.Status = repo.EntryStatusReady
	finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
	if err != nil {
		p.failSyncEntry(ctx, db, createdEntry, err)
		return repo.Entry{}, fmt.Errorf("failed to finalize entry metadata: %w", err)
	}

	return finalEntry, nil
}

// convertStreamToTempFile converts an input stream into an optimized temporary file using MediaConverter.ConvertStreamToFile
// without reserving concurrency slots.
// It returns a TempFileStream wrapping the temp file and its size in bytes.
func (p *Processor) convertStreamToTempFile(
	ctx context.Context,
	input io.Reader,
	inputMimeType string,
	opts media.ConversionOptions,
) (*TempFileStream, int64, error) {
	var seeker io.ReadSeeker
	if rs, ok := input.(io.ReadSeeker); ok {
		if _, err := rs.Seek(0, io.SeekStart); err != nil {
			return nil, 0, fmt.Errorf("failed to seek input stream: %w", err)
		}
		seeker = rs
	} else {
		data, err := io.ReadAll(input)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to read input stream: %w", err)
		}
		seeker = bytes.NewReader(data)
	}

	tempFile, err := p.MediaConverter.ConvertStreamToFile(ctx, seeker, inputMimeType, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("stream conversion failed: %w", err)
	}

	stat, err := tempFile.Stat()
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, 0, fmt.Errorf("failed to stat converted temporary file: %w", err)
	}

	return &TempFileStream{File: tempFile}, stat.Size(), nil
}

// ConvertStream reserves a synchronous FFmpeg slot, converts the input stream
// according to the provided options into an optimized temporary file stream,
// and releases the slot upon Close() of the returned stream.
// It returns the open TempFileStream, the exact file size in bytes, or an error.
// Returns customerrors.ErrUnavailable if all conversion slots are currently occupied.
func (p *Processor) ConvertStream(
	ctx context.Context,
	input io.Reader,
	inputMimeType string,
	opts media.ConversionOptions,
) (*TempFileStream, int64, error) {
	if !p.tryReserveSyncSlot() {
		return nil, 0, customerrors.ErrUnavailable
	}

	stream, size, err := p.convertStreamToTempFile(ctx, input, inputMimeType, opts)
	if err != nil {
		p.releaseSyncSlot()
		return nil, 0, err
	}

	stream.onClose = p.releaseSyncSlot
	return stream, size, nil
}


