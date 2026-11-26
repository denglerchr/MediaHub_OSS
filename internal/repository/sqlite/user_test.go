package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/customerrors"
)

func TestSQLite_CreateOIDCUser(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// 1. Create a template user with permissions to clone
	templateUser, err := r.CreateUser(ctx, repo.User{
		Username:     "_oidc_user",
		PasswordHash: "secret",
		IsAdmin:      false,
		AccountType:  repo.AccountTypeLocal,
	})
	if err != nil {
		t.Fatalf("failed to create template user: %v", err)
	}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "media_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	err = r.SetUserPermissions(ctx, repo.UserPermissions{
		UserID:     templateUser.ID,
		DatabaseID: db.ID,
		Roles:      repo.NewAccessGrant(true, true, false, false, false),
	})
	if err != nil {
		t.Fatalf("failed to set permissions: %v", err)
	}

	// 2. Create first OIDC user (preferred_username: "alice")
	u1, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://auth.example.com",
		Subject:           "sub-alice-1",
		PreferredUsername: "alice",
		Email:             "alice@example.com",
		DefaultRightsUser: "_oidc_user",
	})
	if err != nil {
		t.Fatalf("failed to create oidc user 1: %v", err)
	}
	if u1.Username != "alice" {
		t.Errorf("expected username alice, got %s", u1.Username)
	}
	if u1.AccountType != repo.AccountTypeOIDC {
		t.Errorf("expected AccountTypeOIDC, got %v", u1.AccountType)
	}
	if u1.IsAdmin {
		t.Errorf("expected IsAdmin to be false")
	}

	// Verify permissions were cloned to u1
	perms, err := r.GetAllUserPermissions(ctx, u1.ID)
	if err != nil {
		t.Fatalf("failed to get permissions: %v", err)
	}
	if len(perms) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(perms))
	}
	if perms[0].DatabaseID != db.ID || !perms[0].Roles.HasAccess(repo.AccessView) || !perms[0].Roles.HasAccess(repo.AccessCreate) || perms[0].Roles.HasAccess(repo.AccessEdit) {
		t.Errorf("permission flags unexpected: %+v", perms[0])
	}

	// Verify lookup by OIDC identity
	found, err := r.GetUserByOIDCIdentity(ctx, "https://auth.example.com", "sub-alice-1")
	if err != nil {
		t.Fatalf("failed to get user by oidc identity: %v", err)
	}
	if found.ID != u1.ID || found.Username != "alice" {
		t.Errorf("found user mismatch: expected %s, got %s", u1.ID, found.ID)
	}

	// 3. Create second OIDC user with same preferred_username -> collision resolved to alice_2
	u2, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://auth.example.com",
		Subject:           "sub-alice-2",
		PreferredUsername: "alice",
		Email:             "alice2@example.com",
		DefaultRightsUser: "_oidc_user",
	})
	if err != nil {
		t.Fatalf("failed to create oidc user 2: %v", err)
	}
	if u2.Username != "alice_2" {
		t.Errorf("expected username alice_2, got %s", u2.Username)
	}
	if u2.ID == u1.ID {
		t.Errorf("expected distinct user IDs")
	}

	// 4. Create third OIDC user with same preferred_username -> collision resolved to alice_3
	u3, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://auth.example.com",
		Subject:           "sub-alice-3",
		PreferredUsername: "alice",
		Email:             "alice3@example.com",
		DefaultRightsUser: "",
	})
	if err != nil {
		t.Fatalf("failed to create oidc user 3: %v", err)
	}
	if u3.Username != "alice_3" {
		t.Errorf("expected username alice_3, got %s", u3.Username)
	}

	// No permissions cloned for u3 because DefaultRightsUser is empty
	perms3, err := r.GetAllUserPermissions(ctx, u3.ID)
	if err != nil {
		t.Fatalf("failed to get permissions: %v", err)
	}
	if len(perms3) != 0 {
		t.Errorf("expected 0 permissions for u3, got %d", len(perms3))
	}

	// 5. Attempting to create duplicate (issuer, subject) returns the existing user idempotently
	dupUser, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://auth.example.com",
		Subject:           "sub-alice-1",
		PreferredUsername: "alice",
		Email:             "alice@example.com",
	})
	if err != nil {
		t.Fatalf("expected duplicate (issuer, subject) to return existing user, got: %v", err)
	}
	if dupUser.ID != u1.ID || dupUser.Username != u1.Username {
		t.Errorf("expected existing user %+v, got %+v", u1, dupUser)
	}
	if _, err := r.GetUserByUsername(ctx, "alice_4"); !errors.Is(err, customerrors.ErrNotFound) {
		t.Errorf("expected rolled-back username alice_4 not to persist, got err: %v", err)
	}

	// 6. Unknown OIDC identity returns ErrNotFound
	_, err = r.GetUserByOIDCIdentity(ctx, "https://auth.example.com", "unknown-sub")
	if !errors.Is(err, customerrors.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLite_UpdateUser_DuplicateUsernameReturnsErrUserExists(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(context.Background()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	u1, err := r.CreateUser(ctx, repo.User{
		Username:     "user_one",
		PasswordHash: "hash1",
		AccountType:  repo.AccountTypeLocal,
	})
	if err != nil {
		t.Fatalf("failed to create u1: %v", err)
	}

	u2, err := r.CreateUser(ctx, repo.User{
		Username:     "user_two",
		PasswordHash: "hash2",
		AccountType:  repo.AccountTypeLocal,
	})
	if err != nil {
		t.Fatalf("failed to create u2: %v", err)
	}

	u2.Username = u1.Username
	_, err = r.UpdateUser(ctx, u2)
	if !errors.Is(err, customerrors.ErrUserExists) {
		t.Fatalf("expected ErrUserExists when updating username to an existing one, got: %v", err)
	}
}

func TestSQLite_LastAdminProtection(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	admin1, err := r.CreateUser(ctx, repo.User{
		Username:     "admin_one",
		PasswordHash: "hash1",
		IsAdmin:      true,
		AccountType:  repo.AccountTypeLocal,
	})
	if err != nil {
		t.Fatalf("failed to create admin1: %v", err)
	}
	member, err := r.CreateUser(ctx, repo.User{
		Username:     "member",
		PasswordHash: "hash_m",
		IsAdmin:      false,
		AccountType:  repo.AccountTypeLocal,
	})
	if err != nil {
		t.Fatalf("failed to create member: %v", err)
	}

	// 1. Sole admin cannot be deleted
	if err := r.DeleteUser(ctx, admin1.ID); !errors.Is(err, customerrors.ErrConflict) {
		t.Fatalf("expected ErrConflict deleting sole admin, got: %v", err)
	}

	// 2. Sole admin cannot be demoted
	demoted := admin1
	demoted.IsAdmin = false
	if _, err := r.UpdateUser(ctx, demoted); !errors.Is(err, customerrors.ErrConflict) {
		t.Fatalf("expected ErrConflict demoting sole admin, got: %v", err)
	}

	// 3. Updating sole admin while keeping IsAdmin = true succeeds
	renamedAdmin := admin1
	renamedAdmin.Username = "admin_primary"
	if _, err := r.UpdateUser(ctx, renamedAdmin); err != nil {
		t.Fatalf("expected updating sole admin without demotion to succeed, got: %v", err)
	}

	// 4. Non-admin user can still be deleted when 1 admin exists
	if err := r.DeleteUser(ctx, member.ID); err != nil {
		t.Fatalf("expected deleting non-admin user to succeed, got: %v", err)
	}

	// 5. Missing user returns ErrNotFound
	if err := r.DeleteUser(ctx, member.ID); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound deleting missing user, got: %v", err)
	}

	// 6. With two admins, one can be demoted and the other can be deleted when 2 admins exist
	admin2, err := r.CreateUser(ctx, repo.User{
		Username:     "admin_two",
		PasswordHash: "hash2",
		IsAdmin:      true,
		AccountType:  repo.AccountTypeLocal,
	})
	if err != nil {
		t.Fatalf("failed to create admin2: %v", err)
	}

	demote2 := admin2
	demote2.IsAdmin = false
	if _, err := r.UpdateUser(ctx, demote2); err != nil {
		t.Fatalf("expected demoting one of two admins to succeed, got: %v", err)
	}

	// Re-promote admin2, then delete admin1 -> succeeds leaving admin2 as sole admin
	if _, err := r.UpdateUser(ctx, admin2); err != nil {
		t.Fatalf("failed to re-promote admin2: %v", err)
	}
	if err := r.DeleteUser(ctx, admin1.ID); err != nil {
		t.Fatalf("expected deleting one of two admins to succeed, got: %v", err)
	}

	// Now admin2 is the sole admin and cannot be deleted or demoted
	if err := r.DeleteUser(ctx, admin2.ID); !errors.Is(err, customerrors.ErrConflict) {
		t.Fatalf("expected ErrConflict deleting new sole admin, got: %v", err)
	}
	demote2.IsAdmin = false
	if _, err := r.UpdateUser(ctx, demote2); !errors.Is(err, customerrors.ErrConflict) {
		t.Fatalf("expected ErrConflict demoting new sole admin, got: %v", err)
	}
}

func TestSQLite_CreateOIDCUser_Hardening(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// 1. Long >64 char username truncation + sequential collisions stay <= 64 chars
	longPref := strings.Repeat("a", 75)
	u1, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://issuer",
		Subject:           "sub-long-1",
		PreferredUsername: longPref,
	})
	if err != nil {
		t.Fatalf("failed creating long OIDC user 1: %v", err)
	}
	if len(u1.Username) != repo.MaxUsernameLength {
		t.Errorf("expected length %d, got %d (%q)", repo.MaxUsernameLength, len(u1.Username), u1.Username)
	}

	u2, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://issuer",
		Subject:           "sub-long-2",
		PreferredUsername: longPref,
	})
	if err != nil {
		t.Fatalf("failed creating long OIDC user 2: %v", err)
	}
	if len(u2.Username) > repo.MaxUsernameLength || !strings.HasSuffix(u2.Username, "_2") {
		t.Errorf("unexpected u2 username: %q", u2.Username)
	}

	u3, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://issuer",
		Subject:           "sub-long-3",
		PreferredUsername: longPref,
	})
	if err != nil {
		t.Fatalf("failed creating long OIDC user 3: %v", err)
	}
	if len(u3.Username) > repo.MaxUsernameLength || !strings.HasSuffix(u3.Username, "_3") {
		t.Errorf("unexpected u3 username: %q", u3.Username)
	}

	// 2. Wildcard characters (_, %, \) are escaped properly and created_at is set by DB clock
	specialPref := `user_a%b\c`
	s1, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://issuer",
		Subject:           "sub-special-1",
		PreferredUsername: specialPref,
	})
	if err != nil {
		t.Fatalf("failed creating special char OIDC user 1: %v", err)
	}
	if s1.Username != specialPref {
		t.Errorf("expected %q, got %q", specialPref, s1.Username)
	}
	s2, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://issuer",
		Subject:           "sub-special-2",
		PreferredUsername: specialPref,
	})
	if err != nil {
		t.Fatalf("failed creating special char OIDC user 2: %v", err)
	}
	if s2.Username != specialPref+"_2" {
		t.Errorf("expected %q, got %q", specialPref+"_2", s2.Username)
	}

	var createdAt int64
	if err := r.DB.QueryRowContext(ctx, "SELECT created_at FROM user_oidc_identities WHERE issuer = ? AND subject = ?", "https://issuer", "sub-special-1").Scan(&createdAt); err != nil {
		t.Fatalf("failed to query created_at: %v", err)
	}
	if createdAt <= 0 {
		t.Errorf("expected DB-populated created_at > 0, got %d", createdAt)
	}
}

func TestSQLite_ConsumeRefreshToken(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	u, err := r.CreateUser(ctx, repo.User{
		Username:     "token_user",
		PasswordHash: "hash",
		AccountType:  repo.AccountTypeLocal,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 1. Valid token is consumed once; second consume fails with ErrNotFound
	if err := r.StoreRefreshToken(ctx, u.ID, "valid-hash", time.Minute); err != nil {
		t.Fatalf("StoreRefreshToken failed: %v", err)
	}

	gotUserID, err := r.ConsumeRefreshToken(ctx, "valid-hash")
	if err != nil {
		t.Fatalf("ConsumeRefreshToken failed: %v", err)
	}
	if gotUserID != u.ID {
		t.Errorf("expected user ID %s, got %s", u.ID, gotUserID)
	}

	if _, err := r.ConsumeRefreshToken(ctx, "valid-hash"); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second consume, got: %v", err)
	}

	// 2. Expired token returns ErrNotFound
	if err := r.StoreRefreshToken(ctx, u.ID, "expired-hash", -time.Minute); err != nil {
		t.Fatalf("StoreRefreshToken (expired) failed: %v", err)
	}
	if _, err := r.ConsumeRefreshToken(ctx, "expired-hash"); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for expired token, got: %v", err)
	}

	// 3. Unknown token returns ErrNotFound
	if _, err := r.ConsumeRefreshToken(ctx, "does-not-exist"); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown token, got: %v", err)
	}
}
