package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"regexp"
	"strings"
	"time"
)

var safeNameRegex = regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_]*$")

type scanner interface {
	Scan(dest ...any) error
}

// scanDatabaseRow maps an SQL row from the databases table into repo.Database.
func scanDatabaseRow(s scanner) (repo.Database, error) {
	var db repo.Database
	var intervalMs, maxAgeMs, HKLastRun int64

	err := s.Scan(
		&db.ID,
		&db.Name,
		&db.ContentType,
		&intervalMs,
		&db.Housekeeping.DiskSpace,
		&maxAgeMs,
		&db.Config.CreatePreview,
		&db.Config.AutoConversion,
		&db.NMaxQueued,
		&HKLastRun,
		&db.Stats.EntryCount,
		&db.Stats.TotalDiskSpaceBytes,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.Database{}, customerrors.ErrNotFound
		}
		return repo.Database{}, fmt.Errorf("failed to scan row: %w", err)
	}

	db.Housekeeping.Interval = time.Duration(intervalMs) * time.Millisecond
	db.Housekeeping.MaxAge = time.Duration(maxAgeMs) * time.Millisecond
	if HKLastRun > 0 {
		db.Housekeeping.LastHkRun = time.UnixMilli(HKLastRun)
	}

	return db, nil
}

// BuildDynamicTableSchema generates PostgreSQL DDL for a dedicated media database entries table.
// Optimization: Uses SMALLINT (2 bytes) for entry status instead of 4-byte INTEGER to optimize storage on millions of rows.
// CHECK constraints are preferred over native PG ENUMs for transactional migration safety and driver compatibility.
func (r *PostgresRepository) BuildDynamicTableSchema(dbID, contentType string, customFields []repo.CustomFieldDef) (string, error) {
	if !shared.IsValidULID(dbID) {
		return "", fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n", tableName))
	sb.WriteString("\tid SERIAL PRIMARY KEY,\n")
	sb.WriteString("\ttimestamp BIGINT NOT NULL,\n")
	sb.WriteString("\tcreated_at BIGINT NOT NULL,\n")
	sb.WriteString("\tupdated_at BIGINT NOT NULL,\n")
	sb.WriteString("\tfilesize BIGINT NOT NULL,\n")
	sb.WriteString("\tpreview_filesize BIGINT NOT NULL,\n")
	sb.WriteString("\tfilename TEXT NOT NULL DEFAULT '',\n")

	var statusStrs []string
	for _, s := range r.AllowedStatuses {
		statusStrs = append(statusStrs, fmt.Sprintf("%d", s))
	}
	statusList := strings.Join(statusStrs, ", ")
	// SMALLINT constraint: 2 bytes per row for status tracking (0: processing, 1: ready, 2: error, 3: deleting, 4: queued)
	sb.WriteString(fmt.Sprintf("\n\tstatus SMALLINT NOT NULL DEFAULT %d CHECK(status IN (%s))", repo.EntryStatusReady, statusList))

	fields, typeExists := r.MediaFields[contentType]
	if !typeExists {
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	for _, field := range fields {
		sb.WriteString(fmt.Sprintf(",\n\t%s %s NOT NULL", field.Name, field.PostgresType))
	}

	sb.WriteString(",\n\tmime_type TEXT NOT NULL")

	for _, cf := range customFields {
		datatype := mapToPostgresType(cf.Type.String())
		switch datatype {
		case "TEXT", "INTEGER", "DOUBLE PRECISION", "BOOLEAN", "POINT":
			sb.WriteString(fmt.Sprintf(",\n\t\"%s%d\" %s", customFieldsPrefix, cf.ID, datatype))
		default:
			return "", fmt.Errorf("unsupported custom field type: %s", cf.Type)
		}
	}

	sb.WriteString("\n);")
	return sb.String(), nil
}

// BuildCustomFieldIndexSQL generates the CREATE INDEX statement for a custom field in PostgreSQL.
func BuildCustomFieldIndexSQL(dbID string, cf repo.CustomFieldDef) string {
	tableName := fmt.Sprintf(`"entries_%s"`, dbID)
	if cf.Type.IsCoordinate() {
		return fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_%s%d" ON %s USING gist("%s%d");`, dbID, customFieldsPrefix, cf.ID, tableName, customFieldsPrefix, cf.ID)
	}
	return fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_%s%d" ON %s("%s%d");`, dbID, customFieldsPrefix, cf.ID, tableName, customFieldsPrefix, cf.ID)
}

// BuildCustomFieldDropIndexSQL generates the DROP INDEX statement for a custom field in PostgreSQL.
func BuildCustomFieldDropIndexSQL(dbID string, fieldID int) string {
	return fmt.Sprintf(`DROP INDEX IF EXISTS "idx_entries_%s_%s%d"`, dbID, customFieldsPrefix, fieldID)
}

// BuildIndexesSQL returns standard and partial PostgreSQL indexes for dynamic entry tables.
// Optimization: Includes a partial index on status = 4 (Queued) to make background worker queue polling instant,
// regardless of how many millions of ready entries exist in the table.
func BuildIndexesSQL(dbID string, customFields []repo.CustomFieldDef) []string {
	tableName := fmt.Sprintf(`"entries_%s"`, dbID)
	var sqls []string

	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_time" ON %s(timestamp);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_status" ON %s(status);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_created" ON %s(created_at);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_updated" ON %s(updated_at);`, dbID, tableName))

	// Partial index for fast background queue scanning: filters specifically for queued status (4)
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_queued" ON %s(id) WHERE status = 4;`, dbID, tableName))

	for _, cf := range customFields {
		if cf.IsIndexed {
			sqls = append(sqls, BuildCustomFieldIndexSQL(dbID, cf))
		}
	}

	return sqls
}
