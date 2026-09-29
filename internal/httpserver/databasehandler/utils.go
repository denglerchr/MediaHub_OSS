package databasehandler

import (
	"fmt"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"strings"
	"time"
)

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

	// create return object (ID will be generated automatically by the repository)
	return repository.Database{
		Name:        dbc.Name,
		ContentType: dbc.ContentType,
		NMaxQueued:  dbc.NMaxQueued,
		Config: repository.DatabaseConfig{
			CreatePreview:  dbc.Config.CreatePreview,
			AutoConversion: dbc.Config.AutoConversion,
		},
		Housekeeping: dbc.Housekeeping.toModel(),
		CustomFields: customFields,
		Stats: repository.DatabaseStats{
			EntryCount:          0,
			TotalDiskSpaceBytes: 0,
		},
	}, nil
}

func (cf DatabaseCustomField) toModel() (repository.CustomFieldDef, error) {
	name := strings.TrimSpace(cf.Name)
	if name == "" {
		return repository.CustomFieldDef{}, fmt.Errorf("missing required field: name")
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
func (upd DatabaseUpdatePayload) getHK(lastHKRun time.Time) repository.DatabaseHK {
	hk := upd.Housekeeping.toModel()
	hk.LastHkRun = lastHKRun
	return hk
}

// toModel parses the string-based API payload into the uint64-based Repository model, applying defaults.
func (hk HousekeepingPayload) toModel() repository.DatabaseHK {
	var dbHk repository.DatabaseHK

	// Default: "1h"
	intervalStr := hk.Interval
	if intervalStr == "" {
		intervalStr = "1h"
	}
	if dur, err := shared.ParseDuration(intervalStr); err == nil {
		dbHk.Interval = dur
	}

	// Default: "100G"
	diskSpaceStr := hk.DiskSpace
	if diskSpaceStr == "" {
		diskSpaceStr = "100G"
	}
	if size, err := shared.ParseSize(diskSpaceStr); err == nil {
		dbHk.DiskSpace = size
	}

	// Default: "0"
	maxAgeStr := hk.MaxAge
	if maxAgeStr == "" {
		maxAgeStr = "0"
	}
	if age, err := shared.ParseDuration(maxAgeStr); err == nil {
		dbHk.MaxAge = age
	}

	return dbHk
}

func mapToDatabaseResponse(db repository.Database) DatabaseResponse {

	// convert from repository model to package internal model
	customFields := make([]DatabaseCustomField, len(db.CustomFields))
	for i, cf := range db.CustomFields {
		idVal := cf.ID
		isIndexedVal := cf.IsIndexed
		customFields[i] = DatabaseCustomField{
			ID:        &idVal,
			Name:      cf.Name,
			Type:      cf.Type.String(),
			IsIndexed: &isIndexedVal,
		}
	}

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
		CustomFields: customFields,
		Stats: DatabaseResponseStats{
			EntryCount:          db.Stats.EntryCount,
			TotalDiskSpaceBytes: db.Stats.TotalDiskSpaceBytes,
		},
	}
}
