package entryhandler

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// processImportJob handles the asynchronous extraction and database insertion for bulk imports.
func (h *EntryHandler) processImportJob(ctx context.Context, db repo.Database, username string, tempZipPath string, config ImportConfigPayload) {
	defer os.Remove(tempZipPath)

	h.Logger.Info("Background import job started", "database_id", db.ID, "user", username, "mode", config.Mode)

	// 1. Open the ZIP archive
	zr, err := zip.OpenReader(tempZipPath)
	if err != nil {
		h.Logger.Error("Import failed: Could not open ZIP archive", "database_id", db.ID, "error", err)
		return
	}
	defer zr.Close()

	// 2. Index the ZIP contents and find CSV
	zipFiles, csvZipFile, err := h.indexZipContents(zr)
	if err != nil {
		h.Logger.Error("Import failed", "database_id", db.ID, "error", err)
		return
	}

	// 3. Open and Parse entries.csv
	csvFile, err := csvZipFile.Open()
	if err != nil {
		h.Logger.Error("Import failed: Could not read entries.csv", "database_id", db.ID, "error", err)
		return
	}
	defer csvFile.Close()

	csvReader := csv.NewReader(csvFile)
	headers, err := csvReader.Read()
	if err != nil {
		h.Logger.Error("Import failed: Could not read CSV headers", "database_id", db.ID, "error", err)
		return
	}

	// 4. Validate Headers
	if err := h.validateCSVHeaders(headers); err != nil {
		h.Logger.Error("Import failed: CSV header validation", "database_id", db.ID, "error", err)
		return
	}

	// 5. Process Rows
	var successCount, skipCount, errorCount int

	for rowNum := 2; ; rowNum++ {
		row, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.Logger.Warn("Import warning: Could not read CSV row", "row", rowNum, "error", err)
			errorCount++
			continue
		}

		skipped, err := h.processImportRow(ctx, db, rowNum, row, headers, config, zipFiles)
		if err != nil {
			// Check if we need a hard abort due to unmapped fields
			if errors.Is(err, customerrors.ErrUnmappedFieldAbort) {
				h.Logger.Error("Import aborted: Unmapped field encountered", "database_id", db.ID, "row", rowNum)
				return
			}
			h.Logger.Warn("Import warning: Failed to process row", "row", rowNum, "error", err)
			errorCount++
		} else if skipped {
			skipCount++
		} else {
			successCount++
		}
	}

	// 6. Log Summary
	h.Logger.Info("Background import job completed",
		"database_id", db.ID,
		"successful", successCount,
		"skipped", skipCount,
		"errors", errorCount,
	)
}

// -----------------------------------------------------------------------------
// Helper Functions
// -----------------------------------------------------------------------------

// indexZipContents maps the ZIP contents for O(1) lookups and locates the entries.csv file.
func (h *EntryHandler) indexZipContents(zr *zip.ReadCloser) (map[string]*zip.File, *zip.File, error) {
	zipFiles := make(map[string]*zip.File)
	var csvZipFile *zip.File

	for _, f := range zr.File {
		zipFiles[f.Name] = f
		if f.Name == "entries.csv" {
			csvZipFile = f
		}
	}

	if csvZipFile == nil {
		return nil, nil, errors.New("entries.csv not found in archive")
	}

	return zipFiles, csvZipFile, nil
}

// validateCSVHeaders ensures the standard headers exist in the correct order.
func (h *EntryHandler) validateCSVHeaders(headers []string) error {
	expectedHeaders := []string{"id", "filename", "timestamp", "filesize", "previewsize", "mime_type", "status"}
	if len(headers) < len(expectedHeaders) {
		return errors.New("CSV has missing standard headers")
	}
	for i, expected := range expectedHeaders {
		if headers[i] != expected {
			return fmt.Errorf("invalid standard header format. Expected '%s', got '%s'", expected, headers[i])
		}
	}
	return nil
}

// processImportRow coordinates the database and storage insertions for a single CSV row.
func (h *EntryHandler) processImportRow(ctx context.Context, db repo.Database, rowNum int, row []string, headers []string, config ImportConfigPayload, zipFiles map[string]*zip.File) (bool, error) {

	// 1. Parse Standard Fields
	entry, err := h.parseStandardFields(row)
	if err != nil {
		return false, fmt.Errorf("invalid standard field format: %w", err)
	}
	originalCSVId := entry.ID

	// 2. Determine Target ID & Mode Logic
	if config.Mode == "skip" {
		_, errCheck := h.Repo.GetEntry(ctx, db.ID, entry.ID)
		exists := errCheck == nil

		if exists {
			return true, nil // Skipped successfully
		}
	} else {
		entry.ID = 0 // Instructs the Repo to generate a new Auto-Increment ID
	}

	// 3. Map Custom Fields
	customFields, err := h.mapCustomFields(row, headers, db, config)
	if err != nil {
		return false, err
	}
	entry.CustomFields = customFields

	// 4. Locate Files in ZIP
	mainZipPath := fmt.Sprintf("files/%d_%s", originalCSVId, entry.FileName)
	previewZipPath := fmt.Sprintf("previews/%d.webp", originalCSVId)

	mainFileZipped, ok := zipFiles[mainZipPath]
	if !ok {
		return false, fmt.Errorf("main media file missing in archive: %s", mainZipPath)
	}

	// 5. Extract File to Temp Disk & Read Metadata
	// Create a temp file to allow seeking (required by ffprobe) and safe storage streaming
	tempMediaFile, err := os.CreateTemp(os.TempDir(), "mh-import-entry-*.tmp")
	if err != nil {
		return false, fmt.Errorf("failed to create temporary file for extraction: %w", err)
	}
	tempFilePath := tempMediaFile.Name()
	defer os.Remove(tempFilePath) // Ensure it is cleaned up when this row finishes

	// Spool the zipped content into the temp file
	srcZipStream, err := mainFileZipped.Open()
	if err != nil {
		tempMediaFile.Close()
		return false, fmt.Errorf("failed to open zip entry %s: %w", mainZipPath, err)
	}
	if _, err := io.Copy(tempMediaFile, srcZipStream); err != nil {
		srcZipStream.Close()
		tempMediaFile.Close()
		return false, fmt.Errorf("failed to extract file from zip to disk: %w", err)
	}
	srcZipStream.Close()

	// Sync to disk to ensure ffprobe can read it properly
	tempMediaFile.Sync()

	// Extract Metadata using the fast disk-based method
	mediaFields, err := h.MediaConverter.ReadMediaFieldsFromFile(ctx, tempFilePath, db.ContentType)
	if err != nil {
		h.Logger.Warn("Import warning: Failed to extract metadata", "file", entry.FileName, "error", err)
		// We log the warning but proceed; the database constraints will reject it if required fields are missing
	} else {
		entry.MediaFields = mediaFields
	}

	// 6. Write to Database
	savedEntry, err := h.Repo.CreateEntry(ctx, db, entry)
	if err != nil {
		tempMediaFile.Close()
		return false, fmt.Errorf("failed to insert entry in database: %w", err)
	}

	// 7. Write Main File to Storage
	// Rewind the temp file so we can stream it to the final storage location
	tempMediaFile.Seek(0, io.SeekStart)
	_, err = h.Storage.Write(ctx, db.ID.String(), savedEntry.ID, tempMediaFile)
	tempMediaFile.Close() // Close the handle now that storage has consumed it

	if err != nil {
		h.Repo.DeleteEntry(ctx, db.ID, savedEntry.ID) // Rollback DB on storage failure
		return false, fmt.Errorf("failed to write main file to storage: %w", err)
	}

	// 8. Write Preview to Storage (if it exists in the archive)
	if previewZipped, exists := zipFiles[previewZipPath]; exists {
		pSrcFile, err := previewZipped.Open()
		if err != nil {
			h.Logger.Warn("Import warning: Failed to open preview zip entry", "row", rowNum, "path", previewZipPath, "error", err)
		} else {
			_, err = h.Storage.WritePreview(ctx, db.ID.String(), savedEntry.ID, pSrcFile)
			pSrcFile.Close()
			if err != nil {
				h.Logger.Warn("Import warning: Failed to write preview file to storage", "row", rowNum, "error", err)
			}
		}
	}

	return false, nil // Success, not skipped
}

// parseStandardFields extracts and types the first 7 standard columns from the CSV.
func (h *EntryHandler) parseStandardFields(row []string) (repo.Entry, error) {
	var entry repo.Entry
	var err error

	if entry.ID, err = strconv.ParseInt(row[0], 10, 64); err != nil {
		return entry, fmt.Errorf("invalid ID: %s", row[0])
	}

	entry.FileName = row[1]

	if entry.Timestamp, err = time.Parse(time.RFC3339, row[2]); err != nil {
		return entry, fmt.Errorf("invalid timestamp: %s", row[2])
	}
	if entry.Size, err = strconv.ParseUint(row[3], 10, 64); err != nil {
		return entry, fmt.Errorf("invalid filesize: %s", row[3])
	}
	if entry.PreviewSize, err = strconv.ParseUint(row[4], 10, 64); err != nil {
		return entry, fmt.Errorf("invalid previewsize: %s", row[4])
	}

	entry.MimeType = row[5]

	statusVal, err := strconv.ParseUint(row[6], 10, 8)
	if err != nil {
		return entry, fmt.Errorf("invalid status: %s", row[6])
	}
	entry.Status = repo.EntryStatus(statusVal)

	return entry, nil
}

// mapCustomFields extracts dynamic columns, applies user mapping, and validates against the DB schema.
func (h *EntryHandler) mapCustomFields(row []string, headers []string, db repo.Database, config ImportConfigPayload) (map[string]any, error) {
	mappedCustomFields := make(map[string]any)

	// Fetch media fields for the content type to ignore them in custom fields mapping
	mediaFieldDefs, _ := media.GetMetadataFields(db.ContentType)
	isMediaField := func(name string) bool {
		for _, mf := range mediaFieldDefs {
			if mf.Name == name {
				return true
			}
		}
		return false
	}

	for i := 7; i < len(headers); i++ {
		if i >= len(row) {
			break
		}

		csvHeader := headers[i]
		if isMediaField(csvHeader) {
			continue
		}

		dbField := csvHeader

		// Apply user-defined mapping if provided
		if mapped, ok := config.CustomFieldMapping[csvHeader]; ok {
			dbField = mapped
		}

		// Validate against database schema and enforce types
		validField := false
		for _, cf := range db.CustomFields {
			if cf.Name == dbField {
				validField = true
				switch cf.Type {
				case repo.CustomFieldTypeInteger:
					val, _ := strconv.ParseInt(row[i], 10, 64)
					mappedCustomFields[dbField] = val
				case repo.CustomFieldTypeReal:
					val, _ := strconv.ParseFloat(row[i], 64)
					mappedCustomFields[dbField] = val
				case repo.CustomFieldTypeBoolean:
					val, _ := strconv.ParseBool(row[i])
					mappedCustomFields[dbField] = val
				case repo.CustomFieldTypeCoordinate:
					cell := strings.TrimSpace(row[i])
					if cell != "" {
						parts := strings.Split(cell, ",")
						if len(parts) == 2 {
							lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
							lng, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
							if err1 == nil && err2 == nil {
								coord, err := repo.ParseCoordinate(repo.Coordinate{Latitude: lat, Longitude: lng})
								if err == nil {
									mappedCustomFields[dbField] = coord
								}
							}
						}
					}
				default: // TEXT
					mappedCustomFields[dbField] = row[i]
				}
				break
			}
		}

		if !validField {
			if config.UnmappedFields == "fail" {
				return nil, customerrors.ErrUnmappedFieldAbort
			}
			// If "ignore", we simply don't add it to mappedCustomFields
		}
	}

	return mappedCustomFields, nil
}
