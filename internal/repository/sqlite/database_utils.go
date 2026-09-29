package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"strings"
	"time"
)

// scanner defines an interface satisfied by both *sql.Row and *sql.Rows
type scanner interface {
	Scan(dest ...any) error
}

// scanDatabaseRow maps an SQL row from the databases table into the repository.Database struct.
func scanDatabaseRow(s scanner) (repo.Database, error) {
	var db repo.Database
	var intervalMs, maxAgeMs, HKLastRun int64 // Intermediate variables for millisecond values

	// Make sure ID is the first scanned column matching the modified Select queries
	err := s.Scan(
		&db.ID,
		&db.Name,
		&db.ContentType,
		&intervalMs, // Scan into intermediate variable
		&db.Housekeeping.DiskSpace,
		&maxAgeMs, // Scan into intermediate variable
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

	// Convert the scanned milliseconds back to Go's time.Duration (nanoseconds)
	db.Housekeeping.Interval = time.Duration(intervalMs) * time.Millisecond
	db.Housekeeping.MaxAge = time.Duration(maxAgeMs) * time.Millisecond
	if HKLastRun > 0 {
		db.Housekeeping.LastHkRun = time.UnixMilli(HKLastRun)
	}

	return db, nil
}

// BuildDynamicTableSchema generates the CREATE TABLE statement using the database ID.
func (r *SQLiteRepository) BuildDynamicTableSchema(dbID, contentType string, customFields []repo.CustomFieldDef) (string, error) {
	if !shared.IsValidULID(dbID) {
		return "", fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n", tableName))
	sb.WriteString("\tid INTEGER PRIMARY KEY AUTOINCREMENT,\n")
	sb.WriteString("\ttimestamp BIGINT NOT NULL,\n")
	sb.WriteString("\tcreated_at BIGINT NOT NULL,\n")
	sb.WriteString("\tupdated_at BIGINT NOT NULL,\n")
	sb.WriteString("\tfilesize INTEGER NOT NULL,\n")
	sb.WriteString("\tpreview_filesize INTEGER NOT NULL,\n")
	sb.WriteString("\tfilename TEXT NOT NULL DEFAULT '',\n")

	// 1. Add Status constraint
	var statusStrs []string
	for _, s := range r.AllowedStatuses {
		statusStrs = append(statusStrs, fmt.Sprintf("%d", s))
	}

	// Join them into a comma-separated list: "0, 1, 2, 3"
	statusList := strings.Join(statusStrs, ", ")
	sb.WriteString(fmt.Sprintf("\n\tstatus INTEGER NOT NULL DEFAULT %d CHECK(status IN (%s))", repo.EntryStatusReady, statusList))

	// 2. Add Media-specific fields dynamically!
	fields, typeExists := r.MediaFields[contentType]
	if !typeExists {
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	for _, field := range fields {
		sb.WriteString(fmt.Sprintf(",\n\t%s %s NOT NULL", field.Name, field.SQLiteType))
	}

	// 3. Add Mime Type constraint
	sb.WriteString(",\n\tmime_type TEXT NOT NULL")

	// 4. Add User Custom Fields
	for _, cf := range customFields {
		switch cf.Type {
		case repo.CustomFieldTypeText, repo.CustomFieldTypeInteger, repo.CustomFieldTypeReal, repo.CustomFieldTypeBoolean:
			sb.WriteString(fmt.Sprintf(",\n\t\"%s%d\" %s", customFieldsPrefix, cf.ID, cf.Type.String()))
		case repo.CustomFieldTypeCoordinate:
			sb.WriteString(fmt.Sprintf(",\n\t\"%s%d_lat\" REAL,\n\t\"%s%d_lng\" REAL", customFieldsPrefix, cf.ID, customFieldsPrefix, cf.ID))
		default:
			return "", fmt.Errorf("unsupported custom field type: %s", cf.Type)
		}
	}

	sb.WriteString("\n);")
	return sb.String(), nil
}

// BuildCustomFieldIndexSQL generates the CREATE INDEX statement for a custom field.
func BuildCustomFieldIndexSQL(dbID string, cf repo.CustomFieldDef) string {
	tableName := fmt.Sprintf(`"entries_%s"`, dbID)
	if cf.Type.IsCoordinate() {
		return fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_%s%d" ON %s("%s%d_lat", "%s%d_lng")`, dbID, customFieldsPrefix, cf.ID, tableName, customFieldsPrefix, cf.ID, customFieldsPrefix, cf.ID)
	}
	return fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_%s%d" ON %s("%s%d")`, dbID, customFieldsPrefix, cf.ID, tableName, customFieldsPrefix, cf.ID)
}

// BuildCustomFieldDropIndexSQL generates the DROP INDEX statement for a custom field.
func BuildCustomFieldDropIndexSQL(dbID string, fieldID int) string {
	return fmt.Sprintf(`DROP INDEX IF EXISTS "idx_entries_%s_%s%d"`, dbID, customFieldsPrefix, fieldID)
}

// BuildIndexesSQL creates the indexing statements using the database ID.
func BuildIndexesSQL(dbID string, customFields []repo.CustomFieldDef) []string {
	tableName := fmt.Sprintf(`"entries_%s"`, dbID)
	var sqls []string

	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_time" ON %s(timestamp);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_status" ON %s(status);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_created" ON %s(created_at);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_updated" ON %s(updated_at);`, dbID, tableName))

	for _, cf := range customFields {
		if cf.IsIndexed {
			sqls = append(sqls, BuildCustomFieldIndexSQL(dbID, cf))
		}
	}

	return sqls
}
