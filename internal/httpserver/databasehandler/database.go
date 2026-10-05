package databasehandler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
)

// @Summary Get database details
// @Description Retrieves details, custom fields, and statistics for a specific database.
// @Tags database
// @Produce  json
// @Param    database_id  path  string  true  "Database ID"
// @Success 200 {object} DatabaseResponse
// @Failure 400 {object} utils.ErrorResponse "Missing id path parameter"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 500 {object} utils.ErrorResponse "Failed to calculate stats"
// @Security BasicAuth
// @Router /database/{database_id} [get]
func (h *DatabaseHandler) GetDatabase(w http.ResponseWriter, r *http.Request) {
	var ctx = r.Context()

	user := utils.GetUserFromContext(ctx)

	id := r.PathValue("database_id")
	if id == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: database_id")
		return
	}

	db, err := h.Repo.GetDatabase(ctx, repository.ULID(id))
	if errors.Is(err, customerrors.ErrNotFound) {
		utils.RespondWithError(w, http.StatusNotFound, "Database not found.")
		return
	} else if errors.Is(err, customerrors.ErrValidation) {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid database id.")
		return
	} else if err != nil {
		h.Logger.Error("error retrieving database", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to retrieve database")
		return
	}

	h.Auditor.Log(ctx, "database.get", user.Username, id, map[string]any{"name": db.Name})

	dbResp := mapToDatabaseResponse(db)
	utils.RespondWithJSON(w, http.StatusOK, dbResp)
}

// @Summary List all databases
// @Description Retrieves a list of all available databases and their statistics.
// @Tags database
// @Produce  json
// @Success 200 {array} DatabaseResponse "Returns an empty array if no databases exist"
// @Failure 500 {object} utils.ErrorResponse "Failed to retrieve databases"
// @Security BasicAuth
// @Router /databases [get]
func (h *DatabaseHandler) GetDatabases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := utils.GetUserFromContext(ctx)

	// 1. Fetch all databases from the repository
	dbs, err := h.Repo.GetDatabases(ctx)
	if err != nil {
		h.Logger.Error("Failed to retrieve databases.", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to retrieve databases. Error: %v", err))
		return
	}

	// 2. Filter for non-admin users based on database-level permissions
	holder := utils.GetPermissionHolderFromContext(ctx)
	if !holder.IsGlobalAdmin() {
		permsMap, err := holder.GetAllPermissions(ctx)
		if err != nil {
			h.Logger.Error("Failed to retrieve user permissions.", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to retrieve user permissions")
			return
		}

		// Build an O(1) lookup map of databases the user is allowed to see using the ULID
		allowedDBs := make(map[string]bool)
		for dbID, perm := range permsMap {
			if perm != 0 {
				allowedDBs[dbID.String()] = true
			}
		}

		// Filter the original database list into a new slice
		var filteredDBs []repository.Database
		for _, db := range dbs {
			if allowedDBs[db.ID.String()] {
				filteredDBs = append(filteredDBs, db)
			}
		}

		// Ensure we return an empty array [] instead of null if no DBs matched
		if filteredDBs == nil {
			filteredDBs = []repository.Database{}
		}

		dbs = filteredDBs
	}

	// Convert to DatabaseResponse
	var resp = make([]DatabaseResponse, len(dbs))
	for i, db := range dbs {
		resp[i] = mapToDatabaseResponse(db)
	}

	// Audit
	h.Auditor.Log(ctx, "databases.get", user.Username, "repository", nil)
	utils.RespondWithJSON(w, http.StatusOK, resp)
}

// @Summary Create a new database
// @Description Creates a new database with custom fields and a dedicated entry table.
// @Tags database
// @Accept   json
// @Produce  json
// @Param    database  body  DatabaseCreatePayload  true  "Database Metadata"
// @Success 201 {object} DatabaseResponse
// @Failure 400 {object} utils.ErrorResponse "Invalid request payload or missing name"
// @Failure 409 {object} utils.ErrorResponse "Database name already in use"
// @Failure 500 {object} utils.ErrorResponse "Failed to create database or storage folder"
// @Security BasicAuth
// @Router /database [post]
func (h *DatabaseHandler) CreateDatabase(w http.ResponseWriter, r *http.Request) {
	var payload DatabaseCreatePayload
	var ctx = r.Context()

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.Logger.Warn("Failed to decode request body", "error", err)
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if payload.Name == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required field: name")
		return
	}
	if payload.ContentType == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required field: content_type")
		return
	}

	user := utils.GetUserFromContext(ctx)

	// Create the database
	database, err := payload.toModel()
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	createdDB, err := h.Repo.CreateDatabase(ctx, database)
	if err != nil {
		if errors.Is(err, customerrors.ErrDatabaseExists) {
			utils.RespondWithError(w, http.StatusConflict, "Database name already in use.")
		} else if errors.Is(err, customerrors.ErrInvalidName) || errors.Is(err, customerrors.ErrValidation) {
			utils.RespondWithError(w, http.StatusBadRequest, err.Error())
		} else {
			h.Logger.Error("Failed to create database.", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create database. Error: %v", err))
		}
		return
	}

	// Audit Log
	h.Auditor.Log(ctx, "database.create", user.Username, createdDB.ID.String(), map[string]any{
		"name":         createdDB.Name,
		"content_type": createdDB.ContentType,
	})

	resp := mapToDatabaseResponse(createdDB)
	utils.RespondWithJSON(w, http.StatusCreated, resp)
}

// @Summary Update database housekeeping rules or rename
// @Description Updates the mutable configuration fields for a specific database, including its name.
// @Tags database
// @Accept   json
// @Produce  json
// @Param    database_id  path  string  true  "Database ID"
// @Param    housekeeping  body  DatabaseUpdatePayload  true  "Configuration and Housekeeping Rules"
// @Success 200 {object} DatabaseResponse
// @Failure 400 {object} utils.ErrorResponse "Invalid request payload or missing id path parameter"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 500 {object} utils.ErrorResponse "Failed to update database"
// @Security BasicAuth
// @Router /database/{database_id} [put]
func (h *DatabaseHandler) UpdateDatabase(w http.ResponseWriter, r *http.Request) {

	var ctx = r.Context()

	// Parse request
	id := r.PathValue("database_id")
	if id == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: id")
		return
	}

	db, err := h.Repo.GetDatabase(ctx, repository.ULID(id))
	if errors.Is(err, customerrors.ErrNotFound) {
		utils.RespondWithError(w, http.StatusNotFound, "Database not found.")
		return
	} else if err != nil {
		h.Logger.Error("error retrieving database", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Error retrieving database. Error: %v", err))
		return
	}

	user := utils.GetUserFromContext(ctx)

	var updates DatabaseUpdatePayload
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	// update database (Apply new name if provided)
	if updates.Name != "" {
		db.Name = updates.Name
	}
	db.NMaxQueued = updates.NMaxQueued
	db.Config = updates.getConfig()
	db.Housekeeping = updates.getHK(db.Housekeeping.LastHkRun)

	updatedDB, err := h.Repo.UpdateDatabase(ctx, db)
	if err != nil {
		if errors.Is(err, customerrors.ErrDatabaseExists) {
			utils.RespondWithError(w, http.StatusConflict, "Database name already in use.")
		} else {
			utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Error updating database: %v", err))
		}
		return
	}

	// Audit Log
	h.Auditor.Log(ctx, "database.update", user.Username, updatedDB.ID.String(), nil)

	resp := mapToDatabaseResponse(updatedDB)
	utils.RespondWithJSON(w, http.StatusOK, resp)
}

// @Summary Delete a database
// @Description Deletes a database, its entry table, and all of its associated entries and metadata.
// @Tags database
// @Produce  json
// @Param    database_id  path  string  true  "Database ID"
// @Success 200 {object} utils.MessageResponse "Success message"
// @Failure 400 {object} utils.ErrorResponse "Missing database_id path parameter"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 500 {object} utils.ErrorResponse "Failed to delete database record or folder"
// @Security BasicAuth
// @Router /database/{database_id} [delete]
func (h *DatabaseHandler) DeleteDatabase(w http.ResponseWriter, r *http.Request) {
	var ctx = r.Context()

	id := r.PathValue("database_id")
	if id == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: id")
		return
	}

	user := utils.GetUserFromContext(ctx)

	if err := shared.DeleteDatabaseSafe(ctx, h.Repo, h.Storage, repository.ULID(id)); err != nil {
		if errors.Is(err, customerrors.ErrNotFound) || strings.Contains(err.Error(), "not found") {
			utils.RespondWithError(w, http.StatusNotFound, "Database not found.")
		} else if strings.Contains(err.Error(), "invalid database name") {
			utils.RespondWithError(w, http.StatusBadRequest, "Invalid database name.")
		} else {
			h.Logger.Error("Failed to delete database.", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete database. Error: %v", err))
		}
		return
	}

	// Audit Log
	h.Auditor.Log(ctx, "database.delete", user.Username, id, nil)

	h.Logger.Info("Database deleted successfully.", "database_id", id)
	utils.RespondWithJSON(w, http.StatusOK, utils.MessageResponse{
		Message: "Database '" + id + "' and all its contents were successfully deleted.",
	})
}

// @Summary Trigger database housekeeping
// @Description Manually triggers the housekeeping maintenance task for a specific database.
// @Tags database
// @Produce json
// @Param    database_id path string true "Database ID"
// @Success 200 {object} HousekeepingResponse "Returns a report of actions taken."
// @Failure 400 {object} utils.ErrorResponse "Missing database_id path parameter"
// @Failure 401 {object} utils.ErrorResponse "Unauthorized"
// @Failure 403 {object} utils.ErrorResponse "Forbidden (Requires CanDelete role)"
// @Failure 404 {object} utils.ErrorResponse "Database not found"
// @Failure 409 {object} utils.ErrorResponse "Lock not acquired"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Security BasicAuth
// @Router /database/{database_id}/housekeeping [post]
func (h *DatabaseHandler) TriggerHousekeeping(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Extract and validate user
	user := utils.GetUserFromContext(ctx)

	// 2. Extract database ID from path
	id := r.PathValue("database_id")
	if id == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required parameter: id")
		return
	}

	// 3. Verify the database exists
	db, err := h.Repo.GetDatabase(ctx, repository.ULID(id))
	if err != nil {
		utils.RespondWithError(w, http.StatusNotFound, "Database not found")
		return
	}

	// 4. Execute Housekeeping Logic
	deletedCount, spaceFreed, err := h.HouseKeeper.RunDBHousekeeping(ctx, db)
	if errors.Is(err, customerrors.ErrLockNotAcquired) {
		h.Logger.Error("Skipping housekeeping", "error", err, "database_id", db.ID, "database_name", db.Name)
		utils.RespondWithError(w, http.StatusConflict, "Lock not acquired")
		return
	}
	if err != nil {
		h.Logger.Error("Manual housekeeping failed", "error", err, "database_id", db.ID, "database_name", db.Name)
		utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Housekeeping task failed. Error: %v", err))
		return
	}

	// 5. Audit Log the manual trigger
	h.Auditor.Log(ctx, "database.housekeeping", user.Username, id, map[string]any{
		"name":            db.Name,
		"entries_deleted": deletedCount,
		"space_freed":     spaceFreed,
	})

	// 6. Respond with the summary
	resp := HousekeepingResponse{
		DatabaseID:      id,
		DatabaseName:    db.Name,
		EntriesDeleted:  deletedCount,
		SpaceFreedBytes: spaceFreed,
		Message:         fmt.Sprintf("Housekeeping complete. %d entries deleted due to age or disk space limits.", deletedCount),
	}

	utils.RespondWithJSON(w, http.StatusOK, resp)
}
