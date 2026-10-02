package databasehandler

import (
	"log/slog"
	"mediahub_oss/internal/housekeeping"
	"mediahub_oss/internal/logging/audit"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/storage"
)

type DatabaseHandler struct {
	Logger      *slog.Logger
	Auditor     audit.AuditLogger
	Repo        repository.Repository
	Storage     storage.StorageProvider
	HouseKeeper housekeeping.HouseKeeper
}

// DatabaseCreatePayload defines the required JSON payload for POST /api/database.
type DatabaseCreatePayload struct {
	Name         string                `json:"name"`
	ContentType  string                `json:"content_type"`
	NMaxQueued   int                   `json:"n_max_queued"`
	Config       ConfigPayload         `json:"config"`
	Housekeeping HousekeepingPayload   `json:"housekeeping"`
	CustomFields []DatabaseCustomField `json:"custom_fields"`
}

type DatabaseCustomField struct {
	ID        *int   `json:"id,omitempty"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsIndexed *bool  `json:"is_indexed,omitempty"`
}

// DatabaseUpdatePayload defines the required JSON payload for PUT /api/database.
type DatabaseUpdatePayload struct {
	Name         string              `json:"name"`
	NMaxQueued   int                 `json:"n_max_queued"`
	Config       ConfigPayload       `json:"config"`
	Housekeeping HousekeepingPayload `json:"housekeeping"`
}

// ConfigPayload defines the JSON structure for type-specific settings.
type ConfigPayload struct {
	CreatePreview  bool   `json:"create_preview"`
	AutoConversion string `json:"auto_conversion"`
}

// HousekeepingPayload defines the JSON structure for housekeeping rules.
// These are strings in the API but converted to uint64 for the DB.
type HousekeepingPayload struct {
	Interval  string `json:"interval"`
	DiskSpace string `json:"disk_space"`
	MaxAge    string `json:"max_age"`
}

// HousekeepingResponse defines the JSON payload returned after triggering housekeeping.
type HousekeepingResponse struct {
	DatabaseID      string `json:"database_id"`
	DatabaseName    string `json:"database_name"`
	EntriesDeleted  int    `json:"entries_deleted"`
	SpaceFreedBytes uint64 `json:"space_freed_bytes"`
	Message         string `json:"message"`
}

// DatabaseResponse defines the JSON structure for outbound database data.
type DatabaseResponse struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	ContentType  string                `json:"content_type"`
	NMaxQueued   int                   `json:"n_max_queued"`
	Config       ConfigPayload         `json:"config"`
	Housekeeping DatabaseResponseHK    `json:"housekeeping"`
	CustomFields []DatabaseCustomField `json:"custom_fields"`
	Stats        DatabaseResponseStats `json:"stats,omitempty"`
}

// Using explicit types to send to the frontend
type DatabaseResponseHK struct {
	Interval  string `json:"interval"`   // e.g."10min"
	DiskSpace string `json:"disk_space"` // e.g. "10G"
	MaxAge    string `json:"max_age"`    // e.g. "365d"
}

type DatabaseResponseStats struct {
	EntryCount          uint64 `json:"entry_count"`
	TotalDiskSpaceBytes uint64 `json:"total_disk_space_bytes"`
	QueuedCount         uint64 `json:"queued_count"`
}
