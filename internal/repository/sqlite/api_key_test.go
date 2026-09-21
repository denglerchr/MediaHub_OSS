package sqlite_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	_ "mediahub_oss/internal/repository/migrations/sqlite"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/customerrors"

	"github.com/pressly/goose/v3"
)

func TestAPIKeysRepository(t *testing.T) {
	ctx := context.Background()

	// 1. Initialize SQLite repository in memory
	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	// 2. Run all migrations up to latest (RequiredVersion = 3002)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// 3. Create a test user
	userModel := repo.User{
		Username:     "test_owner",
		PasswordHash: "somehash",
		IsAdmin:      false,
		AccountType:  repo.AccountTypeService,
	}
	createdUser, err := r.CreateUser(ctx, userModel)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 4. Generate keys details
	secret1 := "6b89f8c68c12a4b872b22ad716d9a1b2"
	hashBytes1 := sha256.Sum256([]byte(secret1))
	hash1 := hex.EncodeToString(hashBytes1[:])
	hint1 := "srv_...a1b2"

	secret2 := "fb89f8c68c12a4b872b22ad716d9a1c3"
	hashBytes2 := sha256.Sum256([]byte(secret2))
	hash2 := hex.EncodeToString(hashBytes2[:])
	hint2 := "srv_...a1c3"

	// 5. Test CreateAPIKey
	key1 := repo.APIKey{
		UserID:    createdUser.ID,
		Name:      "key_active",
		KeyHash:   hash1,
		KeyHint:   hint1,
		Scope:     repo.NewAccessGrant(true, true, false, false, false),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	createdKey1, err := r.CreateAPIKey(ctx, key1)
	if err != nil {
		t.Fatalf("failed to create api key 1: %v", err)
	}
	if createdKey1.ID == "" {
		t.Errorf("expected generated ULID ID, got empty string")
	}

	// Create key2 that is expired
	key2 := repo.APIKey{
		UserID:    createdUser.ID,
		Name:      "key_expired",
		KeyHash:   hash2,
		KeyHint:   hint2,
		Scope:     repo.NewAccessGrant(true, false, false, false, false),
		ExpiresAt: time.Now().Add(-1 * time.Hour), // Expired
	}
	createdKey2, err := r.CreateAPIKey(ctx, key2)
	if err != nil {
		t.Fatalf("failed to create api key 2: %v", err)
	}

	// 6. Test GetAPIKeyByID
	retrievedKey1, err := r.GetAPIKeyByID(ctx, createdKey1.ID)
	if err != nil {
		t.Fatalf("failed to get api key by id: %v", err)
	}
	if retrievedKey1.Name != key1.Name {
		t.Errorf("expected name %s, got %s", key1.Name, retrievedKey1.Name)
	}
	if !retrievedKey1.Scope.HasAccess(repo.AccessCreate) {
		t.Errorf("expected ScopeCreate true, got false")
	}

	// 7. Test GetAPIKeyByHash
	hashRetrieved, err := r.GetAPIKeyByHash(ctx, hash1)
	if err != nil {
		t.Fatalf("failed to get api key by hash: %v", err)
	}
	if hashRetrieved.ID != createdKey1.ID {
		t.Errorf("expected key ID %s, got %s", createdKey1.ID, hashRetrieved.ID)
	}

	// 8. Test GetAPIKeyWithOwnerByHash
	keyWithOwner, ownerUser, err := r.GetAPIKeyWithOwnerByHash(ctx, hash1)
	if err != nil {
		t.Fatalf("failed to get api key with owner: %v", err)
	}
	if keyWithOwner.ID != createdKey1.ID {
		t.Errorf("expected key ID %s, got %s", createdKey1.ID, keyWithOwner.ID)
	}
	if ownerUser.ID != createdUser.ID {
		t.Errorf("expected owner user ID %s, got %s", createdUser.ID, ownerUser.ID)
	}
	if ownerUser.Username != "test_owner" {
		t.Errorf("expected username test_owner, got %s", ownerUser.Username)
	}

	// 9. Test GetAPIKeysByUserID
	userKeys, err := r.GetAPIKeysByUserID(ctx, createdUser.ID)
	if err != nil {
		t.Fatalf("failed to get api keys by user ID: %v", err)
	}
	if len(userKeys) != 2 {
		t.Errorf("expected 2 user keys, got %d", len(userKeys))
	}

	// 10. Test GetAllAPIKeys
	allKeys, err := r.GetAllAPIKeys(ctx)
	if err != nil {
		t.Fatalf("failed to get all api keys: %v", err)
	}
	if len(allKeys) != 2 {
		t.Errorf("expected 2 global keys, got %d", len(allKeys))
	}

	// 11. Test UpdateAPIKey
	createdKey1.Name = "updated_name"
	createdKey1.Scope = repo.NewAccessGrant(true, false, false, false, false)
	updatedKey, err := r.UpdateAPIKey(ctx, createdKey1)
	if err != nil {
		t.Fatalf("failed to update api key: %v", err)
	}
	if updatedKey.Name != "updated_name" {
		t.Errorf("expected updated name updated_name, got %s", updatedKey.Name)
	}
	if updatedKey.Scope.HasAccess(repo.AccessCreate) {
		t.Errorf("expected scope_create false, got true")
	}

	// Verify DB change
	retrievedUpdated, err := r.GetAPIKeyByID(ctx, createdKey1.ID)
	if err != nil {
		t.Fatalf("failed to fetch updated key: %v", err)
	}
	if retrievedUpdated.Name != "updated_name" {
		t.Errorf("expected name in DB updated_name, got %s", retrievedUpdated.Name)
	}

	// 12. Test UpdateAPIKeyLastUsed
	now := time.Now()
	err = r.UpdateAPIKeyLastUsed(ctx, createdKey1.ID, 0)
	if err != nil {
		t.Fatalf("failed to update last used: %v", err)
	}
	lastUsedRetrieved, err := r.GetAPIKeyByID(ctx, createdKey1.ID)
	if err != nil {
		t.Fatalf("failed to fetch after last used update: %v", err)
	}
	if lastUsedRetrieved.LastUsedAt.IsZero() {
		t.Errorf("expected last_used_at to be non-zero")
	}
	if lastUsedRetrieved.LastUsedAt.Unix() != now.Unix() {
		t.Errorf("expected last_used_at timestamp %v, got %v", now.Unix(), lastUsedRetrieved.LastUsedAt.Unix())
	}

	// 13. Test DeleteExpiredAPIKeys
	deletedCount, err := r.DeleteExpiredAPIKeys(ctx)
	if err != nil {
		t.Fatalf("failed to delete expired keys: %v", err)
	}
	if deletedCount != 1 {
		t.Errorf("expected 1 deleted expired key, got %d", deletedCount)
	}

	// Verify key2 was deleted, key1 remains
	_, err = r.GetAPIKeyByID(ctx, createdKey2.ID)
	if !errors.Is(err, customerrors.ErrNotFound) {
		t.Errorf("expected expired key2 to be not found, got err: %v", err)
	}
	_, err = r.GetAPIKeyByID(ctx, createdKey1.ID)
	if err != nil {
		t.Errorf("expected key1 to still exist, got err: %v", err)
	}

	// 14. Test DeleteAPIKey
	err = r.DeleteAPIKey(ctx, createdKey1.ID)
	if err != nil {
		t.Fatalf("failed to delete api key: %v", err)
	}
	_, err = r.GetAPIKeyByID(ctx, createdKey1.ID)
	if !errors.Is(err, customerrors.ErrNotFound) {
		t.Errorf("expected key1 to be deleted, got err: %v", err)
	}

	// 15. Test Cascade Deletion when User is Deleted
	// First, create a new key linked to our user
	key3 := repo.APIKey{
		UserID:  createdUser.ID,
		Name:    "key_to_cascade",
		KeyHash: hash2,
		KeyHint: hint2,
		Scope:   repo.NewAccessGrant(true, false, false, false, false),
	}
	createdKey3, err := r.CreateAPIKey(ctx, key3)
	if err != nil {
		t.Fatalf("failed to create api key 3: %v", err)
	}

	// Delete user
	err = r.DeleteUser(ctx, createdUser.ID)
	if err != nil {
		t.Fatalf("failed to delete user: %v", err)
	}

	// Key 3 should be cascaded and deleted automatically
	_, err = r.GetAPIKeyByID(ctx, createdKey3.ID)
	if !errors.Is(err, customerrors.ErrNotFound) {
		t.Errorf("expected key3 to be cascaded deleted, got err: %v", err)
	}
}
