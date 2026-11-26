package entryhandler

import (
	"errors"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/processing"
	"mediahub_oss/internal/shared/customerrors"
	"net/http"
)

// @Summary Upload an entry
// @Description Uploads a new entry to a specified database using multipart/form-data. The metadata part should be a JSON object containing the entry's timestamp, and any custom fields.
// @Description The 'file' part's 'filename' in the Content-Disposition header will be extracted and saved.
// @Description
// @Description This endpoint uses a hybrid model:
// @Description - **Small files (<= Configured Limit):** Processed synchronously. Returns `201 Created` with the full entry metadata.
// @Description - **Large files (> Configured Limit):** Processed asynchronously. Returns `202 Accepted` with a partial response. The client should poll `GET /api/entry/meta` until the `status` field is 'ready'.
// @Tags entry
// @Accept  mpfd
// @Produce  json
// @Param   database_id  path  string  true  "Database ID"
// @Param   metadata      formData  string  true  "JSON metadata for the entry"
// @Param   file          formData  file    true  "Entry file"
// @Success 201 {object} EntryResponse "For small files (synchronous processing)"
// @Success 202 {object} PartialEntryResponse "For large files (asynchronous processing)"
// @Failure 400 {object} utils.ErrorResponse "Invalid request"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 415 {object} utils.ErrorResponse "Unsupported entry format"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Router /database/{database_id}/entry [post]
func (h *EntryHandler) PostEntry(w http.ResponseWriter, r *http.Request) {
	dbID, ok := parseDatabasePathParam(w, r)
	if !ok {
		return
	}

	user := utils.GetUserFromContext(r.Context())

	db, ok := h.getDatabaseOrRespond(w, r, dbID)
	if !ok {
		return
	}

	// Read file into memory or store it on the file system
	maxMemory := h.MaxSyncUploadSizeBytes
	if maxMemory <= 0 {
		maxMemory = 8 << 20
	}

	if err := r.ParseMultipartForm(maxMemory); err != nil {
		h.Logger.Warn("Failed to parse multipart form", "error", err)
		utils.RespondWithError(w, http.StatusBadRequest, "Failed to parse multipart form.")
		return
	}
	defer func() {
		// if everyhting goes well, an appended large file is moved to a different folder for async processing
		// and this cleanup is not necessary. But in case of an issue, we need this.
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing 'file' part in multipart form.")
		return
	}
	defer file.Close()

	// Parse and validate metadata
	metadataStr := r.FormValue("metadata")
	if metadataStr == "" {
		h.Logger.Warn("Missing 'metadata' part in multipart form")
		utils.RespondWithError(w, http.StatusBadRequest, "Missing 'metadata' part in multipart form.")
		return
	}

	entry_request, err := parseUploadMetadata(metadataStr)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Error parsing file metadata: "+err.Error())
		return
	}

	err = validateCustomFields(entry_request.CustomFields, db.CustomFields)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Error validating custom fields: "+err.Error())
		return
	}

	// Call processor
	procReq := processing.EntryRequest{
		Timestamp:    entry_request.Timestamp,
		FileName:     entry_request.FileName,
		CustomFields: entry_request.CustomFields,
	}

	originalMime := header.Header.Get("Content-Type")
	originalName := header.Filename

	entry, wasSync, err := h.Processor.ProcessIncomingEntry(r.Context(), db, procReq, file, originalMime, originalName)
	if err != nil {
		if errors.Is(err, customerrors.ErrUnavailable) {
			utils.RespondWithError(w, http.StatusServiceUnavailable, "Service Unavailable: queue is full or processing capacity exhausted.")
		} else if errors.Is(err, customerrors.ErrBadMimeType) {
			utils.RespondWithError(w, http.StatusUnsupportedMediaType, err.Error())
		} else {
			h.Logger.Error("Processing failed", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	var responseObj EntryWithID
	status := http.StatusCreated
	if wasSync {
		responseObj = mapToEntryResponse(dbID, entry)
	} else {
		responseObj = mapToPartialEntryResponse(dbID, entry)
		status = http.StatusAccepted
	}

	// Audit & Response
	h.Auditor.Log(r.Context(), "entry.post", user.Username, entryResourceID(dbID, responseObj.GetID()), map[string]any{"database_name": db.Name})

	utils.RespondWithJSON(w, status, responseObj)
}
