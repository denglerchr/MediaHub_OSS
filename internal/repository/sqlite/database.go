// implements GetDatabase, GetDatabases, GetDatabaseStats,DeleteDatabase, CreateDatabase,
// UpdateDatabase, UpdateDatabaseLastHkRun

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
	"strings"

	"github.com/Masterminds/squirrel"
)

// CreateDatabase inserts the database metadata and dynamically provisions its dedicated entry table.
func (r *SQLiteRepository) CreateDatabase(ctx context.Context, db repo.Database) (repo.Database, error) {
	// Validation and assigning default values
	if !safeNameRegex.MatchString(db.Name) {
		return repo.Database{}, fmt.Errorf("%w: database name contains invalid characters", customerrors.ErrInvalidName)
	}

	// Generate ULID if not provided by the handler
	if db.ID == "" {
		db.ID = repo.ULID(shared.GenerateULID())
	} else if !shared.IsValidULID(db.ID.String()) {
		return repo.Database{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	var hkLastRunMs int64 = 0
	if !db.Housekeeping.LastHkRun.IsZero() {
		hkLastRunMs = db.Housekeeping.LastHkRun.UnixMilli()
	}

	// Assign sequential IDs for custom fields if not set or just force sequential
	for i := range db.CustomFields {
		db.CustomFields[i].ID = i
	}

	// 1. Generate the dynamic schema and index queries using the ID instead of Name
	createTableSQL, err := r.BuildDynamicTableSchema(db.ID.String(), db.ContentType, db.CustomFields)
	if err != nil {
		return repo.Database{}, fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
	}
	indexSQLs := BuildIndexesSQL(db.ID.String(), db.CustomFields)

	// 2. Execute within a transaction
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.Database{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert metadata into the main databases table (without custom_fields column)
	query, args, err := r.Builder.Insert("databases").
		Columns("id", "name", "content_type", "hk_interval", "hk_disk_space", "hk_max_age", "create_preview", "auto_conversion", "n_max_queued", "hk_last_run").
		Values(
			db.ID,
			db.Name,
			db.ContentType,
			db.Housekeeping.Interval.Milliseconds(), // Converted to ms
			db.Housekeeping.DiskSpace,
			db.Housekeeping.MaxAge.Milliseconds(), // Converted to ms
			db.Config.CreatePreview,
			db.Config.AutoConversion,
			db.NMaxQueued,
			hkLastRunMs,
		).
		ToSql()
	if err != nil {
		return repo.Database{}, fmt.Errorf("failed to build insert query: %w", err)
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		// SQLite error 19 is UNIQUE constraint failed, meaning database name already exists
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return repo.Database{}, customerrors.ErrDatabaseExists
		}
		return repo.Database{}, fmt.Errorf("failed to insert database record: %w", err)
	}

	// Insert custom fields
	for _, cf := range db.CustomFields {
		datatype := cf.Type.String()
		cfQuery, cfArgs, err := r.Builder.Insert("database_custom_fields").
			Columns("database_id", "field_id", "name", "type", "is_indexed").
			Values(db.ID, cf.ID, cf.Name, datatype, cf.IsIndexed).
			ToSql()
		if err != nil {
			return repo.Database{}, fmt.Errorf("failed to build custom field insert query: %w", err)
		}
		if _, err := tx.ExecContext(ctx, cfQuery, cfArgs...); err != nil {
			return repo.Database{}, fmt.Errorf("failed to insert custom field: %w", err)
		}
	}

	// Provision the dynamic entry table
	if _, err := tx.ExecContext(ctx, createTableSQL); err != nil {
		return repo.Database{}, fmt.Errorf("failed to create dynamic table: %w", err)
	}

	// Create supporting indexes
	for _, idxSQL := range indexSQLs {
		if _, err := tx.ExecContext(ctx, idxSQL); err != nil {
			return repo.Database{}, fmt.Errorf("failed to create index: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return repo.Database{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return db, nil
}

// GetDatabase retrieves a single database configuration by its ULID.
func (r *SQLiteRepository) GetDatabase(ctx context.Context, dbID repo.ULID) (repo.Database, error) {
	if !shared.IsValidULID(dbID.String()) {
		return repo.Database{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	query, args, err := r.Builder.Select("id", "name", "content_type", "hk_interval", "hk_disk_space", "hk_max_age", "create_preview", "auto_conversion", "n_max_queued", "hk_last_run", "entry_count", "total_disk_space_bytes").
		From("databases").
		Where(squirrel.Eq{"id": dbID.String()}).
		ToSql()
	if err != nil {
		return repo.Database{}, fmt.Errorf("failed to build select query: %w", err)
	}

	row := r.DB.QueryRowContext(ctx, query, args...)
	db, err := scanDatabaseRow(row)
	if err != nil {
		return repo.Database{}, err
	}

	// Load custom fields
	cfs, err := r.getCustomFields(ctx, db.ID)
	if err != nil {
		return repo.Database{}, err
	}
	db.CustomFields = cfs

	return db, nil
}

// GetDatabases retrieves all available database configurations.
func (r *SQLiteRepository) GetDatabases(ctx context.Context) ([]repo.Database, error) {
	query, args, err := r.Builder.Select("id", "name", "content_type", "hk_interval", "hk_disk_space", "hk_max_age", "create_preview", "auto_conversion", "n_max_queued", "hk_last_run", "entry_count", "total_disk_space_bytes").
		From("databases").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build select query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
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
	rows.Close()

	if len(databases) == 0 {
		return databases, nil
	}

	// Fetch all custom fields and group them by database ID
	cfQuery, cfArgs, err := r.Builder.Select("database_id", "field_id", "name", "type", "is_indexed").
		From("database_custom_fields").
		OrderBy("database_id", "field_id").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build custom fields select: %w", err)
	}

	cfRows, err := r.DB.QueryContext(ctx, cfQuery, cfArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute custom fields query: %w", err)
	}
	defer cfRows.Close()

	cfMap := make(map[string][]repo.CustomFieldDef)
	for cfRows.Next() {
		var dbID string
		var cf repo.CustomFieldDef
		if err := cfRows.Scan(&dbID, &cf.ID, &cf.Name, &cf.Type, &cf.IsIndexed); err != nil {
			return nil, fmt.Errorf("failed to scan custom field: %w", err)
		}
		cfMap[dbID] = append(cfMap[dbID], cf)
	}
	if err := cfRows.Err(); err != nil {
		return nil, fmt.Errorf("custom fields row iteration error: %w", err)
	}

	for i := range databases {
		if cfs, ok := cfMap[databases[i].ID.String()]; ok {
			databases[i].CustomFields = cfs
		} else {
			databases[i].CustomFields = []repo.CustomFieldDef{}
		}
	}

	return databases, nil
}

// UpdateDatabase updates the mutable configuration fields of a database, including its name.
func (r *SQLiteRepository) UpdateDatabase(ctx context.Context, db repo.Database) (repo.Database, error) {
	if !shared.IsValidULID(db.ID.String()) {
		return repo.Database{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	var hkLastRunMs int64 = 0
	if !db.Housekeeping.LastHkRun.IsZero() {
		hkLastRunMs = db.Housekeeping.LastHkRun.UnixMilli()
	}

	query, args, err := r.Builder.Update("databases").
		Set("name", db.Name).                                        // We can now safely update the name!
		Set("hk_interval", db.Housekeeping.Interval.Milliseconds()). // Converted to ms
		Set("hk_disk_space", db.Housekeeping.DiskSpace).
		Set("hk_max_age", db.Housekeeping.MaxAge.Milliseconds()). // Converted to ms
		Set("hk_last_run", hkLastRunMs).
		Set("create_preview", db.Config.CreatePreview).
		Set("auto_conversion", db.Config.AutoConversion).
		Set("n_max_queued", db.NMaxQueued).
		Set("entry_count", db.Stats.EntryCount).
		Set("total_disk_space_bytes", db.Stats.TotalDiskSpaceBytes).
		Where(squirrel.Eq{"id": db.ID}).
		ToSql()
	if err != nil {
		return repo.Database{}, fmt.Errorf("failed to build update query: %w", err)
	}

	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return repo.Database{}, fmt.Errorf("failed to execute update: %w", err)
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return repo.Database{}, customerrors.ErrNotFound
	}

	return r.GetDatabase(ctx, db.ID)
}

// DeleteDatabase permanently removes a database, its entries table, and cascades to permissions.
func (r *SQLiteRepository) DeleteDatabase(ctx context.Context, dbID repo.ULID) error {
	if !shared.IsValidULID(dbID.String()) {
		return fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Drop the dynamic entry table using the ULID
	dropTableSQL := fmt.Sprintf(`DROP TABLE IF EXISTS "entries_%s"`, dbID.String())
	if _, err := tx.ExecContext(ctx, dropTableSQL); err != nil {
		return fmt.Errorf("failed to drop dynamic table: %w", err)
	}

	// Delete from the main metadata table (permissions cascade automatically)
	query, args, err := r.Builder.Delete("databases").Where(squirrel.Eq{"id": dbID.String()}).ToSql()
	if err != nil {
		return fmt.Errorf("failed to build delete query: %w", err)
	}

	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete database record: %w", err)
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return customerrors.ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Invalidate custom fields cache for the deleted database
	r.Cache.Delete("cf:" + dbID.String())

	return nil
}

// GetDatabaseStats retrieves live statistics for a specific database by its ID.
func (r *SQLiteRepository) GetDatabaseStats(ctx context.Context, dbID repo.ULID) (repo.DatabaseStats, error) {
	if !shared.IsValidULID(dbID.String()) {
		return repo.DatabaseStats{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	query, args, err := r.Builder.Select("entry_count", "total_disk_space_bytes").
		From("databases").
		Where(squirrel.Eq{"id": dbID.String()}).
		ToSql()
	if err != nil {
		return repo.DatabaseStats{}, fmt.Errorf("failed to build select query: %w", err)
	}

	var stats repo.DatabaseStats
	err = r.DB.QueryRowContext(ctx, query, args...).Scan(&stats.EntryCount, &stats.TotalDiskSpaceBytes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.DatabaseStats{}, customerrors.ErrNotFound
		}
		return repo.DatabaseStats{}, fmt.Errorf("failed to query database stats: %w", err)
	}

	return stats, nil
}
