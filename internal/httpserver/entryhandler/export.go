package entryhandler

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"net/http"
	"strconv"
	"time"
)

// @Summary Export entries as ZIP
// @Description Streams a ZIP archive containing the files and metadata (CSV) for the specified entries using io.Pipe.
// @Tags database
// @Accept  json
// @Produce application/zip
// @Param   database_id  path   string        true  "Database ID"
// @Param   body    body   ExportRequest  true  "List of Entry IDs to export"
// @Success 200 {file} file "ZIP Archive containing files and entries.csv"
// @Failure 400 {object} utils.ErrorResponse "Missing id query parameter or empty IDs list"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden (Requires CanView role)"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 500 {object} utils.ErrorResponse "ZIP streaming failed"
// @Security BasicAuth
// @Router /database/{database_id}/entries/export [post]
func (h *EntryHandler) ExportEntries(w http.ResponseWriter, r *http.Request) {
	dbID, ok := parseDatabasePathParam(w, r)
	if !ok {
		return
	}

	user := utils.GetUserFromContext(r.Context())

	var req ExportRequest
	if status, msg := utils.DecodeJSONBody(w, r, &req); status != 0 {
		utils.RespondWithError(w, status, msg)
		return
	}
	if len(req.IDs) == 0 {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid request or empty IDs list")
		return
	}

	db, ok := h.getDatabaseOrRespond(w, r, dbID)
	if !ok {
		return
	}

	// Set headers for ZIP download
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s_export.zip\"", dbID))

	// Use io.Pipe to stream generation directly to the HTTP response
	pr, pw := io.Pipe()
	defer pr.CloseWithError(io.ErrClosedPipe)

	// Detached context so file reads in the worker aren't immediately aborted by client cancellation
	// before the pipe write encounters ErrClosedPipe and exits cleanly.
	workerCtx := context.WithoutCancel(r.Context())

	go h.writeExportArchive(workerCtx, pw, db, req.IDs)

	h.Auditor.Log(r.Context(), "entries.export", user.Username, dbID.String(), map[string]any{"count": len(req.IDs)})

	// Stream the pipe reader directly to the response writer
	if _, err := io.Copy(w, pr); err != nil {
		h.Logger.Error("Failed to stream ZIP to client", "error", err)
	}
}

func (h *EntryHandler) writeExportArchive(ctx context.Context, pw *io.PipeWriter, db repo.Database, entryIDs []int64) {
	defer pw.Close()

	zipWriter := zip.NewWriter(pw)
	defer zipWriter.Close()

	validEntries, err := h.writeExportCSV(ctx, zipWriter, db, entryIDs)
	if err != nil {
		pw.CloseWithError(err)
		return
	}

	h.writeExportMediaFiles(ctx, zipWriter, db.ID, validEntries)
}

func (h *EntryHandler) writeExportCSV(ctx context.Context, zipWriter *zip.Writer, db repo.Database, entryIDs []int64) ([]repo.Entry, error) {
	csvFile, err := zipWriter.Create("entries.csv")
	if err != nil {
		h.Logger.Error("Failed to create CSV in zip", "error", err)
		return nil, err
	}

	csvWriter := csv.NewWriter(csvFile)

	// Build dynamic CSV Header
	mediaFieldDefs, _ := media.GetMetadataFields(db.ContentType)
	header := []string{"id", "filename", "timestamp", "filesize", "previewsize", "mime_type", "status"}
	for _, mf := range mediaFieldDefs {
		header = append(header, mf.Name)
	}
	for _, cf := range db.CustomFields {
		header = append(header, cf.Name)
	}
	_ = csvWriter.Write(header)

	// Keep track of valid entries so we don't have to query the DB twice
	var validEntries []repo.Entry

	// Pass 1: Fetch metadata and write all CSV rows
	for _, id := range entryIDs {
		entry, err := h.Repo.GetEntry(ctx, db.ID, id)
		if err != nil {
			h.Logger.Warn("Skipping entry in export (not found)", "id", id)
			continue
		}

		validEntries = append(validEntries, entry)

		row := formatExportCSVRow(entry, mediaFieldDefs, db.CustomFields)
		if err := csvWriter.Write(row); err != nil {
			// The ZIP stream is broken (e.g. client disconnected); abort the export.
			h.Logger.Warn("Aborting export: failed to write CSV row", "id", entry.ID, "error", err)
			return nil, err
		}
	}

	// Flush the CSV buffer to the zip file BEFORE creating new zip entries
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		h.Logger.Warn("Aborting export: failed to flush CSV", "error", err)
		return nil, err
	}

	return validEntries, nil
}

func formatExportCSVRow(entry repo.Entry, mediaFieldDefs []media.FieldDef, customFields []repo.CustomFieldDef) []string {
	row := []string{
		strconv.FormatInt(entry.ID, 10),
		entry.FileName,
		entry.Timestamp.Format(time.RFC3339),
		strconv.FormatUint(entry.Size, 10),
		strconv.FormatUint(entry.PreviewSize, 10),
		entry.MimeType,
		strconv.Itoa(int(entry.Status)),
	}

	for _, mf := range mediaFieldDefs {
		val, exists := entry.MediaFields[mf.Name]
		if !exists || val == nil {
			row = append(row, "")
		} else {
			row = append(row, fmt.Sprintf("%v", val))
		}
	}

	for _, cf := range customFields {
		val, exists := entry.CustomFields[cf.Name]
		if !exists || val == nil {
			row = append(row, "")
		} else if cf.Type.IsCoordinate() {
			if coord, err := repo.ParseCoordinate(val); err == nil {
				row = append(row, fmt.Sprintf("%v, %v", coord.Latitude, coord.Longitude))
			} else {
				row = append(row, fmt.Sprintf("%v", val))
			}
		} else {
			row = append(row, fmt.Sprintf("%v", val))
		}
	}

	return row
}

func (h *EntryHandler) writeExportMediaFiles(ctx context.Context, zipWriter *zip.Writer, dbID repo.ULID, validEntries []repo.Entry) {
	for _, entry := range validEntries {
		fileStream, err := h.Storage.Read(ctx, dbID, entry.ID, 0, -1)
		if err != nil {
			h.Logger.Warn("Failed to read file from storage for export", "id", entry.ID, "error", err)
			continue
		}

		// User-controlled names are sanitized to prevent zip-slip path traversal.
		zipEntryPath := fmt.Sprintf("files/%d_%s", entry.ID, sanitizeZipEntryName(entry.FileName))
		if abort := h.writeStreamToZip(zipWriter, zipEntryPath, fileStream, "file", entry.ID); abort {
			return
		}

		if entry.PreviewSize > 0 {
			previewStream, err := h.Storage.ReadPreview(ctx, dbID, entry.ID)
			if err != nil {
				h.Logger.Warn("Failed to read preview from storage for export", "id", entry.ID, "error", err)
				continue
			}

			zipPreviewPath := fmt.Sprintf("previews/%d.webp", entry.ID)
			if abort := h.writeStreamToZip(zipWriter, zipPreviewPath, previewStream, "preview", entry.ID); abort {
				return
			}
		}
	}
}

func (h *EntryHandler) writeStreamToZip(zipWriter *zip.Writer, zipPath string, stream io.ReadCloser, kind string, entryID int64) bool {
	defer stream.Close()

	zipEntry, err := zipWriter.Create(zipPath)
	if err != nil {
		h.Logger.Warn("Aborting export: failed to create zip entry for "+kind, "id", entryID, "error", err)
		return true
	}

	if _, err := io.Copy(zipEntry, stream); err != nil {
		h.Logger.Warn("Aborting export: failed to stream "+kind+" into zip", "id", entryID, "error", err)
		return true
	}

	return false
}
