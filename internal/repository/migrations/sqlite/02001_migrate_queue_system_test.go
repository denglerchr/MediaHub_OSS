package sqlitemigrations

// Regression tests for up02001 (queue system & custom fields migration).
// The migration rebuilds every entries_* table and copies its rows by column
// name; these tests pin down the data-fidelity guarantees that were previously
// broken (positional INSERT ... SELECT * copy and hardcoded is_indexed = 1).

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"mediahub_oss/internal/repository/migrations"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestMigration02001_CustomFieldsAndTableRebuild(t *testing.T) {
	ctx := context.Background()

	// 1. Open an in-memory SQLite database
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	// 2. Set Goose dialect and filesystem
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	Register()

	// 3. Migrate UP to version 2000 (pre-02001 state)
	if err := goose.UpTo(db, "sqlite", 2000); err != nil {
		t.Fatalf("failed to migrate to version 2000: %v", err)
	}

	const dbID = "01HGFB9Z5W7ABCDEFGHJKMNPQR"

	// 4. Pre-state: a database with old JSON custom_fields. "desc" is explicitly
	// NOT indexed, "rating" is indexed — the migration used to lose this flag.
	_, err = db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type, custom_fields) VALUES (?, 'test_db', 'image', ?)`,
		dbID,
		`[{"ID":0,"Name":"desc","Type":"TEXT","IsIndexed":false},{"ID":1,"Name":"rating","Type":"INTEGER","IsIndexed":true}]`)
	if err != nil {
		t.Fatalf("failed to insert database: %v", err)
	}

	// 5. Old entries table: columns named cf_<name>, status CHECK without 4, and
	// a column ORDER deliberately different from the new schema (mime_type before
	// the media fields, cf_rating before cf_desc) — the exact case where the old
	// positional `INSERT ... SELECT *` copy silently shifted values.
	_, err = db.ExecContext(ctx, `CREATE TABLE "entries_`+dbID+`" (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp BIGINT NOT NULL,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		filesize INTEGER NOT NULL,
		preview_filesize INTEGER NOT NULL,
		filename TEXT NOT NULL DEFAULT '',
		status INTEGER NOT NULL DEFAULT 0 CHECK(status IN (0, 1, 2, 3)),
		mime_type TEXT NOT NULL,
		width INTEGER NOT NULL,
		height INTEGER NOT NULL,
		cf_rating INTEGER,
		cf_desc TEXT
	)`)
	if err != nil {
		t.Fatalf("failed to create old entries table: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO "entries_`+dbID+`"
		(timestamp, created_at, updated_at, filesize, preview_filesize, filename, status, mime_type, width, height, cf_rating, cf_desc)
		VALUES (111, 222, 333, 123, 9, 'a.png', 1, 'image/png', 800, 600, 5, 'hello')`)
	if err != nil {
		t.Fatalf("failed to insert old row: %v", err)
	}

	// 6. Run migration 02001
	if err := goose.UpTo(db, "sqlite", 2001); err != nil {
		t.Fatalf("failed to migrate to version 2001: %v", err)
	}

	// 7. is_indexed must be preserved (desc=false, rating=true)
	var descIndexed, ratingIndexed bool
	if err := db.QueryRowContext(ctx, `SELECT is_indexed FROM database_custom_fields WHERE database_id=? AND name='desc'`, dbID).Scan(&descIndexed); err != nil {
		t.Fatalf("failed to query migrated field 'desc': %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT is_indexed FROM database_custom_fields WHERE database_id=? AND name='rating'`, dbID).Scan(&ratingIndexed); err != nil {
		t.Fatalf("failed to query migrated field 'rating': %v", err)
	}
	if descIndexed {
		t.Errorf("expected is_indexed=false for 'desc', got true")
	}
	if !ratingIndexed {
		t.Errorf("expected is_indexed=true for 'rating', got false")
	}

	// 8. Indexes must only be created for indexed fields (cf_1 = rating)
	var idxCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, "idx_entries_"+dbID+"_cf_0").Scan(&idxCount); err != nil {
		t.Fatalf("failed to query index cf_0: %v", err)
	}
	if idxCount != 0 {
		t.Errorf("index for non-indexed field cf_0 must not exist")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, "idx_entries_"+dbID+"_cf_1").Scan(&idxCount); err != nil {
		t.Fatalf("failed to query index cf_1: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("index for indexed field cf_1 must exist")
	}

	// 9. Row data must be mapped by NAME despite the different old column order
	var ts, created, updated, filesize, preview, status, width, height int64
	var filename, mime, cf0 any
	var cf1 int64
	err = db.QueryRowContext(ctx, `SELECT timestamp, created_at, updated_at, filesize, preview_filesize, filename, status, mime_type, width, height, cf_0, cf_1 FROM "entries_`+dbID+`"`).
		Scan(&ts, &created, &updated, &filesize, &preview, &filename, &status, &mime, &width, &height, &cf0, &cf1)
	if err != nil {
		t.Fatalf("failed to query migrated row: %v", err)
	}
	if ts != 111 || created != 222 || updated != 333 {
		t.Errorf("timestamps misaligned: timestamp=%d created_at=%d updated_at=%d", ts, created, updated)
	}
	if filesize != 123 || preview != 9 {
		t.Errorf("sizes misaligned: filesize=%d preview_filesize=%d", filesize, preview)
	}
	if filename != "a.png" || mime != "image/png" {
		t.Errorf("filename/mime_type misaligned: filename=%v mime_type=%v", filename, mime)
	}
	if status != 1 {
		t.Errorf("status misaligned: %d", status)
	}
	if width != 800 || height != 600 {
		t.Errorf("media fields misaligned: width=%d height=%d", width, height)
	}
	if cf0 != "hello" {
		t.Errorf("cf_0 (desc) misaligned: %v", cf0)
	}
	if cf1 != 5 {
		t.Errorf("cf_1 (rating) misaligned: %d", cf1)
	}

	// 10. The rebuilt table must allow status 4 (queued)
	var sqlSchema string
	if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, "entries_"+dbID).Scan(&sqlSchema); err != nil {
		t.Fatalf("failed to query rebuilt table schema: %v", err)
	}
	if !strings.Contains(sqlSchema, "status IN (0, 1, 2, 3, 4)") {
		t.Errorf("rebuilt schema must allow status 4, got: %s", sqlSchema)
	}
}
