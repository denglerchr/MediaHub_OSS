package sqlite_test

import (
	"context"
	"errors"
	"testing"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	_ "mediahub_oss/internal/repository/migrations/sqlite"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/customerrors"

	"github.com/pressly/goose/v3"
)

func TestSQLite_CreateOIDCUser(t *testing.T) {
	ctx := context.Background()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
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

	// 5. Attempting to create duplicate (issuer, subject) should fail
	_, err = r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
		Issuer:            "https://auth.example.com",
		Subject:           "sub-alice-1",
		PreferredUsername: "alice",
		Email:             "alice@example.com",
	})
	if err == nil {
		t.Errorf("expected error when creating duplicate (issuer, subject), got nil")
	}

	// 6. Unknown OIDC identity returns ErrNotFound
	_, err = r.GetUserByOIDCIdentity(ctx, "https://auth.example.com", "unknown-sub")
	if !errors.Is(err, customerrors.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
