package entryhandler

import (
	"errors"
	"fmt"
	"io"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/media"
	"mediahub_oss/internal/processing"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"net/http"
	"strconv"
	"strings"
)

// entryFilenameOrDefault returns the entry's filename or falls back to its numeric ID string if empty.
func entryFilenameOrDefault(entry repo.Entry) string {
	if entry.FileName != "" {
		return entry.FileName
	}
	return strconv.FormatInt(entry.ID, 10)
}

// parsePositiveQueryInt parses an optional positive integer (> 0) from query parameters.
// Returns 0 if the parameter is omitted.
func parsePositiveQueryInt(r *http.Request, key string) (int, error) {
	valStr := strings.TrimSpace(r.URL.Query().Get(key))
	if valStr == "" {
		return 0, nil
	}
	val, err := strconv.Atoi(valStr)
	if err != nil || val <= 0 {
		return 0, fmt.Errorf("Invalid %s: must be a positive integer.", key)
	}
	return val, nil
}

// parseAndValidateTransform parses optional transformation query parameters (format, width, height, fit),
// validates them against the database content type and converter capabilities,
// and writes the appropriate HTTP error if validation fails.
// Returns (opts, needsConversion, ok).
func (h *EntryHandler) parseAndValidateTransform(
	w http.ResponseWriter,
	r *http.Request,
	dbID repo.ULID,
	entry repo.Entry,
) (media.ConversionOptions, bool, bool) {
	q := r.URL.Query()
	formatParam := strings.TrimSpace(q.Get("format"))
	widthStr := strings.TrimSpace(q.Get("width"))
	heightStr := strings.TrimSpace(q.Get("height"))
	fitParam := strings.TrimSpace(q.Get("fit"))

	if formatParam == "" && widthStr == "" && heightStr == "" && fitParam == "" {
		return media.ConversionOptions{}, false, true
	}

	db, ok := h.getDatabaseOrRespond(w, r, dbID)
	if !ok {
		return media.ConversionOptions{}, false, false
	}

	switch db.ContentType {
	case "video":
		utils.RespondWithError(w, http.StatusNotImplemented, "Video transformation is not implemented.")
		return media.ConversionOptions{}, false, false
	case "file":
		utils.RespondWithError(w, http.StatusBadRequest, "Transformations are not supported for file databases.")
		return media.ConversionOptions{}, false, false
	case "audio":
		if widthStr != "" || heightStr != "" || fitParam != "" {
			utils.RespondWithError(w, http.StatusBadRequest, "Resolution and fit options are not supported for audio databases.")
			return media.ConversionOptions{}, false, false
		}
	case "image":
		// Image supports format, width, height, fit
	default:
		utils.RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("Transformations are not supported for database content type %q.", db.ContentType))
		return media.ConversionOptions{}, false, false
	}

	width, err := parsePositiveQueryInt(r, "width")
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return media.ConversionOptions{}, false, false
	}

	height, err := parsePositiveQueryInt(r, "height")
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return media.ConversionOptions{}, false, false
	}

	if fitParam != "" {
		switch fitParam {
		case "cut", "stretch", "pad-white", "pad-black":
			// valid
		default:
			utils.RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid fit option: %q. Allowed values: cut, stretch, pad-white, pad-black.", fitParam))
			return media.ConversionOptions{}, false, false
		}
	}

	var targetMimeType string
	if formatParam != "" {
		targetMimeType = media.NormalizeMimeType(formatParam)
	}

	convOpts := media.ConversionOptions{
		TargetMimeType: targetMimeType,
		Width:          width,
		Height:         height,
		Fit:            fitParam,
	}

	check := h.MediaConverter.CanConvert(entry.MimeType, convOpts)
	if !check.CanConvert {
		errTarget := targetMimeType
		if errTarget == "" {
			errTarget = entry.MimeType
		}
		utils.RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported conversion or transformation requested for %s.", errTarget))
		return media.ConversionOptions{}, false, false
	}

	return convOpts, check.NeedsConversion, true
}

// serveTransformedEntryFile converts the stored entry on-the-fly using a temporary file stream
// and streams the result (as JSON Base64 or binary attachment) to the client.
func (h *EntryHandler) serveTransformedEntryFile(
	w http.ResponseWriter,
	r *http.Request,
	dbID repo.ULID,
	entry repo.Entry,
	convOpts media.ConversionOptions,
) {
	origStream, err := h.Storage.Read(r.Context(), dbID.String(), entry.ID, 0, -1)
	if err != nil {
		utils.RespondWithError(w, http.StatusNotFound, "File content not found.")
		return
	}
	defer origStream.Close()

	convertedStream, convertedSize, err := h.Processor.ConvertStream(r.Context(), origStream, entry.MimeType, convOpts)
	if err != nil {
		if errors.Is(err, customerrors.ErrUnavailable) {
			utils.RespondWithError(w, http.StatusServiceUnavailable, "Server is currently at capacity for transformations. Try again later.")
			return
		}
		h.Logger.Error("Failed to convert stream on-the-fly", "entry", entry.ID, "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to transform media file.")
		return
	}
	defer convertedStream.Close()

	resultMimeType := convOpts.TargetMimeType
	if resultMimeType == "" {
		resultMimeType = media.NormalizeMimeType(entry.MimeType)
	}

	finalFileName := processing.ReplaceExtension(
		entryFilenameOrDefault(entry),
		processing.GetExtensionForMimeType(resultMimeType),
	)

	user := utils.GetUserFromContext(r.Context())
	h.Auditor.Log(r.Context(), "entry.download", user.Username, entryResourceID(dbID, entry.ID), nil)

	if wantsJSONResponse(r) {
		h.serveJSONFileStream(w, entry.ID, finalFileName, resultMimeType, uint64(convertedSize), convertedStream)
		return
	}

	w.Header().Set("Content-Type", resultMimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(convertedSize, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", finalFileName))
	w.WriteHeader(http.StatusOK)

	_, _ = io.Copy(w, convertedStream)
}

// serveStoredEntryFile streams the untransformed stored entry to the client,
// supporting JSON Base64 negotiation and HTTP Range requests (206 Partial Content).
func (h *EntryHandler) serveStoredEntryFile(
	w http.ResponseWriter,
	r *http.Request,
	dbID repo.ULID,
	entry repo.Entry,
) {
	if wantsJSONResponse(r) {
		fileStream, err := h.Storage.Read(r.Context(), dbID.String(), entry.ID, 0, -1)
		if err != nil {
			utils.RespondWithError(w, http.StatusNotFound, "File content not found.")
			return
		}
		defer fileStream.Close()

		user := utils.GetUserFromContext(r.Context())
		h.Auditor.Log(r.Context(), "entry.download", user.Username, entryResourceID(dbID, entry.ID), nil)

		h.serveJSONFileStream(w, entry.ID, entryFilenameOrDefault(entry), entry.MimeType, entry.Size, fileStream)
		return
	}

	rangeHeader := r.Header.Get("Range")
	fileSize := int64(entry.Size)

	var offset int64 = 0
	var length int64 = -1
	isPartial := false

	if rangeHeader != "" {
		ranges, err := parseRange(rangeHeader, fileSize)
		if err != nil {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
			utils.RespondWithError(w, http.StatusRequestedRangeNotSatisfiable, "Invalid Range Header")
			return
		}
		if len(ranges) > 0 {
			isPartial = true
			offset = ranges[0].start
			length = ranges[0].length
		}
	}

	fileStream, err := h.Storage.Read(r.Context(), dbID.String(), entry.ID, offset, length)
	if err != nil {
		utils.RespondWithError(w, http.StatusNotFound, "File content not found.")
		return
	}
	defer fileStream.Close()

	w.Header().Set("Content-Type", entry.MimeType)
	w.Header().Set("Accept-Ranges", "bytes")

	if isPartial {
		end := offset + length - 1
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, end, fileSize))
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		if entry.FileName != "" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", entry.FileName))
		}
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
		if entry.FileName != "" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", entry.FileName))
		}
		w.WriteHeader(http.StatusOK)
	}

	user := utils.GetUserFromContext(r.Context())
	h.Auditor.Log(r.Context(), "entry.download", user.Username, entryResourceID(dbID, entry.ID), nil)

	_, _ = io.Copy(w, fileStream)
}
