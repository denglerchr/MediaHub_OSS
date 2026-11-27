package entryhandler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mime"
	"net/http"
	"os"
	"time"
)

const (
	previewLockTTL     = 30 * time.Second
	previewLockTimeout = 2 * time.Second
	previewBackupLimit = 10 << 20 // Matches processing.maxPreviewBytes for rollback backups
)

// @Summary Get an entry preview
// @Description Retrieves the generated or manually uploaded WebP preview of an entry. Supports Content Negotiation via Accept header.
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
// @Security BearerAuth
// @Router /database/{database_id}/entry/{id}/preview [get]
func (h *EntryHandler) GetEntryPreview(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := parseEntryPathParams(w, r)
	if !ok {
		return
	}

	ioReader, err := h.Storage.ReadPreview(r.Context(), dbID, id)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			utils.RespondWithError(w, http.StatusNotFound, "Preview not found")
			return
		}
		h.Logger.Error("Failed to read preview from storage", "database_id", dbID, "entry", id, "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to retrieve preview.")
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

// @Summary Create a custom entry preview
// @Description Adds a custom WebP preview to an existing entry if it does not already have one. Request body must be raw binary image/webp (max 128 KiB, max 200x200 px, static or animated). Supported for all database content types.
// @Tags entry
// @Accept image/webp
// @Produce json
// @Param   database_id   path   string   true  "Database ID"
// @Param   id            path   int64    true  "Entry ID"
// @Success 201 {object} PreviewUpdateResponse "Preview metadata after creation"
// @Failure 400 {object} utils.ErrorResponse "Invalid request, empty payload, malformed WebP, or invalid dimensions"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden"
// @Failure 404 {object} utils.ErrorResponse "Database or entry not found"
// @Failure 409 {object} utils.ErrorResponse "Entry already has a preview or is processing, queued, or deleting"
// @Failure 413 {object} utils.ErrorResponse "Preview payload exceeds 128 KiB"
// @Failure 415 {object} utils.ErrorResponse "Unsupported Content-Type or non-WebP payload"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Security BearerAuth
// @Router /database/{database_id}/entry/{id}/preview [post]
func (h *EntryHandler) PostEntryPreview(w http.ResponseWriter, r *http.Request) {
	h.writeEntryPreview(w, r, repo.PreviewUpdateCreateOnly, http.StatusCreated, "entry.preview_create")
}

// @Summary Create or replace a custom entry preview
// @Description Creates or replaces the WebP preview for an existing entry (upsert). Request body must be raw binary image/webp (max 128 KiB, max 200x200 px, static or animated). Supported for all database content types.
// @Tags entry
// @Accept image/webp
// @Produce json
// @Param   database_id   path   string   true  "Database ID"
// @Param   id            path   int64    true  "Entry ID"
// @Success 200 {object} PreviewUpdateResponse "Preview metadata after update"
// @Failure 400 {object} utils.ErrorResponse "Invalid request, empty payload, malformed WebP, or invalid dimensions"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden"
// @Failure 404 {object} utils.ErrorResponse "Database or entry not found"
// @Failure 409 {object} utils.ErrorResponse "Entry is processing, queued, or deleting"
// @Failure 413 {object} utils.ErrorResponse "Preview payload exceeds 128 KiB"
// @Failure 415 {object} utils.ErrorResponse "Unsupported Content-Type or non-WebP payload"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Security BearerAuth
// @Router /database/{database_id}/entry/{id}/preview [put]
func (h *EntryHandler) PutEntryPreview(w http.ResponseWriter, r *http.Request) {
	h.writeEntryPreview(w, r, repo.PreviewUpdateUpsert, http.StatusOK, "entry.preview_update")
}

// @Summary Delete an entry preview
// @Description Deletes the preview image of an existing entry and resets its preview_filesize to 0.
// @Tags entry
// @Produce json
// @Param   database_id   path   string   true  "Database ID"
// @Param   id            path   int64    true  "Entry ID"
// @Success 200 {object} utils.MessageResponse "Preview deleted"
// @Failure 400 {object} utils.ErrorResponse "Invalid request"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden"
// @Failure 404 {object} utils.ErrorResponse "Database, entry, or preview not found"
// @Failure 409 {object} utils.ErrorResponse "Entry is processing, queued, or deleting"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Security BearerAuth
// @Router /database/{database_id}/entry/{id}/preview [delete]
func (h *EntryHandler) DeleteEntryPreview(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := parseEntryPathParams(w, r)
	if !ok {
		return
	}

	unlock, locked := h.acquirePreviewLock(w, r.Context(), dbID, id)
	if !locked {
		return
	}
	defer unlock()

	_, oldSize, err := h.Repo.UpdateEntryPreviewSize(r.Context(), dbID, id, 0, repo.PreviewUpdateDeleteOnly)
	if err != nil {
		switch {
		case errors.Is(err, customerrors.ErrNotFound):
			utils.RespondWithError(w, http.StatusNotFound, "Database, entry, or preview not found.")
		case errors.Is(err, customerrors.ErrConflict):
			utils.RespondWithError(w, http.StatusConflict, "Preview cannot be deleted while entry is processing, queued, or deleting.")
		default:
			h.Logger.Error("Failed to delete entry preview metadata", "database_id", dbID, "entry", id, "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to delete entry preview metadata.")
		}
		return
	}

	if err := h.Storage.DeletePreview(r.Context(), dbID, id); err != nil && !errors.Is(err, customerrors.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
		defer cancel()
		if _, _, restoreErr := h.Repo.UpdateEntryPreviewSize(cleanupCtx, dbID, id, oldSize, repo.PreviewUpdateUpsert); restoreErr != nil {
			h.Logger.Error("Failed to restore entry preview metadata after storage delete failure", "database_id", dbID, "entry", id, "error", restoreErr)
		}
		h.Logger.Error("Failed to delete preview from storage", "database_id", dbID, "entry", id, "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to delete preview from storage.")
		return
	}

	user := utils.GetUserFromContext(r.Context())
	h.Auditor.Log(r.Context(), "entry.preview_delete", user.Username, entryResourceID(dbID, id), map[string]any{
		"preview_filesize_freed": oldSize,
	})

	utils.RespondWithJSON(w, http.StatusOK, utils.MessageResponse{
		Message: fmt.Sprintf("Preview for entry '%d' in database '%s' was successfully deleted.", id, dbID.String()),
	})
}

func (h *EntryHandler) acquirePreviewLock(w http.ResponseWriter, ctx context.Context, dbID repo.ULID, id int64) (func(), bool) {
	key := entryResourceID(dbID, id)

	h.previewMu.Lock()
	if h.previewLocks == nil {
		h.previewLocks = make(map[string]*previewEntryLock)
	}
	entryLock := h.previewLocks[key]
	if entryLock == nil {
		entryLock = &previewEntryLock{}
		h.previewLocks[key] = entryLock
	}
	entryLock.refs++
	h.previewMu.Unlock()

	releaseLocal := func() {
		entryLock.mu.Unlock()
		h.previewMu.Lock()
		entryLock.refs--
		if entryLock.refs == 0 && h.previewLocks[key] == entryLock {
			delete(h.previewLocks, key)
		}
		h.previewMu.Unlock()
	}

	entryLock.mu.Lock()

	// Also serialize across instances using the repository's distributed lock.
	distKey := "preview:" + key
	ownerID := ulid.New().String()
	deadline := time.Now().Add(previewLockTimeout)
	for {
		acquired, err := h.Repo.AcquireLock(ctx, distKey, ownerID, previewLockTTL)
		if err != nil {
			releaseLocal()
			if ctx.Err() != nil {
				utils.RespondWithError(w, http.StatusRequestTimeout, "Request cancelled while waiting for preview lock.")
				return nil, false
			}
			h.Logger.Error("Failed to acquire preview lock", "database_id", dbID, "entry", id, "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to acquire preview lock.")
			return nil, false
		}
		if acquired {
			break
		}
		if time.Now().After(deadline) {
			releaseLocal()
			utils.RespondWithError(w, http.StatusConflict, "Another preview operation is currently in progress for this entry.")
			return nil, false
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			releaseLocal()
			utils.RespondWithError(w, http.StatusRequestTimeout, "Request cancelled while waiting for preview lock.")
			return nil, false
		case <-timer.C:
		}
	}

	return func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, _ = h.Repo.ReleaseLock(releaseCtx, distKey, ownerID)
		cancel()
		releaseLocal()
	}, true
}

func isPreviewBlockedStatus(status repo.EntryStatus) bool {
	return status == repo.EntryStatusProcessing || status == repo.EntryStatusQueued || status == repo.EntryStatusDeleting
}

func respondPreviewValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, media.ErrPreviewTooLarge):
		utils.RespondWithError(w, http.StatusRequestEntityTooLarge, "Preview exceeds maximum allowed size of 128 KiB.")
	case errors.Is(err, media.ErrPreviewNotWebP):
		utils.RespondWithError(w, http.StatusUnsupportedMediaType, "Preview must be a valid WebP image (image/webp).")
	case errors.Is(err, media.ErrPreviewEmpty):
		utils.RespondWithError(w, http.StatusBadRequest, "Preview payload must not be empty.")
	case errors.Is(err, media.ErrPreviewMalformed):
		utils.RespondWithError(w, http.StatusBadRequest, "Preview is not a valid WebP image bitstream.")
	case errors.Is(err, media.ErrPreviewDimensions):
		utils.RespondWithError(w, http.StatusBadRequest, "Preview dimensions must be between 1x1 and 200x200 pixels.")
	default:
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to validate preview payload.")
	}
}

func readValidatedWebPBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "image/webp" {
		utils.RespondWithError(w, http.StatusUnsupportedMediaType, "Content-Type must be image/webp.")
		return nil, false
	}

	if r.Body == nil {
		respondPreviewValidationError(w, media.ErrPreviewEmpty)
		return nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, media.MaxPreviewBytes+1)
	previewBytes, _, _, err := media.ReadAndValidatePreview(r.Body)
	if err != nil {
		respondPreviewValidationError(w, err)
		return nil, false
	}
	return previewBytes, true
}

func (h *EntryHandler) readPreviewBackup(ctx context.Context, dbID repo.ULID, id int64) ([]byte, bool, error) {
	reader, err := h.Storage.ReadPreview(ctx, dbID, id)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer reader.Close()

	data, err := io.ReadAll(io.LimitReader(reader, previewBackupLimit+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > previewBackupLimit {
		return nil, false, nil
	}
	return data, true, nil
}

func (h *EntryHandler) writeEntryPreview(w http.ResponseWriter, r *http.Request, mode repo.PreviewUpdateMode, successStatus int, auditAction string) {
	dbID, id, ok := parseEntryPathParams(w, r)
	if !ok {
		return
	}

	previewBytes, ok := readValidatedWebPBody(w, r)
	if !ok {
		return
	}

	unlock, locked := h.acquirePreviewLock(w, r.Context(), dbID, id)
	if !locked {
		return
	}
	defer unlock()

	preState, err := h.Repo.GetEntryPreviewState(r.Context(), dbID, id)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database or entry not found.")
			return
		}
		h.Logger.Error("Failed to get entry preview state", "database_id", dbID, "entry", id, "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to inspect entry preview state.")
		return
	}

	if isPreviewBlockedStatus(preState.Status) {
		utils.RespondWithError(w, http.StatusConflict, "Preview cannot be modified while entry is processing, queued, or deleting.")
		return
	}
	if mode == repo.PreviewUpdateCreateOnly && preState.PreviewSize > 0 {
		utils.RespondWithError(w, http.StatusConflict, "Entry already has a preview; use PUT to replace it.")
		return
	}

	var backupBytes []byte
	var hasBackup bool
	if mode == repo.PreviewUpdateUpsert && preState.PreviewSize > 0 {
		backupBytes, hasBackup, err = h.readPreviewBackup(r.Context(), dbID, id)
		if err != nil {
			h.Logger.Error("Failed to back up existing preview before replacement", "database_id", dbID, "entry", id, "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to read existing preview before replacement.")
			return
		}
	}

	writtenSize, err := h.Storage.WritePreview(r.Context(), dbID, id, bytes.NewReader(previewBytes))
	if err != nil {
		h.Logger.Error("Failed to write preview to storage", "database_id", dbID, "entry", id, "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to write preview to storage.")
		return
	}

	state, oldSize, err := h.Repo.UpdateEntryPreviewSize(r.Context(), dbID, id, uint64(writtenSize), mode)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
		defer cancel()

		// Because this goroutine holds the per-entry preview lock, rolling back storage
		// is safe even if UpdateEntryPreviewSize failed with ErrConflict (e.g. DeleteSafe).
		if preState.PreviewSize == 0 {
			if delErr := h.Storage.DeletePreview(cleanupCtx, dbID, id); delErr != nil && !errors.Is(delErr, customerrors.ErrNotFound) && !errors.Is(delErr, os.ErrNotExist) {
				h.Logger.Error("Failed to roll back orphaned preview after DB error", "database_id", dbID, "entry", id, "error", delErr)
			}
		} else if hasBackup {
			if _, restoreErr := h.Storage.WritePreview(cleanupCtx, dbID, id, bytes.NewReader(backupBytes)); restoreErr != nil {
				h.Logger.Error("Failed to restore previous preview after DB error", "database_id", dbID, "entry", id, "error", restoreErr)
			}
		}

		switch {
		case errors.Is(err, customerrors.ErrNotFound):
			utils.RespondWithError(w, http.StatusNotFound, "Database or entry not found.")
		case errors.Is(err, customerrors.ErrConflict):
			utils.RespondWithError(w, http.StatusConflict, "Preview cannot be modified in the entry's current state.")
		default:
			h.Logger.Error("Failed to update entry preview metadata", "database_id", dbID, "entry", id, "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to update entry preview metadata.")
		}
		return
	}

	user := utils.GetUserFromContext(r.Context())
	details := map[string]any{
		"preview_filesize": state.PreviewSize,
	}
	if mode == repo.PreviewUpdateUpsert {
		details["previous_preview_filesize"] = oldSize
	}
	h.Auditor.Log(r.Context(), auditAction, user.Username, entryResourceID(dbID, id), details)

	utils.RespondWithJSON(w, successStatus, PreviewUpdateResponse{
		DatabaseID:  dbID.String(),
		EntryID:     id,
		PreviewSize: state.PreviewSize,
		UpdatedAt:   state.UpdatedAt.UnixMilli(),
	})
}
