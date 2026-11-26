package databasehandler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/shared/customerrors"
)

// GetFields retrieves all custom fields for a database.
func (h *DatabaseHandler) GetFields(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID, ok := parseDatabaseIDParam(w, r)
	if !ok {
		return
	}

	fields, err := h.Repo.GetCustomFields(ctx, dbID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database not found.")
			return
		}
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get custom fields: %v", err))
		return
	}

	resp := mapCustomFieldsToDTO(fields)

	utils.RespondWithJSON(w, http.StatusOK, resp)
}

// AddField adds a custom field.
func (h *DatabaseHandler) AddField(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID, ok := parseDatabaseIDParam(w, r)
	if !ok {
		return
	}

	var payload DatabaseCustomField
	if status, msg := utils.DecodeJSONBody(w, r, &payload); status != 0 {
		utils.RespondWithError(w, status, msg)
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
	added, err := h.Repo.AddCustomField(ctx, dbID, modelField)
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

	utils.RespondWithJSON(w, http.StatusCreated, mapCustomFieldToDTO(added))
}

// UpdateField updates a custom field.
func (h *DatabaseHandler) UpdateField(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID, ok := parseDatabaseIDParam(w, r)
	if !ok {
		return
	}

	fieldID, ok := parseFieldIDParam(w, r)
	if !ok {
		return
	}

	var payload struct {
		Name      *string `json:"name"`
		IsIndexed *bool   `json:"is_indexed"`
	}
	if status, msg := utils.DecodeJSONBody(w, r, &payload); status != 0 {
		utils.RespondWithError(w, status, msg)
		return
	}

	if payload.Name == nil && payload.IsIndexed == nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Empty update payload")
		return
	}

	// FE-049: a rename must not land on a reserved standard/media field name either
	// (same rule as AddField / DatabaseCustomField.toModel).
	if payload.Name != nil && IsReservedCustomFieldName(*payload.Name) {
		utils.RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("custom field name '%s' is reserved (standard or media field)", strings.TrimSpace(*payload.Name)))
		return
	}

	updated, err := h.Repo.UpdateCustomField(ctx, dbID, fieldID, payload.Name, payload.IsIndexed)
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

	utils.RespondWithJSON(w, http.StatusOK, mapCustomFieldToDTO(updated))
}

// DeleteField deletes a custom field.
func (h *DatabaseHandler) DeleteField(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbID, ok := parseDatabaseIDParam(w, r)
	if !ok {
		return
	}

	fieldID, ok := parseFieldIDParam(w, r)
	if !ok {
		return
	}

	// Fetch current fields to find the name for response message
	fields, err := h.Repo.GetCustomFields(ctx, dbID)
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

	err = h.Repo.DeleteCustomField(ctx, dbID, fieldID)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "Database or field not found.")
			return
		}
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete field: %v", err))
		return
	}

	db, err := h.Repo.GetDatabase(ctx, dbID)
	dbName := "Database"
	if err == nil {
		dbName = db.Name
	}

	resp := map[string]string{
		"message": fmt.Sprintf("Field '%s' (ID: %d) was successfully deleted from database '%s'.", fieldName, fieldID, dbName),
	}

	utils.RespondWithJSON(w, http.StatusOK, resp)
}
