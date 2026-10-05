package entryhandler

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/media"
	"mediahub_oss/internal/processing"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"net/http"
	"os"
	"strconv"
	"time"
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

// @Summary Delete an entry
// @Description Deletes an entry file from disk and its metadata from the database.
// @Tags entry
// @Produce json
// @Param   database_id  path  string  true  "Database ID"
// @Param   id      path  int     true  "Entry ID"
// @Success 200 {object} utils.MessageResponse "Success message"
// @Failure 400 {object} utils.ErrorResponse "Invalid request"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden"
// @Failure 404 {object} utils.ErrorResponse "Database or entry not found"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Router /database/{database_id}/entry/{id} [delete]
func (h *EntryHandler) DeleteEntry(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := parseEntryPathParams(w, r)
	if !ok {
		return
	}

	user := utils.GetUserFromContext(r.Context())

	// Delete using the Safe 2-Phase Approach
	_, err := shared.DeleteSafe(r.Context(), h.Repo, h.Storage, dbID, id)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database or entry not found.")
		} else {
			h.Logger.Error("Failed to safely delete entry", "database_id", dbID, "id", id, "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete the entry data. Error: %v", err))
		}
		return
	}

	// Audit & Response
	h.Auditor.Log(r.Context(), "entry.delete", user.Username, entryResourceID(dbID, id), nil)

	idStr := strconv.FormatInt(id, 10)
	h.Logger.Info("Entry deleted", "id", idStr, "database_id", dbID)
	utils.RespondWithJSON(w, http.StatusOK, utils.MessageResponse{Message: fmt.Sprintf("Entry '%s' from database '%s' was successfully deleted.", idStr, dbID)})
}

// @Summary Download entry file or transformed derivative
// @Description Retrieves the raw file or an on-the-fly transformed derivative. Supports Content Negotiation (JSON vs Binary), HTTP Range Requests (for raw files), and On-The-Fly Media Transformations.
// @Tags entry
// @Produce octet-stream
// @Produce json
// @Param   database_id   path      string  true   "Database ID"
// @Param   id            path      int64   true   "Entry ID"
// @Param   Range         header    string  false  "Byte range request (e.g., bytes=0-1023)"
// @Param   format        query     string  false  "Desired output format / MIME type (e.g. webp, jpeg, opus)"
// @Param   width         query     int     false  "Desired image width (> 0)"
// @Param   height        query     int     false  "Desired image height (> 0)"
// @Param   fit           query     string  false  "Scaling mode (cut, stretch, pad-white, pad-black)"
// @Success 200 {file} file "The full raw or transformed file data (default)"
// @Success 200 {object} FileJSONResponse "Base64 encoded file data (if Accept: application/json)"
// @Success 206 {file} file "Partial content (streaming response for untransformed files)"
// @Failure 400 {object} utils.ErrorResponse "Invalid request, parameters, or unsupported conversion"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden"
// @Failure 404 {object} utils.ErrorResponse "Database or entry not found"
// @Failure 409 {object} utils.ErrorResponse "File is currently processing"
// @Failure 416 {object} utils.ErrorResponse "Range Not Satisfiable"
// @Failure 501 {object} utils.ErrorResponse "Transformation not implemented for video"
// @Failure 503 {object} utils.ErrorResponse "Server busy (concurrency limit reached)"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Header 200,206 {string} Accept-Ranges "bytes"
// @Header 206 {string} Content-Range "bytes start-end/total"
// @Security BasicAuth
// @Security BearerAuth
// @Router /database/{database_id}/entry/{id}/file [get]
func (h *EntryHandler) GetEntryFile(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := parseEntryPathParams(w, r)
	if !ok {
		return
	}

	filemeta, ok := h.getEntryOrRespond(w, r, dbID, id)
	if !ok {
		return
	}

	// Do not serve files that are still processing!
	if filemeta.Status == repo.EntryStatusProcessing {
		utils.RespondWithError(w, http.StatusConflict, "File is currently being processed. Try again later.")
		return
	}

	convOpts, needsConversion, ok := h.parseAndValidateTransform(w, r, dbID, filemeta)
	if !ok {
		return
	}

	if needsConversion {
		h.serveTransformedEntryFile(w, r, dbID, filemeta, convOpts)
		return
	}

	h.serveStoredEntryFile(w, r, dbID, filemeta)
}

// @Summary Get entry metadata
// @Description Retrieves all metadata for a single entry, including custom fields.
// @Tags entry
// @Produce json
// @Param   database_id  path  string  true  "Database ID"
// @Param   id      path  int64   true  "Entry ID"
// @Success 200 {object} EntryResponse "The full entry metadata object"
// @Failure 400 {object} utils.ErrorResponse "Invalid request"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden"
// @Failure 404 {object} utils.ErrorResponse "Database or entry not found"
// @Security BasicAuth
// @Router /database/{database_id}/entry/{id} [get]
func (h *EntryHandler) GetEntryMeta(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := parseEntryPathParams(w, r)
	if !ok {
		return
	}

	filemeta, ok := h.getEntryOrRespond(w, r, dbID, id)
	if !ok {
		return
	}

	responseObject := mapToEntryResponse(dbID, filemeta)
	setNoCacheHeaders(w)

	user := utils.GetUserFromContext(r.Context())
	h.Auditor.Log(r.Context(), "entry.read_meta", user.Username, entryResourceID(dbID, id), nil)

	utils.RespondWithJSON(w, http.StatusOK, responseObject)
}

// @Summary Get an entry preview
// @Description Retrieves a 200x200 WebP preview of an entry. Supports Content Negotiation via Accept header.
// @Tags entry
// @Produce image/webp
// @Produce json
// @Param   database_id   path   string   true  "Database ID"
// @Param   id       path   int64    true  "Entry ID"
// @Success 200 {file} file "The WebP preview image (default)"
// @Success 200 {object} FileJSONResponse "Base64 encoded preview data (if Accept: application/json)"
// @Failure 400 {object} utils.ErrorResponse "Invalid request"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden"
// @Failure 404 {object} utils.ErrorResponse "Database, entry, or preview not found"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Router /database/{database_id}/entry/{id}/preview [get]
func (h *EntryHandler) GetEntryPreview(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := parseEntryPathParams(w, r)
	if !ok {
		return
	}

	ioReader, err := h.Storage.ReadPreview(r.Context(), dbID.String(), id)
	if err != nil {
		utils.RespondWithError(w, http.StatusNotFound, "Preview not found")
		return
	}
	defer ioReader.Close()

	setNoCacheHeaders(w)

	if wantsJSONResponse(r) {
		filename := fmt.Sprintf("%d_preview.webp", id)
		h.serveJSONFileStream(w, id, filename, "image/webp", 0, ioReader)
		return
	}

	w.Header().Set("Content-Type", "image/webp")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, ioReader); err != nil {
		h.Logger.Error("Failed to stream preview to client", "entry", id, "error", err)
	}
}

// @Summary Update entry metadata
// @Description Updates an entry's mutable metadata, including custom fields, the 'timestamp' and the 'filename'.
// @Tags entry
// @Accept json
// @Produce json
// @Param   database_id   path   string                true  "Database ID"
// @Param   id       path   int64                 true  "Entry ID"
// @Param   updates  body   PostPatchEntryRequest  true  "JSON object with fields to update"
// @Success 200 {object} EntryResponse "The full, updated entry metadata object"
// @Failure 400 {object} utils.ErrorResponse "Invalid request"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden"
// @Failure 404 {object} utils.ErrorResponse "Database or entry not found"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Router /database/{database_id}/entry/{id} [patch]
func (h *EntryHandler) PatchEntry(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := parseEntryPathParams(w, r)
	if !ok {
		return
	}

	user := utils.GetUserFromContext(r.Context())

	var req = PostPatchEntryRequest{
		FileName:     "",
		Timestamp:    math.MinInt64,
		CustomFields: nil,
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	defer r.Body.Close()

	db, ok := h.getDatabaseOrRespond(w, r, dbID)
	if !ok {
		return
	}

	existingEntry, ok := h.getEntryOrRespond(w, r, dbID, id)
	if !ok {
		return
	}

	// Apply Updates Safely (Ignoring Go zero-values)
	if req.FileName != "" {
		existingEntry.FileName = req.FileName
	}

	if req.Timestamp != math.MinInt64 {
		existingEntry.Timestamp = time.UnixMilli(req.Timestamp)
	}

	if req.CustomFields != nil {
		err := validateCustomFields(req.CustomFields, db.CustomFields)
		if err != nil {
			utils.RespondWithError(w, http.StatusBadRequest, "Error during custom field validation: "+err.Error())
			return
		}

		if existingEntry.CustomFields == nil {
			existingEntry.CustomFields = make(map[string]any)
		}
		for key, value := range req.CustomFields {
			existingEntry.CustomFields[key] = value
		}
	}

	updatedEntry, err := h.Repo.UpdateEntryMetadata(r.Context(), dbID, existingEntry)
	if err != nil {
		h.Logger.Error("Failed to update entry metadata", "entry", id, "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to apply updates to database.")
		return
	}

	h.Auditor.Log(r.Context(), "entry.update", user.Username, entryResourceID(dbID, id), nil)

	responseObject := mapToEntryResponse(dbID, updatedEntry)
	utils.RespondWithJSON(w, http.StatusOK, responseObject)
}

// @Summary Bulk delete entries
// @Description Deletes multiple entries in a single atomic transaction. Updates database statistics only once.
// @Tags database
// @Accept  json
// @Produce json
// @Param   database_id  path   string  true  "Database ID"
// @Param   body    body   BulkDeleteRequest true "JSON object containing a list of Entry IDs to delete"
// @Success 200 {object} BulkDeleteResponse "Summary of the deletion operation"
// @Failure 400 {object} utils.ErrorResponse "Invalid request, missing id, or empty IDs list"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden (Requires CanDelete role)"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 500 {object} utils.ErrorResponse "Transaction failed"
// @Security BasicAuth
// @Router /database/{database_id}/entries/delete [post]
func (h *EntryHandler) DeleteEntries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID, ok := parseDatabasePathParam(w, r)
	if !ok {
		return
	}
	user := utils.GetUserFromContext(ctx)

	var req BulkDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid request or empty IDs list")
		return
	}

	// 2. Delete the files and entries
	deletedMeta, err := shared.DeleteMultipleSafe(ctx, h.Repo, h.Storage, dbID, req.IDs)

	// 3. Calculate disk space freed
	var spaceFreed uint64 = 0
	var deletedCount = len(deletedMeta)
	for _, e := range deletedMeta {
		spaceFreed += e.Filesize + e.PreviewSize
	}

	// Safely extract the error message if one exists
	var errorMsg string
	if err != nil {
		errorMsg = err.Error()
	}

	// 4. Respond
	resp := BulkDeleteResponse{
		DatabaseID:      dbID.String(),
		DeletedCount:    deletedCount,
		SpaceFreedBytes: spaceFreed,
		Message:         fmt.Sprintf("Successfully deleted %d entries.", deletedCount),
		Errors:          errorMsg, // Safe to use now!
	}

	// check for internal status or user errors
	status := http.StatusOK
	if deletedCount == 0 && err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			status = http.StatusNotFound
		} else {
			status = http.StatusInternalServerError
		}
	}

	h.Auditor.Log(ctx, "entries.delete", user.Username, dbID.String(), map[string]any{"count": deletedCount})
	utils.RespondWithJSON(w, status, resp)
}

// @Summary Get entries from a database (basic)
// @Description Retrieves a paginated list of entries from a specific database. Only supports time-based filters.
// @Tags database
// @Produce json
// @Param   database_id  path   string  true   "Database ID"
// @Param   limit   query  int     false  "Number of entries to return (default 30)"
// @Param   offset  query  int     false  "Offset for pagination (default 0)"
// @Param   order   query  string  false  "Sort order ('asc' or 'desc', default 'desc')"
// @Param   sort_by query  string  false  "The field to sort the results by ('timestamp', 'created_at', 'updated_at', 'id', default 'timestamp')"
// @Param   time_field query string false  "The field that tstart and tend should filter against ('timestamp', 'created_at', 'updated_at', default 'timestamp')"
// @Param   tstart  query  int64   false  "Start timestamp (Unix milliseconds)"
// @Param   tend    query  int64   false  "End timestamp (Unix milliseconds)"
// @Success 200 {array} EntryResponse "Returns an array of entry metadata objects"
// @Failure 400 {object} utils.ErrorResponse "Missing id param or invalid parameter formats"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden (Requires CanView role)"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 500 {object} utils.ErrorResponse "Failed to retrieve entries"
// @Security BasicAuth
// @Router /database/{database_id}/entries [get]
func (h *EntryHandler) QueryEntries(w http.ResponseWriter, r *http.Request) {
	dbID, ok := parseDatabasePathParam(w, r)
	if !ok {
		return
	}

	user := utils.GetUserFromContext(r.Context())

	limit, err := utils.ParseQueryInt(r, "limit", 30)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, err := utils.ParseQueryInt(r, "offset", 0)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	order := r.URL.Query().Get("order")
	sortBy := r.URL.Query().Get("sort_by")
	timeField := r.URL.Query().Get("time_field")

	var tStart, tEnd time.Time
	tStartQuery, err := utils.ParseQueryInt64(r, "tstart", math.MinInt64)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tStartQuery != math.MinInt64 {
		tStart = time.UnixMilli(tStartQuery)
	}
	tEndQuery, err := utils.ParseQueryInt64(r, "tend", math.MaxInt64)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tEndQuery != math.MaxInt64 {
		tEnd = time.UnixMilli(tEndQuery)
	}

	opts := repo.QueryOptions{
		Limit:     limit,
		Offset:    offset,
		Order:     order,
		SortBy:    sortBy,
		TimeField: timeField,
		TStart:    tStart,
		TEnd:      tEnd,
	}

	if err := opts.Validate(); err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := h.Repo.GetEntries(r.Context(), dbID, opts)
	if err != nil {
		h.Logger.Error("Failed to query entries", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to retrieve entries")
		return
	}

	results := mapEntriesToResponses(dbID, entries)

	h.Auditor.Log(r.Context(), "entries.query", user.Username, dbID.String(), nil)
	utils.RespondWithJSON(w, http.StatusOK, results)
}

// @Summary Search for entries in a database (complex)
// @Description Retrieves a list of entry metadata matching the complex, nested filter criteria provided in the request body.
// @Tags database
// @Accept  json
// @Produce json
// @Param   database_id  path   string        true  "Database ID"
// @Param   search  body   repository.SearchRequest  true  "JSON body defining filter, sort, and pagination logic"
// @Success 200 {array} EntryResponse "Returns an array of matching results (even if empty)"
// @Failure 400 {object} utils.ErrorResponse "Missing id, invalid JSON, missing limit, or invalid filter/sort"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden (Requires CanView role)"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Router /database/{database_id}/entries/search [post]
func (h *EntryHandler) SearchEntries(w http.ResponseWriter, r *http.Request) {
	dbID, ok := parseDatabasePathParam(w, r)
	if !ok {
		return
	}

	user := utils.GetUserFromContext(r.Context())

	var searchPayload SearchRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&searchPayload); err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	db, ok := h.getDatabaseOrRespond(w, r, dbID)
	if !ok {
		return
	}

	if searchPayload.Pagination.Limit <= 0 {
		utils.RespondWithError(w, http.StatusBadRequest, "pagination.limit is required and must be greater than 0")
		return
	}

	searchReq := searchPayload.toModel()
	entries, err := h.Repo.SearchEntries(r.Context(), dbID, searchReq, db.CustomFields)
	if err != nil {
		if errors.Is(err, customerrors.ErrValidation) {
			utils.RespondWithError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("Search failed", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	results := mapEntriesToResponses(dbID, entries)

	h.Auditor.Log(r.Context(), "entries.search", user.Username, dbID.String(), nil)
	utils.RespondWithJSON(w, http.StatusOK, results)
}

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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
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

	go func() {
		defer pw.Close()

		zipWriter := zip.NewWriter(pw)
		defer zipWriter.Close()

		// 1. Create CSV file inside ZIP
		csvFile, err := zipWriter.Create("entries.csv")
		if err != nil {
			h.Logger.Error("Failed to create CSV in zip", "error", err)
			pw.CloseWithError(err)
			return
		}

		csvWriter := csv.NewWriter(csvFile)

		// --- Build dynamic CSV Header ---
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
		for _, id := range req.IDs {
			// Fetch metadata
			entry, err := h.Repo.GetEntry(workerCtx, dbID, id)
			if err != nil {
				h.Logger.Warn("Skipping entry in export (not found)", "id", id)
				continue
			}

			validEntries = append(validEntries, entry)

			// --- Build dynamic CSV Row ---
			row := []string{
				strconv.FormatInt(entry.ID, 10),
				entry.FileName,
				entry.Timestamp.Format(time.RFC3339),
				strconv.FormatUint(entry.Size, 10),
				strconv.FormatUint(entry.PreviewSize, 10),
				entry.MimeType,
				strconv.Itoa(int(entry.Status)),
			}

			// Append media field values safely
			for _, mf := range mediaFieldDefs {
				val, exists := entry.MediaFields[mf.Name]
				if !exists || val == nil {
					row = append(row, "") // Empty column if no value
				} else {
					row = append(row, fmt.Sprintf("%v", val))
				}
			}

			// Append custom field values safely
			for _, cf := range db.CustomFields {
				val, exists := entry.CustomFields[cf.Name]
				if !exists || val == nil {
					row = append(row, "") // Empty column if no value
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

			if err := csvWriter.Write(row); err != nil {
				// The ZIP stream is broken (e.g. client disconnected); abort the export.
				h.Logger.Warn("Aborting export: failed to write CSV row", "id", entry.ID, "error", err)
				return
			}
		}

		// Flush the CSV buffer to the zip file BEFORE creating new zip entries
		csvWriter.Flush()
		if err := csvWriter.Error(); err != nil {
			h.Logger.Warn("Aborting export: failed to flush CSV", "error", err)
			return
		}

		// Pass 2: Stream the files and previews into the ZIP
		for _, entry := range validEntries {
			// --- 1. Stream the Main File ---
			// Fetch file stream from storage
			fileStream, err := h.Storage.Read(workerCtx, dbID.String(), entry.ID, 0, -1)
			if err != nil {
				h.Logger.Warn("Failed to read file from storage for export", "id", entry.ID, "error", err)
				continue // If the main file fails, we skip this entry entirely
			}

			// Create file inside ZIP (user-controlled names are sanitized to
			// prevent path traversal on extraction, e.g. "../../evil.sh")
			zipEntryPath := fmt.Sprintf("files/%d_%s", entry.ID, sanitizeZipEntryName(entry.FileName))
			zipFile, err := zipWriter.Create(zipEntryPath)
			if err != nil {
				fileStream.Close()
				// Creating a zip entry only fails when the underlying stream is
				// broken (client gone); abort instead of spinning over the rest.
				h.Logger.Warn("Aborting export: failed to create zip entry for file", "id", entry.ID, "error", err)
				return
			}

			// Stream content into ZIP
			if _, err := io.Copy(zipFile, fileStream); err != nil {
				fileStream.Close()
				h.Logger.Warn("Aborting export: failed to stream file into zip", "id", entry.ID, "error", err)
				return
			}
			fileStream.Close()

			// --- 2. Stream the Preview File (if it exists) ---
			// We use the database metadata to quickly check if a preview was generated
			if entry.PreviewSize > 0 {
				previewStream, err := h.Storage.ReadPreview(workerCtx, dbID.String(), entry.ID)
				if err != nil {
					h.Logger.Warn("Failed to read preview from storage for export", "id", entry.ID, "error", err)
				} else {
					// Create preview file inside ZIP
					zipPreviewPath := fmt.Sprintf("previews/%d.webp", entry.ID)
					zipPreviewFile, err := zipWriter.Create(zipPreviewPath)
					if err != nil {
						previewStream.Close()
						h.Logger.Warn("Aborting export: failed to create zip entry for preview", "id", entry.ID, "error", err)
						return
					}
					// Stream preview content into ZIP
					if _, err := io.Copy(zipPreviewFile, previewStream); err != nil {
						previewStream.Close()
						h.Logger.Warn("Aborting export: failed to stream preview into zip", "id", entry.ID, "error", err)
						return
					}
					previewStream.Close()
				}
			}
		}
	}()

	h.Auditor.Log(r.Context(), "entries.export", user.Username, dbID.String(), map[string]any{"count": len(req.IDs)})

	// Stream the pipe reader directly to the response writer
	if _, err := io.Copy(w, pr); err != nil {
		h.Logger.Error("Failed to stream ZIP to client", "error", err)
	}
}

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

	// 2. Parse Multipart Form
	// Use the configured MaxSyncUploadSizeBytes to limit memory consumption during parsing
	if err := r.ParseMultipartForm(h.MaxSyncUploadSizeBytes); err != nil {
		h.Logger.Warn("Failed to parse multipart form for import", "error", err)
		utils.RespondWithError(w, http.StatusBadRequest, "Failed to parse multipart form.")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	// 3. Extract and Parse Config (with safe defaults)
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
		// Validate mode
		if importConfig.Mode != "generate_new" && importConfig.Mode != "skip" {
			utils.RespondWithError(w, http.StatusBadRequest, "Invalid 'mode' specified in config. Allowed values: generate_new, skip.")
			return
		}
	}

	// 4. Extract File
	file, header, err := r.FormFile("file")
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing 'file' part in multipart form.")
		return
	}
	defer file.Close()

	// Ensure it's a zip file based on content-type or extension
	if header.Header.Get("Content-Type") != "application/zip" && header.Header.Get("Content-Type") != "application/x-zip-compressed" {
		utils.RespondWithError(w, http.StatusUnsupportedMediaType, "Uploaded file must be a ZIP archive.")
		return
	}

	// 5. Spool to Temporary File on Disk
	tempFile, err := os.CreateTemp(os.TempDir(), "mh-import-*.zip")
	if err != nil {
		h.Logger.Error("Failed to create temporary file for import", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to spool upload to disk.")
		return
	}
	tempFilePath := tempFile.Name()
	tempFile.Close() // Close immediately, we just wanted the generated name

	// Fast Path: Try to move the file directly if it's already on disk
	moved := false
	if f, ok := file.(*os.File); ok {
		if err := os.Rename(f.Name(), tempFilePath); err == nil {
			moved = true
		}
	}

	// Slow Path: Fallback to copying if in-memory or cross-device rename failed
	if !moved {
		destFile, err := os.OpenFile(tempFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			os.Remove(tempFilePath)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to spool upload to disk.")
			return
		}

		// Reset the read pointer just in case
		file.Seek(0, io.SeekStart)
		if _, err := io.Copy(destFile, file); err != nil {
			destFile.Close()
			os.Remove(tempFilePath)
			h.Logger.Error("Failed to copy uploaded file to temp file", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to spool upload to disk.")
			return
		}
		destFile.Close()
	}

	// 6. Launch Background Worker
	// Pass context.Background() because the HTTP request context will cancel when we return the response
	go h.processImportJob(context.Background(), db, user.Username, tempFilePath, importConfig)

	// 7. Audit & Response
	h.Auditor.Log(r.Context(), "entries.import", user.Username, dbID.String(), map[string]any{"mode": importConfig.Mode})

	resp := ImportResponse{
		DatabaseID: dbID.String(),
		Message:    "Import job started successfully. The archive is being processed in the background.",
	}
	utils.RespondWithJSON(w, http.StatusAccepted, resp)
}
