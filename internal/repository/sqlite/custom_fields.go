package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"

	"github.com/Masterminds/squirrel"
)

// GetCustomFields retrieves all custom fields for a specific database.
func (r *SQLiteRepository) GetCustomFields(ctx context.Context, dbID repo.ULID) ([]repo.CustomFieldDef, error) {
	if !shared.IsValidULID(dbID.String()) {
		return nil, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	// First check if database exists to return 404 if not found
	var exists bool
	err := r.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM databases WHERE id = ?)", dbID.String()).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check database existence: %w", err)
	}
	if !exists {
		return nil, customerrors.ErrNotFound
	}
	return r.getCustomFields(ctx, dbID)
}

// getCustomFields retrieves all custom fields for a specific database backed by in-memory cache.
func (r *SQLiteRepository) getCustomFields(ctx context.Context, dbID repo.ULID) ([]repo.CustomFieldDef, error) {
	cacheKey := "cf:" + dbID.String()
	if val, found := r.Cache.Get(cacheKey); found {
		return val.([]repo.CustomFieldDef), nil
	}

	fields, err := r.queryCustomFields(ctx, r.DB, dbID)
	if err != nil {
		return nil, err
	}

	r.Cache.Set(cacheKey, fields, 5*time.Minute)
	return fields, nil
}

// queryCustomFields executes a raw SQL query to retrieve custom fields from the database.
// It is a pure database helper that executes on any Queryer (*sql.DB or *sql.Tx) without touching cache.
func (r *SQLiteRepository) queryCustomFields(ctx context.Context, q Queryer, dbID repo.ULID) ([]repo.CustomFieldDef, error) {
	query, args, err := r.Builder.Select("field_id", "name", "type", "is_indexed").
		From("database_custom_fields").
		Where(squirrel.Eq{"database_id": dbID.String()}).
		OrderBy("field_id").
		ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fields []repo.CustomFieldDef
	for rows.Next() {
		var cf repo.CustomFieldDef
		if err := rows.Scan(&cf.ID, &cf.Name, &cf.Type, &cf.IsIndexed); err != nil {
			return nil, err
		}
		fields = append(fields, cf)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// If empty list, initialize it to avoid returning nil
	if fields == nil {
		fields = []repo.CustomFieldDef{}
	}

	return fields, nil
}

// AddCustomField adds a new custom field to an existing database.
func (r *SQLiteRepository) AddCustomField(ctx context.Context, dbID repo.ULID, field repo.CustomFieldDef) (repo.CustomFieldDef, error) {
	if !shared.IsValidULID(dbID.String()) {
		return repo.CustomFieldDef{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	// Check if database exists
	var exists bool
	err := r.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM databases WHERE id = ?)", dbID.String()).Scan(&exists)
	if err != nil {
		return repo.CustomFieldDef{}, fmt.Errorf("failed to check database existence: %w", err)
	}
	if !exists {
		return repo.CustomFieldDef{}, customerrors.ErrNotFound
	}

	// Validate name
	if field.Name == "" {
		return repo.CustomFieldDef{}, fmt.Errorf("%w: field name cannot be empty", customerrors.ErrValidation)
	}

	// Validate type
	if !field.Type.IsValid() {
		return repo.CustomFieldDef{}, fmt.Errorf("%w: invalid custom field type", customerrors.ErrValidation)
	}
	datatype := field.Type.String()

	// Load existing fields
	existingFields, err := r.getCustomFields(ctx, dbID)
	if err != nil {
		return repo.CustomFieldDef{}, err
	}

	// Check name uniqueness
	for _, f := range existingFields {
		if strings.EqualFold(f.Name, field.Name) {
			return repo.CustomFieldDef{}, customerrors.ErrConflict
		}
	}

	// Find the next available ID between 0 and 254
	usedIDs := make(map[int]bool)
	for _, f := range existingFields {
		usedIDs[f.ID] = true
	}
	nextID := -1
	for i := 0; i <= 254; i++ {
		if !usedIDs[i] {
			nextID = i
			break
		}
	}
	if nextID == -1 {
		return repo.CustomFieldDef{}, fmt.Errorf("Cannot add field: The maximum limit of 255 custom fields has been reached.")
	}
	field.ID = nextID

	// Begin transaction
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.CustomFieldDef{}, err
	}
	defer tx.Rollback()

	// 1. Insert into database_custom_fields
	query, args, err := r.Builder.Insert("database_custom_fields").
		Columns("database_id", "field_id", "name", "type", "is_indexed").
		Values(dbID.String(), field.ID, field.Name, datatype, field.IsIndexed).
		ToSql()
	if err != nil {
		return repo.CustomFieldDef{}, err
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return repo.CustomFieldDef{}, customerrors.ErrConflict
		}
		return repo.CustomFieldDef{}, fmt.Errorf("failed to insert custom field: %w", err)
	}

	// 2. ALTER TABLE entries_ID ADD COLUMN cf_nextID Type
	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	if field.Type.IsCoordinate() {
		alterLatSQL := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN "%s%d_lat" REAL`, tableName, customFieldsPrefix, field.ID)
		if _, err := tx.ExecContext(ctx, alterLatSQL); err != nil {
			return repo.CustomFieldDef{}, fmt.Errorf("failed to add latitude column to entries table: %w", err)
		}
		alterLngSQL := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN "%s%d_lng" REAL`, tableName, customFieldsPrefix, field.ID)
		if _, err := tx.ExecContext(ctx, alterLngSQL); err != nil {
			return repo.CustomFieldDef{}, fmt.Errorf("failed to add longitude column to entries table: %w", err)
		}
	} else {
		alterSQL := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN "%s%d" %s`, tableName, customFieldsPrefix, field.ID, datatype)
		if _, err := tx.ExecContext(ctx, alterSQL); err != nil {
			return repo.CustomFieldDef{}, fmt.Errorf("failed to add column to entries table: %w", err)
		}
	}

	// 3. Create index if is_indexed is true
	if field.IsIndexed {
		indexSQL := BuildCustomFieldIndexSQL(dbID.String(), field)
		if _, err := tx.ExecContext(ctx, indexSQL); err != nil {
			return repo.CustomFieldDef{}, fmt.Errorf("failed to create index on custom field: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return repo.CustomFieldDef{}, err
	}

	// Invalidate cache
	r.Cache.Delete("cf:" + dbID.String())

	return field, nil
}

// UpdateCustomField updates an existing custom field.
func (r *SQLiteRepository) UpdateCustomField(ctx context.Context, dbID repo.ULID, fieldID int, name *string, isIndexed *bool) (repo.CustomFieldDef, error) {
	if !shared.IsValidULID(dbID.String()) {
		return repo.CustomFieldDef{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	// Check if database exists
	var exists bool
	err := r.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM databases WHERE id = ?)", dbID.String()).Scan(&exists)
	if err != nil {
		return repo.CustomFieldDef{}, fmt.Errorf("failed to check database existence: %w", err)
	}
	if !exists {
		return repo.CustomFieldDef{}, customerrors.ErrNotFound
	}

	// Load existing fields
	existingFields, err := r.getCustomFields(ctx, dbID)
	if err != nil {
		return repo.CustomFieldDef{}, err
	}

	// Find the field
	var targetField *repo.CustomFieldDef
	for i := range existingFields {
		if existingFields[i].ID == fieldID {
			targetField = &existingFields[i]
			break
		}
	}
	if targetField == nil {
		return repo.CustomFieldDef{}, customerrors.ErrNotFound
	}

	// Validate name update
	newName := targetField.Name
	if name != nil {
		newName = *name
		if newName == "" {
			return repo.CustomFieldDef{}, fmt.Errorf("%w: name cannot be empty", customerrors.ErrValidation)
		}
		// Check name uniqueness if changed
		if !strings.EqualFold(newName, targetField.Name) {
			for _, f := range existingFields {
				if strings.EqualFold(f.Name, newName) {
					return repo.CustomFieldDef{}, customerrors.ErrConflict
				}
			}
		}
	}

	newIsIndexed := targetField.IsIndexed
	if isIndexed != nil {
		newIsIndexed = *isIndexed
	}

	// If no changes, return early
	if newName == targetField.Name && newIsIndexed == targetField.IsIndexed {
		return *targetField, nil
	}

	// Begin transaction
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.CustomFieldDef{}, err
	}
	defer tx.Rollback()

	// Handle index changes
	if newIsIndexed != targetField.IsIndexed {
		if newIsIndexed {
			// Create index
			cfForIndex := *targetField
			cfForIndex.IsIndexed = true
			indexSQL := BuildCustomFieldIndexSQL(dbID.String(), cfForIndex)
			if _, err := tx.ExecContext(ctx, indexSQL); err != nil {
				return repo.CustomFieldDef{}, fmt.Errorf("failed to create index: %w", err)
			}
		} else {
			// Drop index
			dropIndexSQL := BuildCustomFieldDropIndexSQL(dbID.String(), fieldID)
			if _, err := tx.ExecContext(ctx, dropIndexSQL); err != nil {
				return repo.CustomFieldDef{}, fmt.Errorf("failed to drop index: %w", err)
			}
		}
	}

	// Update record
	query, args, err := r.Builder.Update("database_custom_fields").
		Set("name", newName).
		Set("is_indexed", newIsIndexed).
		Where(squirrel.Eq{"database_id": dbID.String(), "field_id": fieldID}).
		ToSql()
	if err != nil {
		return repo.CustomFieldDef{}, err
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return repo.CustomFieldDef{}, customerrors.ErrConflict
		}
		return repo.CustomFieldDef{}, fmt.Errorf("failed to update custom field record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return repo.CustomFieldDef{}, err
	}

	// Invalidate cache
	r.Cache.Delete("cf:" + dbID.String())

	updatedField := repo.CustomFieldDef{
		ID:        fieldID,
		Name:      newName,
		Type:      targetField.Type,
		IsIndexed: newIsIndexed,
	}
	return updatedField, nil
}

// DeleteCustomField deletes a custom field.
func (r *SQLiteRepository) DeleteCustomField(ctx context.Context, dbID repo.ULID, fieldID int) error {
	if !shared.IsValidULID(dbID.String()) {
		return fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	// Check if database exists
	var exists bool
	err := r.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM databases WHERE id = ?)", dbID.String()).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check database existence: %w", err)
	}
	if !exists {
		return customerrors.ErrNotFound
	}

	// Load existing fields to check if this one exists
	existingFields, err := r.getCustomFields(ctx, dbID)
	if err != nil {
		return err
	}

	var targetField *repo.CustomFieldDef
	for i := range existingFields {
		if existingFields[i].ID == fieldID {
			targetField = &existingFields[i]
			break
		}
	}
	if targetField == nil {
		return customerrors.ErrNotFound
	}

	// Begin transaction
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Drop the index
	dropIndexSQL := BuildCustomFieldDropIndexSQL(dbID.String(), fieldID)
	if _, err := tx.ExecContext(ctx, dropIndexSQL); err != nil {
		return fmt.Errorf("failed to drop index: %w", err)
	}

	// 2. Drop column(s) from entries table
	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	if targetField.Type.IsCoordinate() {
		dropLatSQL := fmt.Sprintf(`ALTER TABLE %s DROP COLUMN "%s%d_lat"`, tableName, customFieldsPrefix, fieldID)
		if _, err := tx.ExecContext(ctx, dropLatSQL); err != nil {
			return fmt.Errorf("failed to drop latitude column from entries table: %w", err)
		}
		dropLngSQL := fmt.Sprintf(`ALTER TABLE %s DROP COLUMN "%s%d_lng"`, tableName, customFieldsPrefix, fieldID)
		if _, err := tx.ExecContext(ctx, dropLngSQL); err != nil {
			return fmt.Errorf("failed to drop longitude column from entries table: %w", err)
		}
	} else {
		dropColSQL := fmt.Sprintf(`ALTER TABLE %s DROP COLUMN "%s%d"`, tableName, customFieldsPrefix, fieldID)
		if _, err := tx.ExecContext(ctx, dropColSQL); err != nil {
			return fmt.Errorf("failed to drop column from entries table: %w", err)
		}
	}

	// 3. Delete from database_custom_fields
	query, args, err := r.Builder.Delete("database_custom_fields").
		Where(squirrel.Eq{"database_id": dbID.String(), "field_id": fieldID}).
		ToSql()
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to delete custom field record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Invalidate cache
	r.Cache.Delete("cf:" + dbID.String())

	return nil
}
