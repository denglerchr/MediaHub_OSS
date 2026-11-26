package sqlitemigrations

import (
	"context"
	"database/sql"
	"fmt"
)

func up03006(ctx context.Context, tx *sql.Tx) error {
	if err := rebuildDatabasesTable03006(ctx, tx); err != nil {
		return err
	}
	if err := backfillQueuedCounts03006(ctx, tx); err != nil {
		return err
	}
	return verifyForeignKeys03006(ctx, tx)
}

func down03006(_ context.Context, _ *sql.Tx) error {
	return nil
}

func rebuildDatabasesTable03006(ctx context.Context, tx *sql.Tx) error {
	const dbCols = `id, name, content_type, hk_interval, hk_disk_space, hk_max_age, create_preview, auto_conversion, n_max_queued, hk_last_run, entry_count, total_disk_space_bytes, queued_count`
	const cfCols = `database_id, field_id, name, type, is_indexed`
	const permCols = `user_id, database_id, can_view, can_create, can_edit, can_delete, can_admin`

	const createDatabasesSQL = `CREATE TABLE databases (
    id TEXT(26) PRIMARY KEY NOT NULL,
    name VARCHAR(64) UNIQUE NOT NULL CHECK(length(name) <= 64),
    content_type TEXT NOT NULL DEFAULT 'image',
    hk_interval INTEGER NOT NULL DEFAULT 3600000,
    hk_disk_space INTEGER NOT NULL DEFAULT 107374182400,
    hk_max_age INTEGER NOT NULL DEFAULT 0,
    create_preview BOOLEAN NOT NULL DEFAULT 0,
    auto_conversion TEXT NOT NULL DEFAULT 'none',
    n_max_queued INTEGER NOT NULL DEFAULT 0,
    hk_last_run INTEGER NOT NULL DEFAULT 0,
    entry_count INTEGER NOT NULL DEFAULT 0,
    total_disk_space_bytes INTEGER NOT NULL DEFAULT 0,
    queued_count INTEGER NOT NULL DEFAULT 0
);`

	const createCFSQL = `CREATE TABLE database_custom_fields (
    database_id VARCHAR(26) NOT NULL,
    field_id INTEGER NOT NULL CHECK(field_id >= 0 AND field_id <= 254),
    name VARCHAR(64) NOT NULL,
    type TEXT NOT NULL CHECK(type IN ('TEXT', 'INTEGER', 'REAL', 'BOOLEAN', 'COORDINATE')),
    is_indexed BOOLEAN NOT NULL DEFAULT 1,
    PRIMARY KEY (database_id, field_id),
    FOREIGN KEY (database_id) REFERENCES databases(id) ON DELETE CASCADE,
    UNIQUE (database_id, name)
);`

	const createPermSQL = `CREATE TABLE database_permissions (
    user_id VARCHAR(26) NOT NULL,
    database_id TEXT(26) NOT NULL,
    can_view BOOLEAN NOT NULL DEFAULT 0,
    can_create BOOLEAN NOT NULL DEFAULT 0,
    can_edit BOOLEAN NOT NULL DEFAULT 0,
    can_delete BOOLEAN NOT NULL DEFAULT 0,
    can_admin BOOLEAN NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, database_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (database_id) REFERENCES databases(id) ON DELETE CASCADE
);`

	stmts := []string{
		// Stage child tables that reference databases(id) before replacing databases,
		// so DROP TABLE never triggers ON DELETE CASCADE under active foreign_keys=ON.
		fmt.Sprintf("CREATE TEMP TABLE _mig_03006_cf AS SELECT %s FROM database_custom_fields;", cfCols),
		fmt.Sprintf("CREATE TEMP TABLE _mig_03006_perm AS SELECT %s FROM database_permissions;", permCols),
		"DROP TABLE database_custom_fields;",
		"DROP TABLE database_permissions;",

		"DROP INDEX IF EXISTS idx_databases_queued_count;",
		"ALTER TABLE databases RENAME TO databases_old;",
		createDatabasesSQL,
		fmt.Sprintf("INSERT INTO databases (%s) SELECT %s FROM databases_old;", dbCols, dbCols),
		"DROP TABLE databases_old;",
		"CREATE INDEX IF NOT EXISTS idx_databases_queued_count ON databases(queued_count) WHERE queued_count > 0;",

		createCFSQL,
		fmt.Sprintf("INSERT INTO database_custom_fields (%s) SELECT %s FROM _mig_03006_cf;", cfCols, cfCols),
		createPermSQL,
		fmt.Sprintf("INSERT INTO database_permissions (%s) SELECT %s FROM _mig_03006_perm;", permCols, permCols),

		"DROP TABLE _mig_03006_cf;",
		"DROP TABLE _mig_03006_perm;",
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed during databases table rebuild (%s): %w", stmt, err)
		}
	}
	return nil
}

func backfillQueuedCounts03006(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM databases")
	if err != nil {
		return fmt.Errorf("failed to list databases for queued_count backfill: %w", err)
	}
	var dbIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan database id: %w", err)
		}
		dbIDs = append(dbIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("failed to iterate database ids: %w", err)
	}
	rows.Close()

	for _, dbID := range dbIDs {
		tableName := "entries_" + dbID
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", tableName).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check entries table for %s: %w", dbID, err)
		}
		if !exists {
			continue
		}

		var queuedCount int64
		countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE status = 4`, tableName)
		if err := tx.QueryRowContext(ctx, countQuery).Scan(&queuedCount); err != nil {
			return fmt.Errorf("failed to count queued entries for %s: %w", dbID, err)
		}

		if _, err := tx.ExecContext(ctx, "UPDATE databases SET queued_count = ? WHERE id = ?", queuedCount, dbID); err != nil {
			return fmt.Errorf("failed to backfill queued_count for %s: %w", dbID, err)
		}
	}
	return nil
}

func verifyForeignKeys03006(ctx context.Context, tx *sql.Tx) error {
	fkRows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check;")
	if err != nil {
		return fmt.Errorf("failed to run foreign_key_check: %w", err)
	}
	defer fkRows.Close()

	if fkRows.Next() {
		var table, parent string
		var rowid int64
		var fkid int
		_ = fkRows.Scan(&table, &rowid, &parent, &fkid)
		return fmt.Errorf("foreign_key_check violation in table %s (rowid %d) -> %s", table, rowid, parent)
	}
	if err := fkRows.Err(); err != nil {
		return fmt.Errorf("foreign_key_check iteration error: %w", err)
	}
	return nil
}
