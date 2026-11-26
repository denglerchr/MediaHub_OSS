package entryhandler

import (
	"encoding/json"
	"errors"
	"io"
	"mediahub_oss/internal/httpserver/utils"
	"mime/multipart"
	"net/http"
	"os"
)

// @Summary Bulk import entries
// @Description Accepts a ZIP archive containing media files and an entries.csv metadata file to bulk-import entries into the database.
// @Description The ZIP file is spooled directly to a temporary file on the server's disk to ensure a low memory footprint. Processing happens asynchronously.
// @Tags database
// @Accept mpfd
// @Produce json
// @Param database_id path string true "Database ID"
// @Param file formData file true "The ZIP archive containing the media files and entries.csv"
// @Param config formData string false "JSON string defining the rules for the import process (e.g., mode, custom_field_mapping, unmapped_fields)"
// @Success 202 {object} ImportResponse "Import job started successfully"
// @Failure 400 {object} utils.ErrorResponse "Invalid request, missing file, or invalid config"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden (Requires CanCreate role)"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 415 {object} utils.ErrorResponse "Unsupported Media Type (Not a ZIP archive)"
// @Failure 500 {object} utils.ErrorResponse "Internal Server Error"
// @Security BasicAuth
// @Router /database/{database_id}/entries/import [post]
func (h *EntryHandler) ImportEntries(w http.ResponseWriter, r *http.Request) {
	dbID, ok := parseDatabasePathParam(w, r)
	if !ok {
		return
	}
	user := utils.GetUserFromContext(r.Context())

	db, ok := h.getDatabaseOrRespond(w, r, dbID)
	if !ok {
		return
	}

	// Parse Multipart Form with bounded upload size
	if r.Body == nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing request body.")
		return
	}
	uploadLimit := h.maxImportUploadBytes
	if uploadLimit <= 0 {
		uploadLimit = maxImportUploadBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)
	if err := r.ParseMultipartForm(h.MaxSyncUploadSizeBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			utils.RespondWithError(w, http.StatusRequestEntityTooLarge, "Import archive exceeds maximum allowed upload size.")
			return
		}
		h.Logger.Warn("Failed to parse multipart form for import", "error", err)
		utils.RespondWithError(w, http.StatusBadRequest, "Failed to parse multipart form.")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	// Extract and Parse Config (with safe defaults)
	configStr := r.FormValue("config")
	importConfig := ImportConfigPayload{
		Mode:               "generate_new",
		CustomFieldMapping: make(map[string]string),
		UnmappedFields:     "ignore",
	}

	if configStr != "" {
		if err := json.Unmarshal([]byte(configStr), &importConfig); err != nil {
			utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON format in 'config' parameter.")
			return
		}
		if importConfig.Mode != "generate_new" && importConfig.Mode != "skip" {
			utils.RespondWithError(w, http.StatusBadRequest, "Invalid 'mode' specified in config. Allowed values: generate_new, skip.")
			return
		}
	}

	// Extract File
	file, header, err := r.FormFile("file")
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing 'file' part in multipart form.")
		return
	}
	defer file.Close()

	// Ensure it's a zip file based on content-type
	if header.Header.Get("Content-Type") != "application/zip" && header.Header.Get("Content-Type") != "application/x-zip-compressed" {
		utils.RespondWithError(w, http.StatusUnsupportedMediaType, "Uploaded file must be a ZIP archive.")
		return
	}

	// Spool to Temporary File on Disk
	tempFilePath, ok := h.spoolImportArchive(w, file)
	if !ok {
		return
	}

	// Acquire bounded worker slot and launch background worker
	jobCtx, release, started := h.importManager().TryStart()
	if !started {
		_ = os.Remove(tempFilePath)
		utils.RespondWithError(w, http.StatusServiceUnavailable, "Import queue is busy or shutting down")
		return
	}
	go func() {
		defer release()
		h.processImportJob(jobCtx, db, user.Username, tempFilePath, importConfig)
	}()

	// Audit & Response
	h.Auditor.Log(r.Context(), "entries.import", user.Username, dbID.String(), map[string]any{"mode": importConfig.Mode})

	resp := ImportResponse{
		DatabaseID: dbID.String(),
		Message:    "Import job started successfully. The archive is being processed in the background.",
	}
	utils.RespondWithJSON(w, http.StatusAccepted, resp)
}

func (h *EntryHandler) spoolImportArchive(w http.ResponseWriter, file multipart.File) (string, bool) {
	tempFile, err := os.CreateTemp(os.TempDir(), "mh-import-*.zip")
	if err != nil {
		h.Logger.Error("Failed to create temporary file for import", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to spool upload to disk.")
		return "", false
	}
	tempFilePath := tempFile.Name()
	tempFile.Close() // Close immediately; we only need the generated name

	// Fast Path: Try to move the file directly if it's already on disk
	if f, ok := file.(*os.File); ok {
		srcPath := f.Name()
		_ = file.Close()
		if err := os.Rename(srcPath, tempFilePath); err == nil {
			return tempFilePath, true
		}
		if reopened, openErr := os.Open(srcPath); openErr == nil {
			file = reopened
			defer reopened.Close()
		}
	}

	// Slow Path: Copy if in-memory or cross-device rename failed
	destFile, err := os.OpenFile(tempFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		os.Remove(tempFilePath)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to spool upload to disk.")
		return "", false
	}

	_, _ = file.Seek(0, io.SeekStart)
	if _, err := io.Copy(destFile, file); err != nil {
		destFile.Close()
		os.Remove(tempFilePath)
		h.Logger.Error("Failed to copy uploaded file to temp file", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to spool upload to disk.")
		return "", false
	}
	destFile.Close()

	return tempFilePath, true
}
