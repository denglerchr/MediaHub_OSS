package sqlitemigrations

import (
	"database/sql"
	"fmt"
	"io/fs"

	"mediahub_oss/internal/repository/migrations"

	"github.com/pressly/goose/v3"
)

// GoMigrations returns the SQLite Go migrations for explicit provider registration.
func GoMigrations() []*goose.Migration {
	return []*goose.Migration{
		goose.NewGoMigration(2001, &goose.GoFunc{RunTx: up02001}, &goose.GoFunc{RunTx: down02001}),
		goose.NewGoMigration(3000, &goose.GoFunc{RunTx: up03000}, &goose.GoFunc{RunTx: down03000}),
		goose.NewGoMigration(3001, &goose.GoFunc{RunTx: up03001}, &goose.GoFunc{RunTx: down03001}),
		goose.NewGoMigration(3006, &goose.GoFunc{RunTx: up03006}, &goose.GoFunc{RunTx: down03006}),
	}
}

// NewProvider constructs an isolated Goose migration provider for SQLite without
// mutating global Goose state.
func NewProvider(db *sql.DB) (*goose.Provider, error) {
	subFS, err := fs.Sub(migrations.EmbedFS, "sqlite")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve sqlite migrations fs: %w", err)
	}

	return goose.NewProvider(
		goose.DialectSQLite3,
		db,
		subFS,
		goose.WithGoMigrations(GoMigrations()...),
		goose.WithDisableGlobalRegistry(true),
	)
}
