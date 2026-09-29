package entryhandler

import (
	"errors"
	"fmt"
	"io"
	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"net/http"
	"strconv"
	"strings"
)

func mapToPartialEntryResponse(dbID repo.ULID, entry repo.Entry) PartialEntryResponse {
	statusStr := repo.GetEntryStatusString(entry.Status)

	return PartialEntryResponse{
		DatabaseID:   dbID.String(),
		EntryID:      entry.ID,
		Status:       statusStr,
		Timestamp:    entry.Timestamp.UnixMilli(),
		CreatedAt:    entry.CreatedAt.UnixMilli(),
		UpdatedAt:    entry.UpdatedAt.UnixMilli(),
		MimeType:     entry.MimeType,
		CustomFields: entry.CustomFields,
	}
}

// Helper to map DB Entry to API Response
func mapToEntryResponse(dbID repo.ULID, entry repo.Entry) EntryResponse {
	statusStr := repo.GetEntryStatusString(entry.Status)

	return EntryResponse{
		DatabaseID:   dbID.String(),
		EntryID:      entry.ID,
		FileName:     entry.FileName,
		Size:         entry.Size,
		PreviewSize:  entry.PreviewSize,
		Status:       statusStr,
		Timestamp:    entry.Timestamp.UnixMilli(),
		CreatedAt:    entry.CreatedAt.UnixMilli(),
		UpdatedAt:    entry.UpdatedAt.UnixMilli(),
		MimeType:     entry.MimeType,
		MediaFields:  entry.MediaFields,
		CustomFields: entry.CustomFields,
	}
}

// mapEntriesToResponses converts a slice of repository entries into API response models.
func mapEntriesToResponses(dbID repo.ULID, entries []repo.Entry) []EntryResponse {
	results := make([]EntryResponse, 0, len(entries))
	for _, entry := range entries {
		results = append(results, mapToEntryResponse(dbID, entry))
	}
	return results
}

func (p SearchRequestPayload) toModel() repo.SearchRequest {
	req := repo.SearchRequest{
		Pagination: repo.Pagination{
			Offset: p.Pagination.Offset,
			Limit:  p.Pagination.Limit,
		},
	}

	// Map the Filter if it exists
	if p.Filter != nil {
		var conditions []repo.Condition
		for _, c := range p.Filter.Conditions {
			conditions = append(conditions, repo.Condition{
				Field:    c.Field,
				Operator: c.Operator,
				Value:    c.Value,
			})
		}

		req.Filter = &repo.FilterGroup{
			Operator:   p.Filter.Operator,
			Conditions: conditions,
		}
	}

	// Map the Sort criteria if it exists
	if p.Sort != nil {
		req.Sort = &repo.SortCriteria{
			Field:     p.Sort.Field,
			Direction: p.Sort.Direction,
		}
	}

	return req
}

// entryResourceID formats a composite audit resource identifier ("<database_id>:<entry_id>").
func entryResourceID(dbID repo.ULID, id int64) string {
	return fmt.Sprintf("%s:%d", dbID, id)
}

// parseDatabasePathParam extracts and validates the "database_id" path parameter.
// If missing, it writes an HTTP 400 error response and returns ok=false.
func parseDatabasePathParam(w http.ResponseWriter, r *http.Request) (repo.ULID, bool) {
	dbID := r.PathValue("database_id")
	if dbID == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: database_id")
		return "", false
	}
	return repo.ULID(dbID), true
}

// parseEntryPathParams extracts and validates "database_id" and "id" path parameters.
// If validation fails, it writes an HTTP 400 error response and returns ok=false.
func parseEntryPathParams(w http.ResponseWriter, r *http.Request) (repo.ULID, int64, bool) {
	dbID, ok := parseDatabasePathParam(w, r)
	if !ok {
		return "", 0, false
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid ID format.")
		return "", 0, false
	}
	return dbID, id, true
}

// getDatabaseOrRespond fetches a database by ID or writes an appropriate HTTP error response (404 or 500).
func (h *EntryHandler) getDatabaseOrRespond(w http.ResponseWriter, r *http.Request, dbID repo.ULID) (repo.Database, bool) {
	db, err := h.Repo.GetDatabase(r.Context(), dbID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database not found.")
		} else {
			h.Logger.Error("Failed to fetch database", "database_id", dbID, "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to fetch database. Error: %v", err))
		}
		return repo.Database{}, false
	}
	return db, true
}

// getEntryOrRespond fetches an entry by database ID and entry ID or writes an appropriate HTTP error response (404 or 500).
func (h *EntryHandler) getEntryOrRespond(w http.ResponseWriter, r *http.Request, dbID repo.ULID, id int64) (repo.Entry, bool) {
	entry, err := h.Repo.GetEntry(r.Context(), dbID, id)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database or entry not found.")
		} else {
			h.Logger.Error("Failed to get entry metadata", "database_id", dbID, "entry", id, "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get entry metadata. Error: %v", err))
		}
		return repo.Entry{}, false
	}
	return entry, true
}

// setNoCacheHeaders sets standard HTTP anti-caching headers on the response.
func setNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

// wantsJSONResponse checks if the client requested a JSON representation via the Accept header.
func wantsJSONResponse(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// serveJSONFileStream writes a 200 OK JSON Base64 file response from an io.Reader.
func (h *EntryHandler) serveJSONFileStream(w http.ResponseWriter, entryID int64, filename, mimeType string, size uint64, stream io.Reader) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := writeJSONFileResponseStream(w, filename, mimeType, size, stream); err != nil {
		h.Logger.Error("Failed to stream JSON file to client", "entry", entryID, "error", err)
	}
}
