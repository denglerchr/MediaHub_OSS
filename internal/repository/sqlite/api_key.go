package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlutil"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
)

// CreateAPIKey stores a new API key in the SQLite database.
func (r *SQLiteRepository) CreateAPIKey(ctx context.Context, apiKey repo.APIKey) (repo.APIKey, error) {
	if apiKey.ID.IsZero() {
		apiKey.ID = ulid.New()
	}
	if apiKey.CreatedAt.IsZero() {
		apiKey.CreatedAt = time.Now()
	}

	var expiresAtVal any = nil
	if !apiKey.ExpiresAt.IsZero() {
		expiresAtVal = apiKey.ExpiresAt.UnixMilli()
	}

	var lastUsedAtVal any = nil
	if !apiKey.LastUsedAt.IsZero() {
		lastUsedAtVal = apiKey.LastUsedAt.UnixMilli()
	}

	var scopeView = apiKey.Scope.HasAccess(repo.AccessView)
	var scopeCreate = apiKey.Scope.HasAccess(repo.AccessCreate)
	var scopeEdit = apiKey.Scope.HasAccess(repo.AccessEdit)
	var scopeDelete = apiKey.Scope.HasAccess(repo.AccessDelete)
	var scopeAdmin = apiKey.Scope.HasAccess(repo.AccessAdmin)

	query, args, err := r.Builder.Insert("api_keys").
		Columns(
			"id", "user_id", "name", "key_hash", "key_hint",
			"scope_view", "scope_create", "scope_edit", "scope_delete", "scope_admin",
			"created_at", "expires_at", "last_used_at",
		).
		Values(
			apiKey.ID, apiKey.UserID, apiKey.Name, apiKey.KeyHash, apiKey.KeyHint,
			scopeView, scopeCreate, scopeEdit, scopeDelete, scopeAdmin,
			apiKey.CreatedAt.UnixMilli(), expiresAtVal, lastUsedAtVal,
		).
		ToSql()
	if err != nil {
		return repo.APIKey{}, fmt.Errorf("failed to build insert api_key query: %w", err)
	}

	_, err = r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		// SQLite error for duplicate unique constraint (e.g., id or key_hash already taken)
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return repo.APIKey{}, customerrors.ErrConflict
		}
		return repo.APIKey{}, fmt.Errorf("failed to insert api_key: %w", err)
	}

	return apiKey, nil
}

// GetAPIKeyByID retrieves a single API key from the database by its ID.
func (r *SQLiteRepository) GetAPIKeyByID(ctx context.Context, id repo.ULID) (repo.APIKey, error) {
	query, args, err := r.Builder.Select(sqlutil.APIKeySelectColumns("")...).
		From("api_keys").
		Where(squirrel.Eq{"id": id}).
		ToSql()
	if err != nil {
		return repo.APIKey{}, fmt.Errorf("failed to build get api_key by id query: %w", err)
	}

	key, err := sqlutil.ScanAPIKey(r.DB.QueryRowContext(ctx, query, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.APIKey{}, customerrors.ErrNotFound
		}
		return repo.APIKey{}, fmt.Errorf("failed to execute get api_key by id query: %w", err)
	}

	return key, nil
}

// GetAPIKeyWithOwnerByHash retrieves both the API key and the owner details in a single query.
func (r *SQLiteRepository) GetAPIKeyWithOwnerByHash(ctx context.Context, keyHash string) (repo.APIKey, repo.User, error) {
	cols := append(sqlutil.APIKeySelectColumns("ak"), "u.id", "u.username", "u.password_hash", "u.is_admin", "u.account_type")
	query, args, err := r.Builder.Select(cols...).
		From("api_keys ak").
		Join("users u ON ak.user_id = u.id").
		Where(squirrel.Eq{"ak.key_hash": keyHash}).
		ToSql()
	if err != nil {
		return repo.APIKey{}, repo.User{}, fmt.Errorf("failed to build get api_key with owner query: %w", err)
	}

	key, user, err := sqlutil.ScanAPIKeyWithOwner(r.DB.QueryRowContext(ctx, query, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.APIKey{}, repo.User{}, customerrors.ErrNotFound
		}
		return repo.APIKey{}, repo.User{}, fmt.Errorf("failed to scan api_key with owner: %w", err)
	}

	return key, user, nil
}

// GetAPIKeysByUserID retrieves all API keys linked to a specific user ID.
func (r *SQLiteRepository) GetAPIKeysByUserID(ctx context.Context, userID repo.ULID) ([]repo.APIKey, error) {
	query, args, err := r.Builder.Select(sqlutil.APIKeySelectColumns("")...).
		From("api_keys").
		Where(squirrel.Eq{"user_id": userID}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get api_keys by user_id query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute get api_keys by user_id query: %w", err)
	}
	defer rows.Close()

	return sqlutil.ScanAPIKeyRows(rows)
}

// GetAllAPIKeys retrieves all API keys in the system.
func (r *SQLiteRepository) GetAllAPIKeys(ctx context.Context) ([]repo.APIKey, error) {
	query, args, err := r.Builder.Select(sqlutil.APIKeySelectColumns("")...).
		From("api_keys").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get all api_keys query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute get all api_keys query: %w", err)
	}
	defer rows.Close()

	return sqlutil.ScanAPIKeyRows(rows)
}

// UpdateAPIKey updates an existing API key's name, scopes, and expiration in SQLite.
func (r *SQLiteRepository) UpdateAPIKey(ctx context.Context, apiKey repo.APIKey) (repo.APIKey, error) {
	var expiresAtVal any = nil
	if !apiKey.ExpiresAt.IsZero() {
		expiresAtVal = apiKey.ExpiresAt.UnixMilli()
	}

	var lastUsedAtVal any = nil
	if !apiKey.LastUsedAt.IsZero() {
		lastUsedAtVal = apiKey.LastUsedAt.UnixMilli()
	}

	query, args, err := r.Builder.Update("api_keys").
		Set("name", apiKey.Name).
		Set("scope_view", apiKey.Scope.HasAccess(repo.AccessView)).
		Set("scope_create", apiKey.Scope.HasAccess(repo.AccessCreate)).
		Set("scope_edit", apiKey.Scope.HasAccess(repo.AccessEdit)).
		Set("scope_delete", apiKey.Scope.HasAccess(repo.AccessDelete)).
		Set("scope_admin", apiKey.Scope.HasAccess(repo.AccessAdmin)).
		Set("expires_at", expiresAtVal).
		Set("last_used_at", lastUsedAtVal).
		Where(squirrel.Eq{"id": apiKey.ID}).
		ToSql()
	if err != nil {
		return repo.APIKey{}, fmt.Errorf("failed to build update api_key query: %w", err)
	}

	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return repo.APIKey{}, fmt.Errorf("failed to execute update api_key: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return repo.APIKey{}, fmt.Errorf("failed to verify rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return repo.APIKey{}, customerrors.ErrNotFound
	}

	return apiKey, nil
}

// DeleteAPIKey removes an API key from the database.
func (r *SQLiteRepository) DeleteAPIKey(ctx context.Context, id repo.ULID) error {
	query, args, err := r.Builder.Delete("api_keys").
		Where(squirrel.Eq{"id": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build delete api_key query: %w", err)
	}

	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to execute delete api_key query: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return customerrors.ErrNotFound
	}

	return nil
}

// DeleteExpiredAPIKeys purges all API keys that have passed their expiration date.
func (r *SQLiteRepository) DeleteExpiredAPIKeys(ctx context.Context) (int64, error) {
	query, args, err := r.Builder.Delete("api_keys").
		Where("expires_at IS NOT NULL AND expires_at < ?", time.Now().UnixMilli()).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build delete expired api_keys query: %w", err)
	}

	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired api_keys: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve rows affected: %w", err)
	}

	return rowsAffected, nil
}

// UpdateAPIKeyLastUsed updates only the last_used_at field for the API Key.
func (r *SQLiteRepository) UpdateAPIKeyLastUsed(ctx context.Context, id repo.ULID, lastUsed time.Duration) error {
	query, args, err := r.Builder.Update("api_keys").
		Set("last_used_at", time.Now().Add(-lastUsed).UnixMilli()). // server-side computed time
		Where(squirrel.Eq{"id": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build update last_used query: %w", err)
	}

	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to execute update last_used: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return customerrors.ErrNotFound
	}

	return nil
}
