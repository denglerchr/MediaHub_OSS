package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
)

// HouseKeepingRequired returns a list of databases where a housekeeping run is required,
// meaning the last housekeeping run was longer ago than the database specific interval.
func (r *SQLiteRepository) HouseKeepingRequired(ctx context.Context) ([]repo.Database, error) {
	// We build a WHERE clause that relies entirely on the SQLite engine's clock.
	// 1. hk_interval > 0 ensures we skip databases where housekeeping is disabled.
	// 2. We compare (last_run + interval) against the current SQLite millisecond timestamp.

	query, args, err := r.Builder.Select(
		"id", "name", "content_type", "hk_interval", "hk_disk_space", "hk_max_age",
		"create_preview", "auto_conversion", "n_max_queued", "hk_last_run",
		"entry_count", "total_disk_space_bytes", "queued_count").
		From("databases").
		Where("hk_interval > 0 AND hk_last_run + hk_interval <= CAST(unixepoch('subsec') * 1000 AS INTEGER)").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build housekeeping required query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query databases for housekeeping: %w", err)
	}
	defer rows.Close()

	var databases []repo.Database
	for rows.Next() {
		// Leverage your existing scanner helper from database_utils.go
		db, err := scanDatabaseRow(rows)
		if err != nil {
			return nil, err
		}
		databases = append(databases, db)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return databases, nil
}

// HouseKeepingWasCalled sets the LastHkRun to now (server timestamp),
// used by housekeeping to track when the last run was.
func (r *SQLiteRepository) HouseKeepingWasCalled(ctx context.Context, dbID repo.ULID) (time.Time, error) {
	if !shared.IsValidULID(dbID.String()) {
		return time.Time{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	// Get current server time in milliseconds
	now := time.Now()

	// Build the update query
	query, args, err := r.Builder.Update("databases").
		Set("hk_last_run", now.UnixMilli()).
		Where(squirrel.Eq{"id": dbID.String()}).
		ToSql()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to build housekeeping update query: %w", err)
	}

	// Execute the update
	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to update last housekeeping run: %w", err)
	}

	// Ensure the database actually existed and was updated
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to retrieve rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return time.Time{}, customerrors.ErrNotFound
	}

	return now, nil
}
