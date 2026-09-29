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

func (p *Processor) handleSmallFileSync(
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

	cleanupOnError := func(uploadErr error) {
		p.Logger.Error("Upload failed", "entry", createdEntry.ID, "error", uploadErr)
		createdEntry.Status = repo.EntryStatusError
		_, _ = p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanupOnError(err)
		return repo.Entry{}, fmt.Errorf("failed to seek original file for probing: %w", err)
	}

	meta, metaErr := p.MediaConverter.ReadMediaFieldsFromStream(ctx, file, db.ContentType)
	if metaErr == nil {
		createdEntry.MediaFields = meta
	} else {
		p.Logger.Warn("could not extract metadata from original file", "entryID", createdEntry.ID, "error", metaErr)
	}

	var streamToUpload io.ReadSeeker = file
	var tempStream *TempFileStream
	if plan.WantsConversion && plan.NeedsConversion {
		if !plan.CanConvert {
			err := fmt.Errorf("cannot convert %v to the database mime type %v", plan.InitMimeType, db.Config.AutoConversion)
			cleanupOnError(err)
			return repo.Entry{}, err
		}

		var err error
		tempStream, _, err = p.convertStreamToTempFile(ctx, streamToUpload, plan.InitMimeType, media.ConversionOptions{
			TargetMimeType: plan.ResultMimeType,
		})
		if err != nil {
			cleanupOnError(err)
			return repo.Entry{}, err
		}
		defer tempStream.Close()

		streamToUpload = tempStream
	}

	if _, err := streamToUpload.Seek(0, io.SeekStart); err != nil {
		cleanupOnError(err)
		return repo.Entry{}, fmt.Errorf("failed to seek file stream before storage: %w", err)
	}

	fileSize, err := p.Storage.Write(ctx, db.ID.String(), createdEntry.ID, streamToUpload)
	if err != nil {
		cleanupOnError(err)
		return repo.Entry{}, fmt.Errorf("failed to write to storage provider: %w", err)
	}
	createdEntry.Size = uint64(fileSize)

	if plan.WantsPreview && plan.CanGenPreview {
		streamToUpload.Seek(0, io.SeekStart)
		fileBytes, err := io.ReadAll(streamToUpload)
		if err != nil {
			p.Logger.Error("Failed to read file into memory for preview generation", "entry", createdEntry.ID, "error", err)
			createdEntry.Status = repo.EntryStatusReady
			finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
			if err != nil {
				return repo.Entry{}, fmt.Errorf("failed to finalize entry metadata: %w", err)
			}
			return finalEntry, nil
		}

		createdEntry.Status = repo.EntryStatusProcessing
		finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
		if err != nil {
			return repo.Entry{}, fmt.Errorf("failed to finalize entry metadata: %w", err)
		}

		go func(bgEntry repo.Entry) {
			var err error
			var previewSize uint64 = 0

			reader := bytes.NewReader(fileBytes)
			if previewSize, err = p.generateAndStorePreview(context.Background(), db, bgEntry.ID, reader, plan.ResultMimeType); err != nil {
				p.Logger.Error("Async preview generation failed", "entry", bgEntry.ID, "error", err)
			}

			bgEntry.Status = repo.EntryStatusReady
			bgEntry.PreviewSize = previewSize

			if _, err := p.Repo.UpdateEntry(context.Background(), db.ID, bgEntry); err != nil {
				p.Logger.Error("Failed to update status to ready after async preview", "entry", bgEntry.ID, "error", err)
			}
		}(finalEntry)

		return finalEntry, nil
	}

	createdEntry.Status = repo.EntryStatusReady
	finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
	if err != nil {
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

	cleanupSlot := func() {
		p.releaseSyncSlot()
		p.TriggerQueueWorkersIfPossible(context.Background())
	}

	stream, size, err := p.convertStreamToTempFile(ctx, input, inputMimeType, opts)
	if err != nil {
		cleanupSlot()
		return nil, 0, err
	}

	stream.onClose = cleanupSlot
	return stream, size, nil
}
