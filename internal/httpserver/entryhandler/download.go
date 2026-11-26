package entryhandler

import (
	"fmt"
	"io"
	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
	"net/http"
)

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

	ioReader, err := h.Storage.ReadPreview(r.Context(), dbID, id)
	if err != nil {
		utils.RespondWithError(w, http.StatusNotFound, "Preview not found")
		return
	}
	defer ioReader.Close()

	setNoCacheHeaders(w)
	w.Header().Set("X-Content-Type-Options", "nosniff")

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
