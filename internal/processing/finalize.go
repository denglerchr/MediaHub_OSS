package processing

import (
	"context"
	"fmt"
	"io"
	"os"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
)

// finalizeEntryFile runs the finalization pipeline for a background-processed
// entry (async upload or claimed queued entry):
//
//  1. Convert the temp file if the plan requires it.
//  2. Extract media metadata if the database content type defines fields.
//  3. Generate and store the preview if configured.
//  4. Write the final file to storage.
//  5. Update the entry (size, MIME type, file name) and mark it ready.
//
// It takes ownership of originalTempPath and removes it (plus any intermediate
// conversion output) when done. On failure the entry is marked as erroneous via
// failProcessedEntry and the error is returned; the previously stored original
// file (for queued entries) is preserved.
func (p *Processor) finalizeEntryFile(
	ctx context.Context,
	db repo.Database,
	entry repo.Entry,
	originalTempPath string,
	plan ProcessingPlan,
) (retErr error) {
	p.Logger.Debug("Worker: Starting finalization pipeline", "entry", entry.ID)

	var processErr error
	cleanupPaths := []string{originalTempPath}

	defer func() {
		for _, path := range cleanupPaths {
			os.Remove(path)
		}
		if processErr != nil {
			p.Logger.Error("Worker: Processing failed", "entry", entry.ID, "error", processErr)
			p.failProcessedEntry(ctx, db.ID, entry)
		}
		retErr = processErr
	}()

	currentPath := originalTempPath

	// 1. Convert
	if plan.WantsConversion && plan.NeedsConversion {
		if !plan.CanConvert {
			processErr = fmt.Errorf("cannot convert %v to the database mime type %v", plan.InitMimeType, db.Config.AutoConversion)
			return
		}

		convertedFile, err := os.CreateTemp(os.TempDir(), "mh-converted-*")
		if err != nil {
			processErr = fmt.Errorf("failed to create converted temp file: %w", err)
			return
		}
		convertedPath := convertedFile.Name()
		convertedFile.Close()
		cleanupPaths = append(cleanupPaths, convertedPath)

		if err := p.MediaConverter.ConvertFile(ctx, currentPath, convertedPath, plan.InitMimeType, media.ConversionOptions{
			TargetMimeType: plan.TargetMimeType,
		}); err != nil {
			processErr = fmt.Errorf("conversion to file failed: %w", err)
			return
		}
		currentPath = convertedPath
	}

	// 2. Extract metadata
	if mf, err := media.GetMetadataFields(db.ContentType); err == nil && len(mf) > 0 {
		if extractedMeta, err := p.MediaConverter.ReadMediaFieldsFromFile(ctx, currentPath, db.ContentType); err != nil {
			p.Logger.Warn("Worker: Failed to extract metadata", "entry", entry.ID, "error", err)
		} else {
			entry.MediaFields = extractedMeta
		}
	}

	// 3. Generate preview
	if plan.WantsPreview && plan.CanGenPreview {
		previewSize, err := p.generateAndStorePreview(ctx, db, entry.ID, plan.ResultMimeType, func(ctx context.Context, w io.Writer) error {
			return p.MediaConverter.CreatePreviewFromFile(ctx, currentPath, w, plan.ResultMimeType)
		})
		if err != nil {
			p.Logger.Error("Worker: Failed to generate or store preview", "entry", entry.ID, "error", err)
		} else {
			entry.PreviewSize = previewSize
		}
	}

	// 4. Store the final file
	finalFile, err := os.Open(currentPath)
	if err != nil {
		processErr = fmt.Errorf("failed to open final file for storage: %w", err)
		return
	}
	fileSize, err := p.Storage.Write(ctx, db.ID.String(), entry.ID, finalFile)
	finalFile.Close()
	if err != nil {
		processErr = fmt.Errorf("failed to stream file to storage: %w", err)
		return
	}

	// 5. Finalize the entry
	entry.Status = repo.EntryStatusReady
	entry.Size = uint64(fileSize)
	entry.MimeType = plan.ResultMimeType
	entry.FileName = plan.FinalFileName

	if _, err := p.Repo.UpdateEntry(ctx, db.ID, entry); err != nil {
		processErr = fmt.Errorf("failed to update final database stats: %w", err)
		return
	}

	p.Logger.Info("Worker: Successfully processed entry", "entry", entry.ID)
	return
}
