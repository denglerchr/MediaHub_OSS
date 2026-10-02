package processing

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
)

func (p *Processor) createPreliminaryEntry(
	ctx context.Context,
	db repo.Database,
	entryMetadata EntryRequest,
	plan ProcessingPlan,
	status repo.EntryStatus,
	useResultMimeType bool,
) (repo.Entry, error) {
	var err error
	partialEntry := repo.Entry{}
	if entryMetadata.Timestamp == math.MinInt64 {
		partialEntry.Timestamp = time.Time{}
	} else {
		partialEntry.Timestamp = time.UnixMilli(entryMetadata.Timestamp)
	}

	if useResultMimeType {
		partialEntry.MimeType = plan.ResultMimeType
		partialEntry.FileName = plan.FinalFileName
	} else {
		partialEntry.MimeType = plan.InitMimeType
		partialEntry.FileName = plan.InitFileName
	}
	partialEntry.Status = status

	partialEntry.MediaFields, err = defaultMediaFields(db.ContentType)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to create default media fields: %w", err)
	}

	partialEntry.CustomFields = entryMetadata.CustomFields

	createdEntry, err := p.Repo.CreateEntry(ctx, db, partialEntry)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to create partial database entry: %w", err)
	}

	return createdEntry, nil
}

func (p *Processor) generateAndStorePreview(
	ctx context.Context,
	db repo.Database,
	entryID int64,
	inputSeeker io.ReadSeeker,
	mimeType string,
) (uint64, error) {
	pr, pw := io.Pipe()
	errChan := make(chan error, 1)

	go func() {
		defer pw.Close()
		err := p.MediaConverter.CreatePreviewFromStream(ctx, inputSeeker, pw, mimeType)
		errChan <- err
	}()

	previewSize, err := p.Storage.WritePreview(ctx, db.ID.String(), entryID, pr)
	if err != nil {
		pr.CloseWithError(err)
		<-errChan
		return 0, fmt.Errorf("failed to save preview to storage: %w", err)
	}

	if genErr := <-errChan; genErr != nil {
		return 0, fmt.Errorf("failed to generate preview: %w", genErr)
	}

	return uint64(previewSize), nil
}

func (p *Processor) generateAndStorePreviewFromFile(
	ctx context.Context,
	db repo.Database,
	entryID int64,
	filePath string,
	mimeType string,
) (uint64, error) {
	pr, pw := io.Pipe()
	errChan := make(chan error, 1)

	go func() {
		defer pw.Close()
		err := p.MediaConverter.CreatePreviewFromFile(ctx, filePath, pw, mimeType)
		errChan <- err
	}()

	previewSize, err := p.Storage.WritePreview(ctx, db.ID.String(), entryID, pr)
	if err != nil {
		pr.CloseWithError(err)
		<-errChan
		return 0, fmt.Errorf("failed to save preview to storage: %w", err)
	}

	if genErr := <-errChan; genErr != nil {
		return 0, fmt.Errorf("failed to generate preview: %w", genErr)
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
