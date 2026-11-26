package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"time"

	"github.com/Masterminds/squirrel"
)

// StoreRefreshToken inserts a new hashed refresh token into the database along with its expiry time.
func (r *SQLiteRepository) StoreRefreshToken(ctx context.Context, userID repo.ULID, tokenHash string, validDuration time.Duration) error {
	expiry := time.Now().Add(validDuration).UnixMilli()

	// Build the INSERT query using Squirrel
	query, args, err := r.Builder.Insert("refresh_tokens").
		Columns("user_id", "token_hash", "expiry").
		Values(userID, tokenHash, expiry).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build insert token query: %w", err)
	}

	// Execute the query
	_, err = r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to store refresh token: %w", err)
	}

	return nil
}

// ValidateRefreshToken checks if a refresh token hash exists and is not expired.
// Returns the associated user ID if the token is valid.
func (r *SQLiteRepository) ValidateRefreshToken(ctx context.Context, tokenHash string) (repo.ULID, error) {
	// Build the SELECT query to fetch the user ID and expiration time
	query, args, err := r.Builder.Select("user_id", "expiry").
		From("refresh_tokens").
		Where(squirrel.Eq{"token_hash": tokenHash}).
		ToSql()
	if err != nil {
		return repo.ULID{}, fmt.Errorf("failed to build validate token query: %w", err)
	}

	var userID repo.ULID
	var expiry int64

	// Execute the query and scan the results
	err = r.DB.QueryRowContext(ctx, query, args...).Scan(&userID, &expiry)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.ULID{}, customerrors.ErrNotFound
		}
		return repo.ULID{}, fmt.Errorf("failed to query refresh token: %w", err)
	}

	// Check if the token has expired
	if time.Now().After(time.UnixMilli(expiry)) {
		return repo.ULID{}, customerrors.ErrNotFound
	}

	// Token is valid and active
	return userID, nil
}

// ConsumeRefreshToken atomically deletes an unexpired refresh token and returns its associated user ID.
func (r *SQLiteRepository) ConsumeRefreshToken(ctx context.Context, tokenHash string) (repo.ULID, error) {
	const query = `DELETE FROM refresh_tokens
WHERE token_hash = ? AND expiry >= ?
RETURNING user_id`

	var userID repo.ULID
	if err := r.DB.QueryRowContext(ctx, query, tokenHash, time.Now().UnixMilli()).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.ULID{}, customerrors.ErrNotFound
		}
		return repo.ULID{}, fmt.Errorf("failed to consume refresh token: %w", err)
	}

	return userID, nil
}

// DeleteRefreshToken removes a specific refresh token from the database using its hash (e.g., upon logout).
func (r *SQLiteRepository) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	// Build the DELETE query using Squirrel
	query, args, err := r.Builder.Delete("refresh_tokens").
		Where(squirrel.Eq{"token_hash": tokenHash}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build delete token query: %w", err)
	}

	// Execute the query
	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete refresh token: %w", err)
	}

	// Check if a row was actually deleted
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to retrieve rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return customerrors.ErrNotFound
	}

	return nil
}

// DeleteExpiredRefreshTokens removes all tokens that have passed their expiration date.
// Returns the number of tokens that were purged.
func (r *SQLiteRepository) DeleteExpiredRefreshTokens(ctx context.Context) (int64, error) {
	// Build the DELETE query.
	query, args, err := r.Builder.Delete("refresh_tokens").
		Where("expiry < CAST(unixepoch('subsec') * 1000 AS INTEGER)").
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build delete expired tokens query: %w", err)
	}

	// Execute the query
	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired refresh tokens: %w", err)
	}

	// Retrieve how many expired tokens were cleaned up
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve rows affected: %w", err)
	}

	return rowsAffected, nil
}

// DeleteAllRefreshTokensForUser removes all active sessions for a specific user.
func (r *SQLiteRepository) DeleteAllRefreshTokensForUser(ctx context.Context, userID repo.ULID) error {
	// Build the DELETE query targeting the specific user_id
	query, args, err := r.Builder.Delete("refresh_tokens").
		Where(squirrel.Eq{"user_id": userID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build delete all tokens query: %w", err)
	}

	// Execute the query
	_, err = r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete all refresh tokens for user: %w", err)
	}

	// We do not check for rowsAffected == 0 here.
	// If the user had no active tokens, the desired state is already achieved!
	return nil
}
