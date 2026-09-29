package sqlitemigrations

import (
	"context"
	"database/sql"
	"testing"

	"mediahub_oss/internal/repository/migrations"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestMigration03005_ForeignKeySafety(t *testing.T) {
	ctx := context.Background()

	// 1. Open an in-memory SQLite database with foreign_keys explicitly enabled
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	// Verify foreign_keys is on
	var fkEnabled int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&fkEnabled); err != nil || fkEnabled != 1 {
		t.Fatalf("foreign_keys pragma is not enabled: %v (val=%d)", err, fkEnabled)
	}

	// 2. Set Goose dialect and filesystem
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	Register()

	// 3. Migrate UP to version 3004 (prior to 3005)
	if err := goose.UpTo(db, "sqlite", 3004); err != nil {
		t.Fatalf("failed to migrate to version 3004: %v", err)
	}

	// 4. Insert test database and custom fields in 3004
	dbID := "01HGFB9Z5W7ABCDEFGHJKMNPQR"
	_, err = db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type) VALUES (?, 'test_db', 'image');`, dbID)
	if err != nil {
		t.Fatalf("failed to insert test database: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO database_custom_fields (database_id, field_id, name, type, is_indexed) VALUES (?, 0, 'caption', 'TEXT', 1);`, dbID)
	if err != nil {
		t.Fatalf("failed to insert custom field 0: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO database_custom_fields (database_id, field_id, name, type, is_indexed) VALUES (?, 1, 'rating', 'INTEGER', 0);`, dbID)
	if err != nil {
		t.Fatalf("failed to insert custom field 1: %v", err)
	}

	// 5. Run migration UP to 3005
	if err := goose.UpTo(db, "sqlite", 3005); err != nil {
		t.Fatalf("failed to migrate to version 3005: %v", err)
	}

	// 6. Verify that databases table still has the database
	var dbCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM databases WHERE id = ?;", dbID).Scan(&dbCount); err != nil || dbCount != 1 {
		t.Fatalf("database row was lost! count=%d, err=%v", dbCount, err)
	}

	// 7. Verify that custom fields were preserved
	var cfCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM database_custom_fields WHERE database_id = ?;", dbID).Scan(&cfCount); err != nil || cfCount != 2 {
		t.Fatalf("custom fields were lost! count=%d, err=%v", cfCount, err)
	}

	// 8. Verify that COORDINATE type can now be inserted
	_, err = db.ExecContext(ctx, `INSERT INTO database_custom_fields (database_id, field_id, name, type, is_indexed) VALUES (?, 2, 'location', 'COORDINATE', 1);`, dbID)
	if err != nil {
		t.Fatalf("failed to insert COORDINATE custom field after migration: %v", err)
	}

	// 9. Run PRAGMA foreign_key_check to verify integrity
	fkRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check;")
	if err != nil {
		t.Fatalf("failed to run foreign_key_check: %v", err)
	}
	defer fkRows.Close()
	if fkRows.Next() {
		var table, parent string
		var rowid int64
		var fkid int
		fkRows.Scan(&table, &rowid, &parent, &fkid)
		t.Fatalf("foreign_key_check failed: violation in table %s (rowid %d) -> %s", table, rowid, parent)
	}

	// 10. Verify CASCADE behavior still works when parent database is deleted
	_, err = db.ExecContext(ctx, `DELETE FROM databases WHERE id = ?;`, dbID)
	if err != nil {
		t.Fatalf("failed to delete database: %v", err)
	}

	var remainingCFs int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM database_custom_fields WHERE database_id = ?;", dbID).Scan(&remainingCFs); err != nil || remainingCFs != 0 {
		t.Fatalf("expected ON DELETE CASCADE to delete custom fields when database is deleted, remaining=%d", remainingCFs)
	}

	// 11. Test Down migration with new DB
	dbID2 := "01HGFB9Z5W7ABCDEFGHJKMNPQ2"
	_, err = db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type) VALUES (?, 'test_db2', 'image');`, dbID2)
	if err != nil {
		t.Fatalf("failed to insert test database 2: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO database_custom_fields (database_id, field_id, name, type, is_indexed) VALUES (?, 0, 'caption', 'TEXT', 1);`, dbID2)
	if err != nil {
		t.Fatalf("failed to insert custom field: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO database_custom_fields (database_id, field_id, name, type, is_indexed) VALUES (?, 1, 'location', 'COORDINATE', 1);`, dbID2)
	if err != nil {
		t.Fatalf("failed to insert coordinate field: %v", err)
	}

	// Migrate Down to 3004
	if err := goose.DownTo(db, "sqlite", 3004); err != nil {
		t.Fatalf("failed to migrate down to 3004: %v", err)
	}

	// Verify database row still exists
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM databases WHERE id = ?;", dbID2).Scan(&dbCount); err != nil || dbCount != 1 {
		t.Fatalf("database row was lost on Down migration! count=%d", dbCount)
	}

	// Verify TEXT field was kept, COORDINATE field was omitted
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM database_custom_fields WHERE database_id = ?;", dbID2).Scan(&cfCount); err != nil || cfCount != 1 {
		t.Fatalf("expected 1 custom field after Down migration, got %d", cfCount)
	}
}
