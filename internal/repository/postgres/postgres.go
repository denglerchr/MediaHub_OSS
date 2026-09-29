package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"mediahub_oss/internal/media"
	"mediahub_oss/internal/repository"

	"github.com/Masterminds/squirrel"
	_ "github.com/lib/pq" // PostgreSQL driver
)

const customFieldsPrefix = "cf_"

type PostgresRepository struct {
	DB              *sql.DB
	Builder         squirrel.StatementBuilderType
	AllowedStatuses []repository.EntryStatus
	MediaFields     map[string][]MediaField
}

type MediaField struct {
	Name         string
	PostgresType string // "INTEGER", "BIGINT", "TEXT", "DOUBLE PRECISION" or similar
}

// NewRepository initializes and returns a pointer to a new PostgresRepository.
func NewRepository(source string, maxOpenConns, maxIdleConns int) (*PostgresRepository, error) {
	db, err := sql.Open("postgres", source)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres database pool: %w", err)
	}

	if maxOpenConns > 0 {
		db.SetMaxOpenConns(maxOpenConns)
	}
	if maxIdleConns > 0 {
		db.SetMaxIdleConns(maxIdleConns)
	}

	// Verify the connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping postgres database: %w", err)
	}

	// PostgreSQL uses $1, $2 for prepared statement placeholders
	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

	// Extract media metadata fields for each content type
	mediaFields := make(map[string][]MediaField)
	for _, contentType := range media.GetContentTypes() {
		fieldDefs, err := media.GetMetadataFields(contentType)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("could not find media type %v: %w", contentType, err)
		}
		mediaFieldsOfContent := make([]MediaField, len(fieldDefs))
		for i, v := range fieldDefs {
			pgType := mapToPostgresType(v.Type)
			mediaFieldsOfContent[i] = MediaField{v.Name, pgType}
		}
		mediaFields[contentType] = mediaFieldsOfContent
	}

	return &PostgresRepository{
		DB:              db,
		Builder:         builder,
		AllowedStatuses: repository.GetAllEntryStatuses(),
		MediaFields:     mediaFields,
	}, nil
}

func mapToPostgresType(t string) string {
	switch strings.ToUpper(t) {
	case "INTEGER", "INT", "INT32", "UINT32":
		return "INTEGER"
	case "INT64", "UINT64", "BIGINT":
		return "BIGINT"
	case "INT16", "UINT16", "INT8", "UINT8", "SMALLINT":
		return "SMALLINT"
	case "REAL", "FLOAT64", "FLOAT32", "DOUBLE", "DOUBLE PRECISION":
		return "DOUBLE PRECISION"
	case "TEXT", "STRING":
		return "TEXT"
	case "BOOLEAN", "BOOL":
		return "BOOLEAN"
	case "COORDINATE", "POINT":
		return "POINT"
	default:
		return t
	}
}

func (r *PostgresRepository) Close() error {
	if r.DB != nil {
		return r.DB.Close()
	}
	return nil
}

// GetDBTime returns the current database server timestamp in UNIX milliseconds.
func (r *PostgresRepository) GetDBTime(ctx context.Context) (time.Time, error) {
	var millis int64
	err := r.DB.QueryRowContext(ctx, "SELECT (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT").Scan(&millis)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get postgres server time: %w", err)
	}
	return time.UnixMilli(millis), nil
}
