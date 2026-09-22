package databasehandler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// GetFields retrieves all custom fields for a database.
func (h *DatabaseHandler) GetFields(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID := r.PathValue("database_id")
	if dbID == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: database_id")
		return
	}

	fields, err := h.Repo.GetCustomFields(ctx, repository.ULID(dbID))
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database not found.")
			return
		}
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get custom fields: %v", err))
		return
	}

	resp := make([]DatabaseCustomField, len(fields))
	for i, f := range fields {
		idVal := f.ID
		isIndexedVal := f.IsIndexed
		resp[i] = DatabaseCustomField{
			ID:        &idVal,
			Name:      f.Name,
			Type:      f.Type.String(),
			IsIndexed: &isIndexedVal,
		}
	}

	utils.RespondWithJSON(w, http.StatusOK, resp)
}

// AddField adds a custom field.
func (h *DatabaseHandler) AddField(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID := r.PathValue("database_id")
	if dbID == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: database_id")
		return
	}

	var payload DatabaseCustomField
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if payload.Name == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required field: name")
		return
	}
	if payload.Type == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required field: type")
		return
	}

	modelField, err := payload.toModel()
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	added, err := h.Repo.AddCustomField(ctx, repository.ULID(dbID), modelField)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database not found.")
			return
		}
		if errors.Is(err, customerrors.ErrConflict) {
			utils.RespondWithError(w, http.StatusConflict, "A field with the requested name already exists.")
			return
		}
		if errors.Is(err, customerrors.ErrValidation) || strings.Contains(err.Error(), "unsupported") {
			utils.RespondWithError(w, http.StatusBadRequest, err.Error())
			return
		}
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to add field: %v", err))
		return
	}

	idVal := added.ID
	isIndexedVal := added.IsIndexed
	resp := DatabaseCustomField{
		ID:        &idVal,
		Name:      added.Name,
		Type:      added.Type.String(),
		IsIndexed: &isIndexedVal,
	}

	utils.RespondWithJSON(w, http.StatusCreated, resp)
}

// UpdateField updates a custom field.
func (h *DatabaseHandler) UpdateField(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID := r.PathValue("database_id")
	if dbID == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: database_id")
		return
	}

	fieldIDStr := r.PathValue("field_id")
	fieldID, err := strconv.Atoi(fieldIDStr)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid field_id path parameter")
		return
	}

	var payload struct {
		Name      *string `json:"name"`
		IsIndexed *bool   `json:"is_indexed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if payload.Name == nil && payload.IsIndexed == nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Empty update payload")
		return
	}

	updated, err := h.Repo.UpdateCustomField(ctx, repository.ULID(dbID), fieldID, payload.Name, payload.IsIndexed)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database or field not found.")
			return
		}
		if errors.Is(err, customerrors.ErrConflict) {
			utils.RespondWithError(w, http.StatusConflict, "The new field name is already in use by another field.")
			return
		}
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update field: %v", err))
		return
	}

	idVal := updated.ID
	isIndexedVal := updated.IsIndexed
	resp := DatabaseCustomField{
		ID:        &idVal,
		Name:      updated.Name,
		Type:      updated.Type.String(),
		IsIndexed: &isIndexedVal,
	}

	utils.RespondWithJSON(w, http.StatusOK, resp)
}

// DeleteField deletes a custom field.
func (h *DatabaseHandler) DeleteField(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID := r.PathValue("database_id")
	if dbID == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: database_id")
		return
	}

	fieldIDStr := r.PathValue("field_id")
	fieldID, err := strconv.Atoi(fieldIDStr)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid field_id path parameter")
		return
	}

	// Fetch current fields to find the name for response message
	fields, err := h.Repo.GetCustomFields(ctx, repository.ULID(dbID))
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database not found.")
			return
		}
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get custom fields: %v", err))
		return
	}

	var fieldName string
	for _, f := range fields {
		if f.ID == fieldID {
			fieldName = f.Name
			break
		}
	}
	if fieldName == "" {
		utils.RespondWithError(w, http.StatusNotFound, "Field not found.")
		return
	}

	err = h.Repo.DeleteCustomField(ctx, repository.ULID(dbID), fieldID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database or field not found.")
			return
		}
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete field: %v", err))
		return
	}

	db, err := h.Repo.GetDatabase(ctx, repository.ULID(dbID))
	dbName := "Database"
	if err == nil {
		dbName = db.Name
	}

	resp := map[string]string{
		"message": fmt.Sprintf("Field '%s' (ID: %d) was successfully deleted from database '%s'.", fieldName, fieldID, dbName),
	}

	utils.RespondWithJSON(w, http.StatusOK, resp)
}
