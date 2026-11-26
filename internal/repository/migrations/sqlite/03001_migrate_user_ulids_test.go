package sqlitemigrations

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite" // SQLite driver
)

func TestMigration03001(t *testing.T) {
	ctx := context.Background()

	// 1. Open an in-memory SQLite database
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	// 2. Create isolated Goose migration provider
	provider, err := NewProvider(db)
	if err != nil {
		t.Fatalf("failed to create migration provider: %v", err)
	}

	// 3. Migrate UP to version 3000 (pre-ULID)
	if _, err := provider.UpTo(ctx, 3000); err != nil {
		t.Fatalf("failed to migrate to version 3000: %v", err)
	}

	// 4. Insert test databases (required for permissions foreign key)
	_, err = db.ExecContext(ctx, `INSERT INTO databases (id, name, content_type) VALUES ('01HGFB9Z5W7ABCDEFGHJKMNPQR', 'test_db', 'image');`)
	if err != nil {
		t.Fatalf("failed to insert test database: %v", err)
	}

	// 5. Insert old users (integer IDs)
	_, err = db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, is_admin) VALUES (1, 'user1', 'hash1', 0);`)
	if err != nil {
		t.Fatalf("failed to insert old user 1: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, is_admin) VALUES (2, 'user2', 'hash2', 1);`)
	if err != nil {
		t.Fatalf("failed to insert old user 2: %v", err)
	}

	// 6. Insert old database permissions
	_, err = db.ExecContext(ctx, `INSERT INTO database_permissions (user_id, database_id, can_view, can_create, can_edit, can_delete) VALUES (1, '01HGFB9Z5W7ABCDEFGHJKMNPQR', 1, 1, 0, 0);`)
	if err != nil {
		t.Fatalf("failed to insert old permissions: %v", err)
	}

	// 7. Insert old refresh tokens
	_, err = db.ExecContext(ctx, `INSERT INTO refresh_tokens (id, user_id, token_hash, expiry) VALUES (1, 1, 'tokenhash1', 123456789);`)
	if err != nil {
		t.Fatalf("failed to insert old refresh tokens: %v", err)
	}

	// 8. Run migration 3001 (Up)
	if _, err := provider.UpTo(ctx, 3001); err != nil {
		t.Fatalf("failed to migrate to version 3001: %v", err)
	}

	// 9. Verify migration 3001 Up results
	// - Verify users table has new ULID format IDs and is_service_account column
	var user1ID, user2ID string
	var isServiceAccount1, isServiceAccount2 bool
	err = db.QueryRowContext(ctx, "SELECT id, is_service_account FROM users WHERE username = 'user1';").Scan(&user1ID, &isServiceAccount1)
	if err != nil {
		t.Fatalf("failed to query migrated user 1: %v", err)
	}
	err = db.QueryRowContext(ctx, "SELECT id, is_service_account FROM users WHERE username = 'user2';").Scan(&user2ID, &isServiceAccount2)
	if err != nil {
		t.Fatalf("failed to query migrated user 2: %v", err)
	}

	if len(user1ID) != 26 || len(user2ID) != 26 {
		t.Errorf("expected 26-character ULID string for user IDs, got user1: %s, user2: %s", user1ID, user2ID)
	}
	if isServiceAccount1 || isServiceAccount2 {
		t.Errorf("expected is_service_account to be false (0) for migrated users, got user1: %t, user2: %t", isServiceAccount1, isServiceAccount2)
	}

	// - Verify database_permissions is migrated and mapped to new ULIDs
	var permUserID, permDatabaseID string
	var canView, canCreate bool
	err = db.QueryRowContext(ctx, "SELECT user_id, database_id, can_view, can_create FROM database_permissions;").Scan(&permUserID, &permDatabaseID, &canView, &canCreate)
	if err != nil {
		t.Fatalf("failed to query migrated permissions: %v", err)
	}
	if permUserID != user1ID {
		t.Errorf("expected permission user_id to be %s, got %s", user1ID, permUserID)
	}
	if !canView || !canCreate {
		t.Errorf("expected permission flags to be true, got canView: %t, canCreate: %t", canView, canCreate)
	}

	// - Verify refresh_tokens is cleared
	var tokenCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM refresh_tokens;").Scan(&tokenCount)
	if err != nil {
		t.Fatalf("failed to query refresh token count: %v", err)
	}
	if tokenCount != 0 {
		t.Errorf("expected refresh_tokens table to be cleared, got count: %d", tokenCount)
	}

	// 10. Insert a token in version 3001 state (to test rollback clears it too)
	_, err = db.ExecContext(ctx, `INSERT INTO refresh_tokens (user_id, token_hash, expiry) VALUES (?, 'newtokenhash1', 123456789);`, user1ID)
	if err != nil {
		t.Fatalf("failed to insert token in v3001 state: %v", err)
	}

	// 11. Run migration 3001 (Down)
	if _, err := provider.DownTo(ctx, 3000); err != nil {
		t.Fatalf("failed to migrate down to version 3000: %v", err)
	}

	// 12. Verify migration 3001 Down results
	// - Verify users table has integer IDs and no is_service_account column
	var user1IDInt, user2IDInt int64
	err = db.QueryRowContext(ctx, "SELECT id FROM users WHERE username = 'user1';").Scan(&user1IDInt)
	if err != nil {
		t.Fatalf("failed to query rolled back user 1: %v", err)
	}
	err = db.QueryRowContext(ctx, "SELECT id FROM users WHERE username = 'user2';").Scan(&user2IDInt)
	if err != nil {
		t.Fatalf("failed to query rolled back user 2: %v", err)
	}

	// Verify is_service_account column is removed
	err = db.QueryRowContext(ctx, "SELECT is_service_account FROM users;").Scan(&isServiceAccount1)
	if err == nil {
		t.Errorf("expected error querying is_service_account column after rollback, but column still exists")
	}

	// - Verify database_permissions is rolled back and mapped to integer IDs
	var permUserIDInt int64
	err = db.QueryRowContext(ctx, "SELECT user_id FROM database_permissions;").Scan(&permUserIDInt)
	if err != nil {
		t.Fatalf("failed to query rolled back permissions: %v", err)
	}
	if permUserIDInt != user1IDInt {
		t.Errorf("expected rolled back permission user_id to be %d, got %d", user1IDInt, permUserIDInt)
	}

	// - Verify refresh_tokens is cleared after rollback
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM refresh_tokens;").Scan(&tokenCount)
	if err != nil {
		t.Fatalf("failed to query rolled back refresh token count: %v", err)
	}
	if tokenCount != 0 {
		t.Errorf("expected refresh_tokens table to be cleared on rollback, got count: %d", tokenCount)
	}
}
