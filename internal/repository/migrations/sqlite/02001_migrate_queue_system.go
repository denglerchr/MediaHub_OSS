// Migration: Migrate Queue System & Custom Fields Table
// Description: Upgrades the database schema from v2.0 to v2.1.
//
// Up changes:
//   - Adds the 'n_max_queued' integer column to the 'databases' table.
//   - Creates the 'database_custom_fields' dedicated metadata table.
//   - Extracts custom fields definitions from the old JSON string column in 'databases' and populates 'database_custom_fields'.
//   - Renames entry custom field columns in the dynamic 'entries_{db_id}' tables from 'cf_{name}' to 'cf_{field_id}'.
//   - Drops old dynamic entry indexes and recreates them using 'cf_{field_id}'.
//   - Drops the obsolete 'custom_fields' column from 'databases'.
//   - Updates SQLite check constraints for entry status fields.
//
// Down changes:
//   - Restores the obsolete 'custom_fields' JSON column in 'databases'.
//   - Re-populates the JSON metadata from the 'database_custom_fields' table.
//   - Renames dynamic entry columns back from 'cf_{field_id}' to 'cf_{name}'.
//   - Restores status field check constraints to exclude state 4.
//   - Drops the 'database_custom_fields' table and the 'n_max_queued' column.
package sqlitemigrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"mediahub_oss/internal/media"
	"mediahub_oss/internal/repository"
)

type mediaFieldDef struct {
	Name       string
	SQLiteType string
}

func up02001(ctx context.Context, tx *sql.Tx) error {
	// 1. Add n_max_queued column to databases table if not exists
	var hasNMaxQueued bool
	colRows, err := tx.QueryContext(ctx, `PRAGMA table_info("databases")`)
	if err == nil {
		for colRows.Next() {
			var cid int
			var name, dtype string
			var notnull, pk int
			var dfltVal any
			if err := colRows.Scan(&cid, &name, &dtype, &notnull, &dfltVal, &pk); err == nil {
				if name == "n_max_queued" {
					hasNMaxQueued = true
				}
			}
		}
		colRows.Close()
	}

	if !hasNMaxQueued {
		_, err := tx.ExecContext(ctx, "ALTER TABLE databases ADD COLUMN n_max_queued INTEGER NOT NULL DEFAULT 0;")
		if err != nil {
			return fmt.Errorf("failed to add n_max_queued column to databases: %w", err)
		}
	}

	// 2. Create database_custom_fields table
	createCFTableSQL := `
	CREATE TABLE IF NOT EXISTS database_custom_fields (
		database_id VARCHAR(26) NOT NULL,
		field_id INTEGER NOT NULL CHECK(field_id >= 0 AND field_id <= 254),
		name VARCHAR(64) NOT NULL,
		type TEXT NOT NULL CHECK(type IN ('TEXT', 'INTEGER', 'REAL', 'BOOLEAN')),
		is_indexed BOOLEAN NOT NULL DEFAULT 1,
		PRIMARY KEY (database_id, field_id),
		FOREIGN KEY (database_id) REFERENCES databases(id) ON DELETE CASCADE,
		UNIQUE (database_id, name)
	);`
	if _, err := tx.ExecContext(ctx, createCFTableSQL); err != nil {
		return fmt.Errorf("failed to create database_custom_fields table: %w", err)
	}

	// 3. Migrate custom fields definitions and rename entry table columns
	// Only migrate if custom_fields column exists in databases table
	var hasCustomFieldsCol bool
	colRows, err = tx.QueryContext(ctx, `PRAGMA table_info("databases")`)
	if err == nil {
		for colRows.Next() {
			var cid int
			var name, dtype string
			var notnull, pk int
			var dfltVal any
			if err := colRows.Scan(&cid, &name, &dtype, &notnull, &dfltVal, &pk); err == nil {
				if name == "custom_fields" {
					hasCustomFieldsCol = true
				}
			}
		}
		colRows.Close()
	}

	if hasCustomFieldsCol {
		rows, err := tx.QueryContext(ctx, "SELECT id, custom_fields FROM databases")
		if err != nil {
			return fmt.Errorf("failed to query databases: %w", err)
		}
		defer rows.Close()

		type dbFieldMigration struct {
			dbID         string
			customFields []repository.CustomFieldDef
		}
		var migrations []dbFieldMigration
		for rows.Next() {
			var dbID, cfJSON string
			if err := rows.Scan(&dbID, &cfJSON); err != nil {
				return err
			}
			var cfs []repository.CustomFieldDef
			if err := json.Unmarshal([]byte(cfJSON), &cfs); err != nil {
				return err
			}
			migrations = append(migrations, dbFieldMigration{dbID, cfs})
		}
		rows.Close()

		// 3.1. Drop ALL old custom field indexes first across all databases
		// This prevents ALTER TABLE RENAME COLUMN on any field from failing due to schema validation on OTHER fields.
		for _, m := range migrations {
			for _, cf := range m.customFields {
				_, _ = tx.ExecContext(ctx, fmt.Sprintf(`DROP INDEX IF EXISTS "idx_entries_%s_%s"`, m.dbID, cf.Name))
				_, _ = tx.ExecContext(ctx, fmt.Sprintf(`DROP INDEX IF EXISTS "idx_entries_%s_cf_%s"`, m.dbID, cf.Name))
			}
		}

		// 3.2. Migrate custom fields definitions and rename entry table columns
		for _, m := range migrations {
			for i, cf := range m.customFields {
				// Insert into new table
				insertCFSQL := `INSERT INTO database_custom_fields (database_id, field_id, name, type, is_indexed) VALUES (?, ?, ?, ?, 1)`
				if _, err := tx.ExecContext(ctx, insertCFSQL, m.dbID, i, cf.Name, cf.Type.String()); err != nil {
					return fmt.Errorf("failed to insert custom field: %w", err)
				}

				// Check if table and old column exist
				var hasTable bool
				err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, fmt.Sprintf("entries_%s", m.dbID)).Scan(&hasTable)
				if err != nil {
					return err
				}
				if hasTable {
					var hasCol bool
					colRows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("entries_%s")`, m.dbID))
					if err == nil {
						for colRows.Next() {
							var cid int
							var name, dtype string
							var notnull, pk int
							var dfltVal any
							if err := colRows.Scan(&cid, &name, &dtype, &notnull, &dfltVal, &pk); err == nil {
								if name == fmt.Sprintf("cf_%s", cf.Name) {
									hasCol = true
								}
							}
						}
						colRows.Close()
					}

					if hasCol {
						// Rename column from cf_{name} to cf_{id}
						renameSQL := fmt.Sprintf(`ALTER TABLE "entries_%s" RENAME COLUMN "cf_%s" TO "cf_%d"`, m.dbID, cf.Name, i)
						if _, err := tx.ExecContext(ctx, renameSQL); err != nil {
							return fmt.Errorf("failed to rename column: %w", err)
						}
					}

					// Create new index (since is_indexed defaults to true)
					createIdx := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_cf_%d" ON "entries_%s"("cf_%d")`, m.dbID, i, m.dbID, i)
					if _, err := tx.ExecContext(ctx, createIdx); err != nil {
						return fmt.Errorf("failed to create index: %w", err)
					}
				}
			}
		}

		// 4. Drop custom_fields column from databases table
		if _, err := tx.ExecContext(ctx, "ALTER TABLE databases DROP COLUMN custom_fields;"); err != nil {
			return fmt.Errorf("failed to drop custom_fields column: %w", err)
		}
	}

	// 5. Migrate dynamic entries tables check constraints to include status 4 (queued)
	return migrateCheckConstraints(ctx, tx, []repository.EntryStatus{0, 1, 2, 3, 4}, false)
}

func down02001(ctx context.Context, tx *sql.Tx) error {
	// 0. Drop any old custom field indexes first to prevent ALTER TABLE RENAME from failing due to index inconsistencies.
	var databaseCustomFieldsExists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='database_custom_fields')`).Scan(&databaseCustomFieldsExists)
	if err == nil && databaseCustomFieldsExists {
		rows, err := tx.QueryContext(ctx, "SELECT database_id, name FROM database_custom_fields")
		if err == nil {
			type cfRef struct {
				dbID string
				name string
			}
			var refs []cfRef
			for rows.Next() {
				var ref cfRef
				if err := rows.Scan(&ref.dbID, &ref.name); err == nil {
					refs = append(refs, ref)
				}
			}
			rows.Close()

			for _, ref := range refs {
				_, _ = tx.ExecContext(ctx, fmt.Sprintf(`DROP INDEX IF EXISTS "idx_entries_%s_%s"`, ref.dbID, ref.name))
				_, _ = tx.ExecContext(ctx, fmt.Sprintf(`DROP INDEX IF EXISTS "idx_entries_%s_cf_%s"`, ref.dbID, ref.name))
			}
		}
	}

	// 1. Revert dynamic entries tables check constraints back to status 0, 1, 2, 3
	if err := migrateCheckConstraints(ctx, tx, []repository.EntryStatus{0, 1, 2, 3}, true); err != nil {
		return err
	}

	// 2. Add custom_fields column back to databases table if not exists
	var hasCustomFieldsCol bool
	colRows, err := tx.QueryContext(ctx, `PRAGMA table_info("databases")`)
	if err == nil {
		for colRows.Next() {
			var cid int
			var name, dtype string
			var notnull, pk int
			var dfltVal any
			if err := colRows.Scan(&cid, &name, &dtype, &notnull, &dfltVal, &pk); err == nil {
				if name == "custom_fields" {
					hasCustomFieldsCol = true
				}
			}
		}
		colRows.Close()
	}

	if !hasCustomFieldsCol {
		_, err = tx.ExecContext(ctx, "ALTER TABLE databases ADD COLUMN custom_fields TEXT NOT NULL DEFAULT '[]';")
		if err != nil {
			return fmt.Errorf("failed to add custom_fields column back: %w", err)
		}
	}

	// 3. Re-populate custom_fields JSON and rename columns back
	if databaseCustomFieldsExists {
		rows, err := tx.QueryContext(ctx, "SELECT database_id, field_id, name, type, is_indexed FROM database_custom_fields ORDER BY database_id, field_id")
		if err == nil {
			type cfRecord struct {
				ID        int
				Name      string
				Type      string
				IsIndexed bool
			}
			dbFields := make(map[string][]cfRecord)
			for rows.Next() {
				var dbID string
				var rec cfRecord
				if err := rows.Scan(&dbID, &rec.ID, &rec.Name, &rec.Type, &rec.IsIndexed); err == nil {
					dbFields[dbID] = append(dbFields[dbID], rec)
				}
			}
			rows.Close()

			for dbID, recs := range dbFields {
				var oldCFs []repository.CustomFieldDef
				for _, rec := range recs {
					parsedType, _ := repository.ParseCustomFieldType(rec.Type)
					oldCFs = append(oldCFs, repository.CustomFieldDef{
						Name: rec.Name,
						Type: parsedType,
					})
				}
				js, err := json.Marshal(oldCFs)
				if err != nil {
					return err
				}

				// Update JSON column in databases
				_, err = tx.ExecContext(ctx, "UPDATE databases SET custom_fields = ? WHERE id = ?", string(js), dbID)
				if err != nil {
					return err
				}

				// Rename entry columns back from cf_{id} to cf_{name}
				for _, rec := range recs {
					var hasTable bool
					err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, fmt.Sprintf("entries_%s", dbID)).Scan(&hasTable)
					if err != nil {
						return err
					}
					if hasTable {
						var hasCol bool
						colRows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("entries_%s")`, dbID))
						if err == nil {
							for colRows.Next() {
								var cid int
								var name, dtype string
								var notnull, pk int
								var dfltVal any
								if err := colRows.Scan(&cid, &name, &dtype, &notnull, &dfltVal, &pk); err == nil {
									if name == fmt.Sprintf("cf_%d", rec.ID) {
										hasCol = true
									}
								}
							}
							colRows.Close()
						}

						if hasCol {
							renameSQL := fmt.Sprintf(`ALTER TABLE "entries_%s" RENAME COLUMN "cf_%d" TO "cf_%s"`, dbID, rec.ID, rec.Name)
							if _, err := tx.ExecContext(ctx, renameSQL); err != nil {
								return err
							}
						}

						// Drop new index
						_, _ = tx.ExecContext(ctx, fmt.Sprintf(`DROP INDEX IF EXISTS "idx_entries_%s_cf_%d"`, dbID, rec.ID))

						// Recreate old index (fixing the column name to cf_{name})
						createIdx := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_%s" ON "entries_%s"("cf_%s")`, dbID, rec.Name, dbID, rec.Name)
						_, _ = tx.ExecContext(ctx, createIdx)
					}
				}
			}
		}
	}

	// 4. Drop database_custom_fields table
	if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS database_custom_fields;"); err != nil {
		return fmt.Errorf("failed to drop database_custom_fields table: %w", err)
	}

	// 5. Drop n_max_queued column from databases table if exists
	var hasNMaxQueued bool
	colRows, err = tx.QueryContext(ctx, `PRAGMA table_info("databases")`)
	if err == nil {
		for colRows.Next() {
			var cid int
			var name, dtype string
			var notnull, pk int
			var dfltVal any
			if err := colRows.Scan(&cid, &name, &dtype, &notnull, &dfltVal, &pk); err == nil {
				if name == "n_max_queued" {
					hasNMaxQueued = true
				}
			}
		}
		colRows.Close()
	}
	if hasNMaxQueued {
		_, err = tx.ExecContext(ctx, "ALTER TABLE databases DROP COLUMN n_max_queued;")
		if err != nil {
			return fmt.Errorf("failed to drop n_max_queued column from databases: %w", err)
		}
	}

	return nil
}

func migrateCheckConstraints(ctx context.Context, tx *sql.Tx, allowedStatuses []repository.EntryStatus, isDowngrade bool) error {
	// 1. Query databases
	rows, err := tx.QueryContext(ctx, "SELECT id, content_type FROM databases")
	if err != nil {
		// If databases table doesn't exist yet, skip
		return nil
	}
	defer rows.Close()

	type dbInfo struct {
		ID           string
		ContentType  string
		CustomFields []repository.CustomFieldDef
	}
	var databases []dbInfo
	for rows.Next() {
		var id, contentType string
		if err := rows.Scan(&id, &contentType); err != nil {
			return err
		}
		databases = append(databases, dbInfo{ID: id, ContentType: contentType})
	}
	rows.Close()

	// Load custom fields from database_custom_fields table
	for idx, db := range databases {
		cfRows, err := tx.QueryContext(ctx, "SELECT field_id, name, type, is_indexed FROM database_custom_fields WHERE database_id = ? ORDER BY field_id", db.ID)
		if err != nil {
			continue
		}
		var customFields []repository.CustomFieldDef
		for cfRows.Next() {
			var cf repository.CustomFieldDef
			if err := cfRows.Scan(&cf.ID, &cf.Name, &cf.Type, &cf.IsIndexed); err != nil {
				cfRows.Close()
				return err
			}
			customFields = append(customFields, cf)
		}
		cfRows.Close()
		databases[idx].CustomFields = customFields
	}

	mediaFields := make(map[string][]mediaFieldDef)
	for _, contentType := range media.GetContentTypes() {
		fieldDefs, err := media.GetMetadataFields(contentType)
		if err != nil {
			return err
		}
		mediaFieldsOfContent := make([]mediaFieldDef, len(fieldDefs))
		for i, v := range fieldDefs {
			mediaFieldsOfContent[i] = mediaFieldDef{Name: v.Name, SQLiteType: v.Type}
		}
		mediaFields[contentType] = mediaFieldsOfContent
	}

	// 3. Migrate each entries table
	for _, db := range databases {
		tableName := fmt.Sprintf(`"entries_%s"`, db.ID)
		oldTableName := fmt.Sprintf(`"entries_%s_old"`, db.ID)

		// Check if table exists
		var sqlSchema string
		err = tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, fmt.Sprintf("entries_%s", db.ID)).Scan(&sqlSchema)
		if err != nil {
			continue // Skip if table doesn't exist
		}

		// Check if we need to migrate
		statusListStr := strings.Join(func() []string {
			var strs []string
			for _, s := range allowedStatuses {
				strs = append(strs, fmt.Sprintf("%d", s))
			}
			return strs
		}(), ", ")
		expectedPattern := fmt.Sprintf("status IN (%s)", statusListStr)

		if strings.Contains(sqlSchema, expectedPattern) {
			continue // Already has the expected constraint, skip
		}

		// If downgrading, update any status = 4 (queued) to status = 2 (error) first
		if isDowngrade {
			updateSQL := fmt.Sprintf(`UPDATE %s SET status = 2 WHERE status = 4`, tableName)
			_, _ = tx.ExecContext(ctx, updateSQL)
		}

		newSchemaSQL, err := buildDynamicTableSchema(db.ID, db.ContentType, db.CustomFields, allowedStatuses, mediaFields)
		if err != nil {
			return fmt.Errorf("failed to build dynamic schema SQL for db %s: %w", db.ID, err)
		}
		if err != nil {
			return fmt.Errorf("failed to build dynamic schema SQL for db %s: %w", db.ID, err)
		}

		// Rename old table
		renameSQL := fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, tableName, oldTableName)
		if _, err := tx.ExecContext(ctx, renameSQL); err != nil {
			return fmt.Errorf("failed to rename old table: %w", err)
		}

		// Create new table with updated constraints
		if _, err := tx.ExecContext(ctx, newSchemaSQL); err != nil {
			return fmt.Errorf("failed to create new table: %w", err)
		}

		// Copy data
		copySQL := fmt.Sprintf(`INSERT INTO %s SELECT * FROM %s`, tableName, oldTableName)
		if _, err := tx.ExecContext(ctx, copySQL); err != nil {
			return fmt.Errorf("failed to copy data: %w", err)
		}

		// Drop old table
		dropSQL := fmt.Sprintf(`DROP TABLE %s`, oldTableName)
		if _, err := tx.ExecContext(ctx, dropSQL); err != nil {
			return fmt.Errorf("failed to drop old table: %w", err)
		}

		// Recreate indexes
		indexesSQLs := buildIndexesSQL(db.ID, db.CustomFields)
		for _, indexSQL := range indexesSQLs {
			if _, err := tx.ExecContext(ctx, indexSQL); err != nil {
				return fmt.Errorf("failed to recreate index: %w", err)
			}
		}
	}

	return nil
}

func buildDynamicTableSchema(dbID, contentType string, customFields []repository.CustomFieldDef, allowedStatuses []repository.EntryStatus, mediaFields map[string][]mediaFieldDef) (string, error) {
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

	var statusStrs []string
	for _, s := range allowedStatuses {
		statusStrs = append(statusStrs, fmt.Sprintf("%d", s))
	}
	statusList := strings.Join(statusStrs, ", ")
	sb.WriteString(fmt.Sprintf("\n\tstatus INTEGER NOT NULL DEFAULT %d CHECK(status IN (%s))", repository.EntryStatusReady, statusList))

	fields, typeExists := mediaFields[contentType]
	if !typeExists {
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	for _, field := range fields {
		sb.WriteString(fmt.Sprintf(",\n\t%s %s NOT NULL", field.Name, field.SQLiteType))
	}

	sb.WriteString(",\n\tmime_type TEXT NOT NULL")

	for _, cf := range customFields {
		datatype := cf.Type.String()
		switch datatype {
		case "TEXT", "INTEGER", "REAL", "BOOLEAN":
			sb.WriteString(fmt.Sprintf(",\n\t\"cf_%d\" %s", cf.ID, datatype))
		default:
			return "", fmt.Errorf("unsupported custom field type: %s", cf.Type)
		}
	}

	sb.WriteString("\n);")
	return sb.String(), nil
}

func buildIndexesSQL(dbID string, customFields []repository.CustomFieldDef) []string {
	tableName := fmt.Sprintf(`"entries_%s"`, dbID)
	var sqls []string
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_time" ON %s(timestamp);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_status" ON %s(status);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_created" ON %s(created_at);`, dbID, tableName))
	sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_updated" ON %s(updated_at);`, dbID, tableName))
	for _, cf := range customFields {
		if cf.IsIndexed {
			sqls = append(sqls, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "idx_entries_%s_cf_%d" ON %s("cf_%d");`, dbID, cf.ID, tableName, cf.ID))
		}
	}
	return sqls
}
