package databasehandler

import (
	"fmt"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/ulid"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func parseFieldIDParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	fieldIDStr := r.PathValue("field_id")
	fieldID, err := strconv.Atoi(fieldIDStr)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid field_id path parameter")
		return 0, false
	}
	return fieldID, true
}

func mapCustomFieldToDTO(cf repository.CustomFieldDef) DatabaseCustomField {
	idVal := cf.ID
	isIndexedVal := cf.IsIndexed
	return DatabaseCustomField{
		ID:        &idVal,
		Name:      cf.Name,
		Type:      cf.Type.String(),
		IsIndexed: &isIndexedVal,
	}
}

func mapCustomFieldsToDTO(fields []repository.CustomFieldDef) []DatabaseCustomField {
	res := make([]DatabaseCustomField, len(fields))
	for i, cf := range fields {
		res[i] = mapCustomFieldToDTO(cf)
	}
	return res
}

func parseDatabaseIDParam(w http.ResponseWriter, r *http.Request) (repository.ULID, bool) {
	id := r.PathValue("database_id")
	if id == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Missing required path parameter: database_id")
		return repository.ULID{}, false
	}
	dbID, err := ulid.Parse(id)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid database_id path parameter")
		return repository.ULID{}, false
	}
	return dbID, true
}

// toModel parses the string-based API payload into the Repository model
func (dbc DatabaseCreatePayload) toModel() (repository.Database, error) {

	// convert from package internal model to repository model
	customFields := make([]repository.CustomFieldDef, len(dbc.CustomFields))
	for i, cf := range dbc.CustomFields {
		modelCF, err := cf.toModel()
		if err != nil {
			return repository.Database{}, err
		}
		customFields[i] = modelCF
	}

	hk, err := dbc.Housekeeping.toModel()
	if err != nil {
		return repository.Database{}, err
	}

	// create return object (ID will be generated automatically by the repository)
	return repository.Database{
		Name:        dbc.Name,
		ContentType: dbc.ContentType,
		NMaxQueued:  dbc.NMaxQueued,
		Config: repository.DatabaseConfig{
			CreatePreview:  dbc.Config.CreatePreview,
			AutoConversion: dbc.Config.AutoConversion,
		},
		Housekeeping: hk,
		CustomFields: customFields,
		Stats: repository.DatabaseStats{
			EntryCount:          0,
			TotalDiskSpaceBytes: 0,
		},
	}, nil
}

// IsReservedCustomFieldName reports whether the given custom-field name collides
// with a standard or media entry field. Matching is case-insensitive.
func IsReservedCustomFieldName(name string) bool {
	return repository.IsReservedCustomFieldName(name)
}

func (cf DatabaseCustomField) toModel() (repository.CustomFieldDef, error) {
	name := strings.TrimSpace(cf.Name)
	if name == "" {
		return repository.CustomFieldDef{}, fmt.Errorf("missing required field: name")
	}

	// FE-049: custom-field names colliding with the standard entry/media fields
	// break clients (duplicate keys in entry tables, wrong filter/search column)
	// and cannot be read back distinctly. Reserved set kept in sync with the
	// frontend (frontend/src/app/utils/validation.ts) and documented in
	// AI/Concept/03_01_Database_Dynamic.md. Enforced here so both
	// POST /api/database (inline custom_fields) and POST .../field (AddField)
	// reject such names.
	if IsReservedCustomFieldName(name) {
		return repository.CustomFieldDef{}, fmt.Errorf("custom field name '%s' is reserved (standard or media field)", name)
	}

	fieldType, err := repository.ParseCustomFieldType(cf.Type)
	if err != nil {
		return repository.CustomFieldDef{}, err
	}

	id := 0
	if cf.ID != nil {
		id = *cf.ID
	}
	isIndexed := true
	if cf.IsIndexed != nil {
		isIndexed = *cf.IsIndexed
	}
	return repository.CustomFieldDef{
		ID:        id,
		Name:      name,
		Type:      fieldType,
		IsIndexed: isIndexed,
	}, nil
}

// Extract the config part from the payload and return the repository type
func (upd DatabaseUpdatePayload) getConfig() repository.DatabaseConfig {
	return repository.DatabaseConfig{
		CreatePreview:  upd.Config.CreatePreview,
		AutoConversion: upd.Config.AutoConversion,
	}
}

// Extract the housekeeping part from the payload and return the repository type
func (upd DatabaseUpdatePayload) getHK(lastHKRun time.Time) (repository.DatabaseHK, error) {
	hk, err := upd.Housekeeping.toModel()
	if err != nil {
		return repository.DatabaseHK{}, err
	}
	hk.LastHkRun = lastHKRun
	return hk, nil
}

// toModel parses the string-based API payload into the uint64-based Repository model, applying defaults.
func (hk HousekeepingPayload) toModel() (repository.DatabaseHK, error) {
	var dbHk repository.DatabaseHK

	// Default: "1h"
	intervalStr := hk.Interval
	if intervalStr == "" {
		intervalStr = "1h"
	}
	dur, err := shared.ParseDuration(intervalStr)
	if err != nil {
		return repository.DatabaseHK{}, fmt.Errorf("invalid housekeeping.interval: %w", err)
	}
	dbHk.Interval = dur

	// Default: "100G"
	diskSpaceStr := hk.DiskSpace
	if diskSpaceStr == "" {
		diskSpaceStr = "100G"
	}
	size, err := shared.ParseSize(diskSpaceStr)
	if err != nil {
		return repository.DatabaseHK{}, fmt.Errorf("invalid housekeeping.disk_space: %w", err)
	}
	dbHk.DiskSpace = size

	// Default: "0"
	maxAgeStr := hk.MaxAge
	if maxAgeStr == "" {
		maxAgeStr = "0"
	}
	age, err := shared.ParseDuration(maxAgeStr)
	if err != nil {
		return repository.DatabaseHK{}, fmt.Errorf("invalid housekeeping.max_age: %w", err)
	}
	dbHk.MaxAge = age

	return dbHk, nil
}

func mapToDatabaseResponse(db repository.Database) DatabaseResponse {
	// create return object
	return DatabaseResponse{
		ID:          db.ID.String(),
		Name:        db.Name,
		ContentType: db.ContentType,
		NMaxQueued:  db.NMaxQueued,
		Config: ConfigPayload{
			CreatePreview:  db.Config.CreatePreview,
			AutoConversion: db.Config.AutoConversion,
		},
		Housekeeping: DatabaseResponseHK{
			Interval:  shared.DurationToString(db.Housekeeping.Interval),
			DiskSpace: shared.BytesToString(db.Housekeeping.DiskSpace),
			MaxAge:    shared.DurationToString(db.Housekeeping.MaxAge),
		},
		CustomFields: mapCustomFieldsToDTO(db.CustomFields),
		Stats: DatabaseResponseStats{
			EntryCount:          db.Stats.EntryCount,
			TotalDiskSpaceBytes: db.Stats.TotalDiskSpaceBytes,
			QueuedCount:         db.Stats.QueuedCount,
		},
	}
}
