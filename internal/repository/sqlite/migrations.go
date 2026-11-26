package sqlite

import (
	"context"
	"fmt"

	sqlitemigrations "mediahub_oss/internal/repository/migrations/sqlite"
)

// GetMigrationVersion retrieves the current schema version (e.g., 2000).
func (r *SQLiteRepository) GetMigrationVersion(ctx context.Context) (int, error) {
	provider, err := sqlitemigrations.NewProvider(r.DB)
	if err != nil {
		return 0, fmt.Errorf("failed to initialize migration provider: %w", err)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get database version: %w", err)
	}

	return int(version), nil
}

// MigrateUp applies all pending migrations.
func (r *SQLiteRepository) MigrateUp(ctx context.Context) error {
	provider, err := sqlitemigrations.NewProvider(r.DB)
	if err != nil {
		return fmt.Errorf("failed to initialize migration provider: %w", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("failed to migrate database up: %w", err)
	}
	return nil
}

// MigrateDown rolls back the database by one version.
func (r *SQLiteRepository) MigrateDown(ctx context.Context) error {
	provider, err := sqlitemigrations.NewProvider(r.DB)
	if err != nil {
		return fmt.Errorf("failed to initialize migration provider: %w", err)
	}

	if _, err := provider.Down(ctx); err != nil {
		return fmt.Errorf("failed to migrate database down: %w", err)
	}
	return nil
}
