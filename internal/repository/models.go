package repository

import (
	"time"
)

type ULID string

func (u ULID) String() string {
	return string(u)
}

type Database struct {
	ID           ULID
	Name         string
	ContentType  string
	NMaxQueued   int
	Config       DatabaseConfig
	Housekeeping DatabaseHK
	CustomFields []CustomFieldDef
	Stats        DatabaseStats
}

type DatabaseConfig struct {
	CreatePreview  bool
	AutoConversion string
}

// Struct for housekeeping settings
type DatabaseHK struct {
	Interval  time.Duration
	DiskSpace uint64
	MaxAge    time.Duration
	LastHkRun time.Time // timestamp of the last housekeeping run, used to determine when the next run should occur
}

type DatabaseStats struct {
	EntryCount          uint64
	TotalDiskSpaceBytes uint64
}

// CustomFieldDef defines a custom metadata field for a database.
type CustomFieldDef struct {
	ID        int
	Name      string
	Type      string
	IsIndexed bool
}

type Entry struct {
	ID           int64
	FileName     string
	Size         uint64
	PreviewSize  uint64
	Timestamp    time.Time // The zero value (time.Time{}) indicates a missing timestamp
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MimeType     string
	Status       EntryStatus    // "processing" 0x01 or "ready" 0x00 for now
	MediaFields  map[string]any // contains fields that are related to the filetype, e.g., image size
	CustomFields map[string]any
}

type User struct {
	ID           ULID
	Username     string
	IsAdmin      bool
	PasswordHash string
	AccountType  AccountType
}

type APIKey struct {
	ID         ULID
	UserID     ULID
	Name       string
	KeyHash    string
	KeyHint    string
	Scope      AccessGrant
	CreatedAt  time.Time
	ExpiresAt  time.Time // Uses time.Time{} for infinity / no expiry
	LastUsedAt time.Time // Uses time.Time{} for never used
}

// defines a role that a user has in a specific database (CanView, CanCreate, CanEdit, CanDelete)
type UserPermissions struct {
	UserID     ULID
	DatabaseID ULID
	Roles      AccessGrant
}

// Pagination controls the subset of results returned.
type Pagination struct {
	Offset int
	Limit  int
}

// SearchRequest defines the complex, nested filter criteria for database queries.
type SearchRequest struct {
	Filter     *FilterGroup
	Sort       *SortCriteria
	Pagination Pagination
}

// FilterGroup allows chaining multiple conditions together.
type FilterGroup struct {
	Operator   string      // e.g., "and", "or"
	Conditions []Condition // The individual rules
}

// Condition represents a single query filter.
type Condition struct {
	Field    string
	Operator string // e.g., "=", ">", "<", "LIKE"
	Value    any    // 'any' allows for strings, numbers, or booleans
}

// SortCriteria defines how the results should be ordered.
type SortCriteria struct {
	Field     string
	Direction string // "asc" or "desc"
}

// returned upon deleting an entry from the database
type DeletedEntryMeta struct {
	ID          int64
	Filesize    uint64
	PreviewSize uint64
}

type AuditLog struct {
	ID        int64     // created by the database upon writing
	Timestamp time.Time // timestamp created by the database upon writing
	Action    string
	Actor     string
	Resource  string
	Details   map[string]any
}
