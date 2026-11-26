package entryhandler

import (
	"log/slog"
	"mediahub_oss/internal/logging/audit"
	"mediahub_oss/internal/media"
	"mediahub_oss/internal/processing"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/storage"
)

type EntryHandler struct {
	Logger                 *slog.Logger
	Auditor                audit.AuditLogger
	Repo                   repository.Repository
	Storage                storage.StorageProvider
	MaxSyncUploadSizeBytes int64
	MediaConverter         media.MediaConverter
	Processor              *processing.Processor
	Imports                *ImportManager

	maxImportUploadBytes int64
}

// metadata that can be added when sending a new entry
type PostPatchEntryRequest struct {
	Timestamp    int64          `json:"timestamp"`
	FileName     string         `json:"filename"`
	CustomFields map[string]any `json:"custom_fields"`
}

type BulkDeleteRequest struct {
	IDs []int64 `json:"ids"`
}

// ExportRequest defines the payload for the export endpoint.
type ExportRequest struct {
	IDs []int64 `json:"ids"`
}

// SearchRequestPayload defines the JSON structure for the complex search endpoint.
type SearchRequestPayload struct {
	Filter     *FilterGroupPayload  `json:"filter,omitempty"`
	Sort       *SortCriteriaPayload `json:"sort,omitempty"`
	Pagination PaginationPayload    `json:"pagination"`
}

// FilterGroupPayload allows chaining multiple conditions together.
type FilterGroupPayload struct {
	Operator   string             `json:"operator"`   // e.g., "and", "or"
	Conditions []ConditionPayload `json:"conditions"` // The individual rules
}

// ConditionPayload represents a single query filter.
type ConditionPayload struct {
	Field    string `json:"field"`
	Operator string `json:"operator"` // e.g., "=", ">", "<", "LIKE"
	Value    any    `json:"value"`    // 'any' allows for strings, numbers, or booleans
}

// SortCriteriaPayload defines how the results should be ordered.
type SortCriteriaPayload struct {
	Field     string `json:"field"`
	Direction string `json:"direction"` // "asc" or "desc"
}

// PaginationPayload controls the subset of results returned.
type PaginationPayload struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

// Returned in case of sync file handling or entry requests
type EntryResponse struct {
	DatabaseID   string         `json:"database_id"`
	EntryID      int64          `json:"id"`
	FileName     string         `json:"filename"`
	Size         uint64         `json:"filesize"`
	PreviewSize  uint64         `json:"preview_filesize"`
	Status       string         `json:"status"`
	Timestamp    int64          `json:"timestamp"`
	CreatedAt    int64          `json:"created_at"`
	UpdatedAt    int64          `json:"updated_at"`
	MimeType     string         `json:"mime_type"`
	MediaFields  map[string]any `json:"media_fields"`
	CustomFields map[string]any `json:"custom_fields"`
}

// Returned in case of async file handling
type PartialEntryResponse struct {
	DatabaseID   string         `json:"database_id"`
	EntryID      int64          `json:"id"`
	Status       string         `json:"status"`
	Timestamp    int64          `json:"timestamp"`
	CreatedAt    int64          `json:"created_at"`
	UpdatedAt    int64          `json:"updated_at"`
	MimeType     string         `json:"mime_type"`
	CustomFields map[string]any `json:"custom_fields"`
}

// FileJSONResponse is used when clients request a file via Accept: application/json.
// This is used for both /entry/file and /entry/preview endpoints.
type FileJSONResponse struct {
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     uint64 `json:"size,omitempty"`
	Data     string `json:"data"` // Base64 encoded string with data URI prefix
}

// BulkDeleteResponse defines the success payload for a bulk delete operation.
type BulkDeleteResponse struct {
	DatabaseID      string `json:"database_id"`
	DeletedCount    int    `json:"deleted_count"`
	SpaceFreedBytes uint64 `json:"space_freed_bytes"`
	Message         string `json:"message"`
	Errors          string `json:"errors"`
}

// Helper for range parsing
type byteRange struct {
	start  int64
	length int64
}

// ImportConfigPayload defines the JSON configuration for the bulk import process.
type ImportConfigPayload struct {
	Mode               string            `json:"mode"`                 // "generate_new", "skip", or "overwrite"
	CustomFieldMapping map[string]string `json:"custom_field_mapping"` // Maps CSV column headers to DB custom fields
	UnmappedFields     string            `json:"unmapped_fields"`      // "ignore" or "fail"
}

// ImportResponse defines the JSON payload returned upon successfully accepting an import job.
type ImportResponse struct {
	DatabaseID string `json:"database_id"`
	Message    string `json:"message"`
}

// Interfaces

// Define an interface that guarantees a GetID method
type EntryWithID interface {
	GetID() int64
}

// Add the method to both structs so they satisfy the interface
func (e EntryResponse) GetID() int64        { return e.EntryID }
func (p PartialEntryResponse) GetID() int64 { return p.EntryID }
