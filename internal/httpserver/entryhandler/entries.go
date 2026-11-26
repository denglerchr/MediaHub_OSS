package entryhandler

import (
	"errors"
	"fmt"
	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"net/http"
	"strconv"
	"time"
)

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
		Timestamp:    shared.MissingTimestamp,
		CustomFields: nil,
	}

	if status, msg := utils.DecodeJSONBody(w, r, &req); status != 0 {
		utils.RespondWithError(w, status, msg)
		return
	}

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

	if req.Timestamp != shared.MissingTimestamp {
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
	if status, msg := utils.DecodeJSONBody(w, r, &req); status != 0 {
		utils.RespondWithError(w, status, msg)
		return
	}
	if len(req.IDs) == 0 {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid request or empty IDs list")
		return
	}

	// Delete the files and entries
	deletedMeta, err := shared.DeleteMultipleSafe(ctx, h.Repo, h.Storage, dbID, req.IDs)

	// Calculate disk space freed
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

	// Respond
	resp := BulkDeleteResponse{
		DatabaseID:      dbID.String(),
		DeletedCount:    deletedCount,
		SpaceFreedBytes: spaceFreed,
		Message:         fmt.Sprintf("Successfully deleted %d entries.", deletedCount),
		Errors:          errorMsg,
	}

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
// @Description Retrieves a paginated list of entries from a specific database. Supports time-based and ID-range filters.
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
// @Param   idstart query  int64   false  "Inclusive start entry ID (id >= idstart)"
// @Param   idend   query  int64   false  "Inclusive end entry ID (id <= idend)"
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
	tStartQuery, err := utils.ParseQueryInt64(r, "tstart", shared.MissingTimestamp)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tStartQuery != shared.MissingTimestamp {
		tStart = time.UnixMilli(tStartQuery)
	}
	tEndQuery, err := utils.ParseQueryInt64(r, "tend", shared.MissingTimestamp)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tEndQuery != shared.MissingTimestamp {
		tEnd = time.UnixMilli(tEndQuery)
	}

	idStart, err := utils.ParseQueryInt64(r, "idstart", 0)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	idEnd, err := utils.ParseQueryInt64(r, "idend", 0)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	opts := repo.QueryOptions{
		Limit:     limit,
		Offset:    offset,
		Order:     order,
		SortBy:    sortBy,
		TimeField: timeField,
		TStart:    tStart,
		TEnd:      tEnd,
		IDStart:   idStart,
		IDEnd:     idEnd,
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
	if status, msg := utils.DecodeJSONBody(w, r, &searchPayload); status != 0 {
		utils.RespondWithError(w, status, msg)
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
	if searchPayload.Pagination.Limit > repo.MaxQueryLimit {
		utils.RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("pagination.limit cannot exceed %d", repo.MaxQueryLimit))
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
