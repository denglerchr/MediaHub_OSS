package processing

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
)

// createPreliminaryEntry creates the database entry for an incoming upload before
// its file content is stored. The entry is created in "processing" status.
//
// If useResultMimeType is true, the entry directly advertises the final (converted)
// MIME type and file name; otherwise it advertises the original ones, which are
// corrected by the background worker once processing completes.
func (p *Processor) createPreliminaryEntry(
	ctx context.Context,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
	useResultMimeType bool,
) (repo.Entry, error) {
	entry := repo.Entry{
		Status:       repo.EntryStatusProcessing,
		CustomFields: req.CustomFields,
	}

	if req.Timestamp == shared.MissingTimestamp {
		entry.Timestamp = time.Time{}
	} else {
		entry.Timestamp = time.UnixMilli(req.Timestamp)
	}

	if useResultMimeType {
		entry.MimeType = plan.ResultMimeType
		entry.FileName = plan.FinalFileName
	} else {
		entry.MimeType = plan.InitMimeType
		entry.FileName = plan.InitFileName
	}

	var err error
	entry.MediaFields, err = defaultMediaFields(db.ContentType)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to create default media fields: %w", err)
	}

	createdEntry, err := p.Repo.CreateEntry(ctx, db, entry)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to create preliminary database entry: %w", err)
	}

	return createdEntry, nil
}

// storeProcessedStream writes a fully processed (possibly converted) stream to
// storage and records the resulting file size on the entry. On failure the entry
// is marked as erroneous via failUploadEntry.
func (p *Processor) storeProcessedStream(ctx context.Context, db repo.Database, entry *repo.Entry, stream io.ReadSeeker) error {
	if _, err := stream.Seek(0, io.SeekStart); err != nil {
		p.failUploadEntry(ctx, db, *entry, err)
		return fmt.Errorf("failed to seek file stream before storage: %w", err)
	}

	fileSize, err := p.Storage.Write(ctx, db.ID, entry.ID, stream)
	if err != nil {
		p.failUploadEntry(ctx, db, *entry, err)
		return fmt.Errorf("failed to write to storage provider: %w", err)
	}
	entry.Size = uint64(fileSize)
	return nil
}

const maxPreviewBytes = 10 << 20 // 10 MiB

var errPreviewTooLarge = fmt.Errorf("generated preview exceeds maximum size (%d bytes)", maxPreviewBytes)

type cappedWriter struct {
	buf      *bytes.Buffer
	limit    int
	exceeded bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.exceeded || len(p) > w.limit-w.buf.Len() {
		w.exceeded = true
		return 0, errPreviewTooLarge
	}
	return w.buf.Write(p)
}

// generateAndStorePreview buffers the output of the preview producer in memory up
// to maxPreviewBytes and then writes it to storage with a known byte length.
func (p *Processor) generateAndStorePreview(
	ctx context.Context,
	db repo.Database,
	entryID int64,
	produce func(ctx context.Context, w io.Writer) error,
) (uint64, error) {
	genCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var buf bytes.Buffer
	cw := &cappedWriter{buf: &buf, limit: maxPreviewBytes}
	if err := produce(genCtx, cw); err != nil {
		return 0, fmt.Errorf("failed to generate preview: %w", err)
	}
	if cw.exceeded {
		return 0, fmt.Errorf("failed to generate preview: %w", errPreviewTooLarge)
	}

	previewSize, err := p.Storage.WritePreview(ctx, db.ID, entryID, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return 0, fmt.Errorf("failed to save preview to storage: %w", err)
	}

	return uint64(previewSize), nil
}

// defaultMediaFields returns dynamic defaults for media fields based on content type.
func defaultMediaFields(contentType string) (map[string]any, error) {
	var val any

	metadataFields, err := media.GetMetadataFields(contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata fields: %w", err)
	}

	mediaFields := make(map[string]any)
	for _, field := range metadataFields {
		switch field.Type {
		case "uint8":
			val = uint8(0)
		case "uint64":
			val = uint64(0)
		case "int64":
			val = int64(-1)
		case "float64":
			val = float64(-1.0)
		case "bool":
			val = false
		default:
			return nil, fmt.Errorf("implementation missing default value for media field type %s", field.Type)
		}
		mediaFields[field.Name] = val
	}

	return mediaFields, nil
}
