package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
)

// HouseKeepingRequired returns a list of databases where a housekeeping run is required.
func (r *PostgresRepository) HouseKeepingRequired(ctx context.Context) ([]repo.Database, error) {
	query, args, err := r.Builder.Select(
		"id", "name", "content_type", "hk_interval", "hk_disk_space", "hk_max_age",
		"create_preview", "auto_conversion", "n_max_queued", "hk_last_run",
		"entry_count", "total_disk_space_bytes", "queued_count").
		From("databases").
		Where("hk_interval > 0 AND hk_last_run + hk_interval <= CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)").
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

// HouseKeepingWasCalled sets the LastHkRun to now (server timestamp).
func (r *PostgresRepository) HouseKeepingWasCalled(ctx context.Context, dbID repo.ULID) (time.Time, error) {
	if !shared.IsValidULID(dbID.String()) {
		return time.Time{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	query, args, err := r.Builder.Update("databases").
		Set("hk_last_run", squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")).
		Where(squirrel.Eq{"id": dbID.String()}).
		Suffix("RETURNING hk_last_run").
		ToSql()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to build housekeeping update query: %w", err)
	}

	var hkLastRunMillis int64
	err = r.DB.QueryRowContext(ctx, query, args...).Scan(&hkLastRunMillis)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, customerrors.ErrNotFound
		}
		return time.Time{}, fmt.Errorf("failed to update last housekeeping run: %w", err)
	}

	return time.UnixMilli(hkLastRunMillis), nil
}
