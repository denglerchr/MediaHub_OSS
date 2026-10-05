package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mediahub_oss/internal/shared/customerrors"

	"github.com/Masterminds/squirrel"
)

// AcquireLock attempts to acquire an atomic distributed lock in a single database round-trip.
func (r *PostgresRepository) AcquireLock(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error) {
	// A non-positive TTL would produce an already-expired lock, silently turning
	// mutual exclusion into a no-op.
	if ttl <= 0 {
		return false, fmt.Errorf("%w: lock ttl must be positive", customerrors.ErrValidation)
	}

	// Convert the TTL into milliseconds to pass to the query, rounding up so that
	// positive sub-millisecond TTLs never truncate to 0 ms (which would also create
	// an already-expired lock).
	ttlMs := int64((ttl + time.Millisecond - 1) / time.Millisecond)

	// By using (EXTRACT(EPOCH FROM clock_timestamp()) * 1000), Postgres acts as the single source
	// of truth for time directly during the INSERT
	query, args, err := r.Builder.Insert("system_locks").
		Columns("lock_name", "locked_at", "locked_by", "expires_at").
		Values(
			lockName,
			squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT"),
			ownerID,
			squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 + ?)::BIGINT", ttlMs),
		).
		Suffix(`
            ON CONFLICT (lock_name) DO UPDATE 
            SET locked_at = EXCLUDED.locked_at, 
                locked_by = EXCLUDED.locked_by, 
                expires_at = EXCLUDED.expires_at 
            WHERE system_locks.expires_at < (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)
               OR system_locks.locked_by = EXCLUDED.locked_by
            RETURNING lock_name
        `).
		ToSql()

	if err != nil {
		return false, fmt.Errorf("failed to build acquire lock query: %w", err)
	}

	// Using QueryRowContext with RETURNING is more resilient than RowsAffected
	var returnedLock string
	err = r.DB.QueryRowContext(ctx, query, args...).Scan(&returnedLock)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No rows returned means the WHERE clause failed: the lock is held by someone else
			return false, nil
		}
		return false, fmt.Errorf("failed to execute acquire lock query: %w", err)
	}

	// If we got the lock name back, we successfully acquired or extended the lock
	return returnedLock == lockName, nil
}

// ReleaseLock removes a distributed lock if the owner matches.
// Returns true if the lock was successfully released, false if it was already expired/stolen.
func (r *PostgresRepository) ReleaseLock(ctx context.Context, lockName string, ownerID string) (bool, error) {
	query, args, err := r.Builder.Delete("system_locks").
		Where(squirrel.Eq{"lock_name": lockName, "locked_by": ownerID}).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("failed to build release lock query: %w", err)
	}

	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("failed to execute release lock: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to get rows affected: %w", err)
	}

	// If rowsAffected == 0, the lock expired and was stolen by another worker,
	// or it was already deleted.
	return rowsAffected == 1, nil
}
