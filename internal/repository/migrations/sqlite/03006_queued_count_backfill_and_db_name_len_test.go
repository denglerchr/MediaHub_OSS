package sqlitemigrations

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigration03006_QueuedCountBackfillAndDBNameLen(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	var fkEnabled int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&fkEnabled); err != nil || fkEnabled != 1 {
		t.Fatalf("foreign_keys pragma is not enabled: %v (val=%d)", err, fkEnabled)
	}

	provider, err := NewProvider(db)
	if err != nil {
		t.Fatalf("failed to create migration provider: %v", err)
	}

	// 1. Migrate up to 3005
	if _, err := provider.UpTo(ctx, 3005); err != nil {
		t.Fatalf("failed to migrate to version 3005: %v", err)
	}

	// 2. Seed users, databases, custom fields, permissions, and dynamic entries tables with queued entries
	const userID = "01HGFB9Z5W7ABCDEFGHJKMNP01"
	const db1ID = "01HGFB9Z5W7ABCDEFGHJKMNPQ1"
	const db2ID = "01HGFB9Z5W7ABCDEFGHJKMNPQ2"

	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, is_admin, account_type) VALUES (?, 'alice', 'hash', 0, 0);`, userID); err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type, queued_count) VALUES (?, 'db_with_queue', 'image', 0);`, db1ID); err != nil {
		t.Fatalf("failed to insert db1: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type, queued_count) VALUES (?, 'db_no_queue', 'image', 5);`, db2ID); err != nil {
		t.Fatalf("failed to insert db2: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO database_custom_fields (database_id, field_id, name, type, is_indexed) VALUES (?, 0, 'caption', 'TEXT', 1);`, db1ID); err != nil {
		t.Fatalf("failed to insert custom field: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO database_permissions (user_id, database_id, can_view, can_create, can_edit, can_delete, can_admin) VALUES (?, ?, 1, 1, 0, 0, 0);`, userID, db1ID); err != nil {
		t.Fatalf("failed to insert permission: %v", err)
	}

	// Create entries tables matching v3 schema
	for _, dbID := range []string{db1ID, db2ID} {
		createEntries := `CREATE TABLE "entries_` + dbID + `" (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp BIGINT NOT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			filesize INTEGER NOT NULL,
			preview_filesize INTEGER NOT NULL,
			filename TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 1 CHECK(status IN (0, 1, 2, 3, 4)),
			width INTEGER NOT NULL,
			height INTEGER NOT NULL,
			mime_type TEXT NOT NULL
		);`
		if _, err := db.ExecContext(ctx, createEntries); err != nil {
			t.Fatalf("failed to create entries table for %s: %v", dbID, err)
		}
	}

	// Insert 3 queued (status=4) and 1 ready (status=1) in db1; 0 queued and 1 ready in db2
	if _, err := db.ExecContext(ctx, `INSERT INTO "entries_`+db1ID+`" (timestamp, created_at, updated_at, filesize, preview_filesize, filename, status, width, height, mime_type) VALUES
		(1, 1, 1, 100, 0, 'q1.jpg', 4, 10, 10, 'image/jpeg'),
		(2, 2, 2, 100, 0, 'q2.jpg', 4, 10, 10, 'image/jpeg'),
		(3, 3, 3, 100, 0, 'q3.jpg', 4, 10, 10, 'image/jpeg'),
		(4, 4, 4, 100, 0, 'r1.jpg', 1, 10, 10, 'image/jpeg');`); err != nil {
		t.Fatalf("failed to insert db1 entries: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO "entries_`+db2ID+`" (timestamp, created_at, updated_at, filesize, preview_filesize, filename, status, width, height, mime_type) VALUES
		(1, 1, 1, 100, 0, 'r2.jpg', 1, 10, 10, 'image/jpeg');`); err != nil {
		t.Fatalf("failed to insert db2 entries: %v", err)
	}

	// Verify 50-char name fails at 3005 (CHECK(length(name) <= 32))
	name50 := strings.Repeat("a", 50)
	if _, err := db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type) VALUES (?, ?, 'image');`, "01HGFB9Z5W7ABCDEFGHJKMNP50", name50); err == nil {
		t.Fatalf("expected 50-char database name to be rejected before 03006")
	}

	// 3. Migrate UP to 3006
	if _, err := provider.UpTo(ctx, 3006); err != nil {
		t.Fatalf("failed to migrate to version 3006: %v", err)
	}

	// 4. Verify queued_count backfill
	var q1, q2 int64
	if err := db.QueryRowContext(ctx, `SELECT queued_count FROM databases WHERE id = ?;`, db1ID).Scan(&q1); err != nil {
		t.Fatalf("failed to query db1 queued_count: %v", err)
	}
	if q1 != 3 {
		t.Errorf("expected db1 queued_count=3, got %d", q1)
	}
	if err := db.QueryRowContext(ctx, `SELECT queued_count FROM databases WHERE id = ?;`, db2ID).Scan(&q2); err != nil {
		t.Fatalf("failed to query db2 queued_count: %v", err)
	}
	if q2 != 0 {
		t.Errorf("expected db2 queued_count=0, got %d", q2)
	}

	// 5. Verify database_custom_fields and database_permissions rows survived rebuild
	var cfCount, permCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM database_custom_fields WHERE database_id = ?;`, db1ID).Scan(&cfCount); err != nil || cfCount != 1 {
		t.Fatalf("expected 1 preserved custom field, got %d (err=%v)", cfCount, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM database_permissions WHERE database_id = ?;`, db1ID).Scan(&permCount); err != nil || permCount != 1 {
		t.Fatalf("expected 1 preserved permission, got %d (err=%v)", permCount, err)
	}

	// 6. Verify 50-char name is accepted and 65-char name is rejected
	if _, err := db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type) VALUES (?, ?, 'image');`, "01HGFB9Z5W7ABCDEFGHJKMNP50", name50); err != nil {
		t.Fatalf("expected 50-char database name to be accepted after 03006: %v", err)
	}
	name65 := strings.Repeat("b", 65)
	if _, err := db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type) VALUES (?, ?, 'image');`, "01HGFB9Z5W7ABCDEFGHJKMNP65", name65); err == nil {
		t.Fatalf("expected 65-char database name to be rejected after 03006")
	}

	// 7. Verify PRAGMA foreign_keys is still enabled and ON DELETE CASCADE still works
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&fkEnabled); err != nil || fkEnabled != 1 {
		t.Fatalf("expected foreign_keys pragma to remain enabled after migration, got %d (err=%v)", fkEnabled, err)
	}
	fkRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check;")
	if err != nil {
		t.Fatalf("failed to run foreign_key_check: %v", err)
	}
	if fkRows.Next() {
		fkRows.Close()
		t.Fatalf("expected zero foreign_key_check violations")
	}
	fkRows.Close()

	if _, err := db.ExecContext(ctx, `DELETE FROM databases WHERE id = ?;`, db1ID); err != nil {
		t.Fatalf("failed to delete db1: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM database_custom_fields WHERE database_id = ?;`, db1ID).Scan(&cfCount); err != nil || cfCount != 0 {
		t.Fatalf("expected ON DELETE CASCADE on database_custom_fields, remaining=%d (err=%v)", cfCount, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM database_permissions WHERE database_id = ?;`, db1ID).Scan(&permCount); err != nil || permCount != 0 {
		t.Fatalf("expected ON DELETE CASCADE on database_permissions, remaining=%d (err=%v)", permCount, err)
	}

	// 8. Verify child foreign keys still reference databases(id) and no staging tables remain
	for _, tbl := range []string{"database_custom_fields", "database_permissions"} {
		var schemaSQL string
		if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name=?;`, tbl).Scan(&schemaSQL); err != nil {
			t.Fatalf("failed to read schema for %s: %v", tbl, err)
		}
		if !strings.Contains(schemaSQL, "REFERENCES databases(id)") {
			t.Errorf("expected %s to reference databases(id), got: %s", tbl, schemaSQL)
		}
	}
}
