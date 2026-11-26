package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"mediahub_oss/internal/media"
	"mediahub_oss/internal/repository"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/patrickmn/go-cache"
	_ "modernc.org/sqlite" // SQLite driver
)

// to avoid name collisions of custom user fields
const customFieldsPrefix = "cf_"

// to validate the name of provided databases
var safeNameRegex = regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_]*$")

type lockEntry struct {
	ownerID   string
	expiresAt time.Time
}

type SQLiteRepository struct {
	DB      *sql.DB
	Cache   *cache.Cache
	Builder squirrel.StatementBuilderType // SQL Query Builder

	AllowedStatuses []repository.EntryStatus
	MediaFields     map[string][]MediaField // Added MediaFields

	cfMu    sync.RWMutex
	locksMu sync.Mutex
	locks   map[string]lockEntry
}

type MediaField struct {
	Name       string
	SQLiteType string // "INTEGER", "TEXT" or similar
}

// NewRepository initializes and returns a pointer to a new SQLiteRepository.
func NewRepository(path string) (*SQLiteRepository, error) {
	// 1. Configure the Connection String (DSN) with essential Pragmas
	dsn := path

	// We'll build a list of required pragmas for a robust concurrent SQLite setup
	pragmas := map[string]string{
		"foreign_keys": "1",
		"journal_mode": "WAL",    // Enables Write-Ahead Logging (concurrent reads/writes)
		"synchronous":  "NORMAL", // Safe for WAL mode, improves write performance
		"busy_timeout": "5000",   // Wait up to 5 seconds for a lock instead of failing instantly
	}

	// Append missing pragmas to the DSN
	for key, val := range pragmas {
		pragmaStr := fmt.Sprintf("_pragma=%s(%s)", key, val)
		if !strings.Contains(dsn, pragmaStr) {
			if strings.Contains(dsn, "?") {
				dsn += "&" + pragmaStr
			} else {
				dsn += "?" + pragmaStr
			}
		}
	}

	// 2. Open the Database Connection
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database pool: %w", err)
	}

	// 3. Configure the Connection Pool (Crucial for SQLite)
	// This serializes access at the Go level, preventing the SQLite engine from ever
	// encountering a scenario where two Go connections fight for a write lock.
	db.SetMaxOpenConns(1)

	// Verify the Connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	// Initialize the Query Builder
	// SQLite uses `?` for prepared statement placeholders (unlike Postgres which uses `$1, $2`).
	// We configure Squirrel to use the Question format globally for this builder.
	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Question)

	// Initialize the Cache
	c := cache.New(5*time.Minute, 10*time.Minute)

	// extract media fields as map[string][]MediaField
	mediaFields := make(map[string][]MediaField)
	for _, contentType := range media.GetContentTypes() {
		fieldDefs, err := media.GetMetadataFields(contentType)
		if err != nil {
			return &SQLiteRepository{}, fmt.Errorf("Could not find media type %v", contentType)
		}
		mediaFieldsOfContent := make([]MediaField, len(fieldDefs))
		for i, v := range fieldDefs {
			mediaFieldsOfContent[i] = MediaField{v.Name, mapToSQLiteType(v.Type)}
		}
		mediaFields[contentType] = mediaFieldsOfContent
	}

	return &SQLiteRepository{
		DB:              db,
		Cache:           c,
		Builder:         builder,
		AllowedStatuses: repository.GetAllEntryStatuses(),
		MediaFields:     mediaFields, // TODO create map from media interface methods
		locks:           make(map[string]lockEntry),
	}, nil
}

func mapToSQLiteType(t string) string {
	switch strings.ToUpper(t) {
	case "INTEGER", "INT", "INT64", "UINT64", "INT32", "UINT32", "INT16", "UINT16", "INT8", "UINT8", "SMALLINT", "BIGINT":
		return "INTEGER"
	case "REAL", "FLOAT64", "FLOAT32", "DOUBLE", "DOUBLE PRECISION":
		return "REAL"
	case "TEXT", "STRING":
		return "TEXT"
	case "BOOLEAN", "BOOL":
		return "INTEGER"
	default:
		return t
	}
}

func (s *SQLiteRepository) Close() error {
	if s.DB != nil {
		if err := s.DB.Close(); err != nil {
			return err
		}
	}
	return nil
}

// GetServerTime returns the current database timestamp in UNIX milliseconds.
func (r *SQLiteRepository) GetDBTime(ctx context.Context) (time.Time, error) {

	// for SQLite, we can just return the client time since SQLite doesn't have a separate server time
	//  and relies on the client's system time.
	return time.Now(), nil
}
