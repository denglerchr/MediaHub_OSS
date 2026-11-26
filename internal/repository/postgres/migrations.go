package postgres

import (
	"context"
	"fmt"
	"io/fs"

	"mediahub_oss/internal/repository/migrations"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

func (r *PostgresRepository) newMigrationProvider() (*goose.Provider, error) {
	subFS, err := fs.Sub(migrations.EmbedFS, "postgres")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve postgres migrations fs: %w", err)
	}

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres session locker: %w", err)
	}

	return goose.NewProvider(
		goose.DialectPostgres,
		r.DB,
		subFS,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
}

// GetMigrationVersion retrieves the current schema version (e.g. 3003).
func (r *PostgresRepository) GetMigrationVersion(ctx context.Context) (int, error) {
	provider, err := r.newMigrationProvider()
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
func (r *PostgresRepository) MigrateUp(ctx context.Context) error {
	provider, err := r.newMigrationProvider()
	if err != nil {
		return fmt.Errorf("failed to initialize migration provider: %w", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("failed to migrate database up: %w", err)
	}
	return nil
}

// MigrateDown rolls back the database by one version.
func (r *PostgresRepository) MigrateDown(ctx context.Context) error {
	provider, err := r.newMigrationProvider()
	if err != nil {
		return fmt.Errorf("failed to initialize migration provider: %w", err)
	}

	if _, err := provider.Down(ctx); err != nil {
		return fmt.Errorf("failed to migrate database down: %w", err)
	}
	return nil
}
