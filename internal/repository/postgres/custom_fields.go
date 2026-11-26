package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"

	"github.com/Masterminds/squirrel"
)

// GetCustomFields retrieves all custom fields for a specific database.
func (r *PostgresRepository) GetCustomFields(ctx context.Context, dbID repo.ULID) ([]repo.CustomFieldDef, error) {
	var exists bool
	err := r.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM databases WHERE id = $1)", dbID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check database existence: %w", err)
	}
	if !exists {
		return nil, customerrors.ErrNotFound
	}
	return r.getCustomFields(ctx, r.DB, dbID)
}

func (r *PostgresRepository) getCustomFields(ctx context.Context, q Queryer, dbID repo.ULID) ([]repo.CustomFieldDef, error) {
	query, args, err := r.Builder.Select("field_id", "name", "type", "is_indexed").
		From("database_custom_fields").
		Where(squirrel.Eq{"database_id": dbID}).
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

	if fields == nil {
		fields = []repo.CustomFieldDef{}
	}

	return fields, nil
}

func (r *PostgresRepository) lockEntriesTable(ctx context.Context, tx *sql.Tx, dbID repo.ULID, mode string) error {
	query := fmt.Sprintf(`LOCK TABLE "entries_%s" IN %s MODE`, dbID.String(), mode)
	if _, err := tx.ExecContext(ctx, query); err != nil {
		if isPQUndefinedTable(err) {
			return customerrors.ErrNotFound
		}
		return fmt.Errorf("failed to lock entries table: %w", err)
	}
	return nil
}

func (r *PostgresRepository) lockDatabaseForUpdate(ctx context.Context, tx *sql.Tx, dbID repo.ULID) error {
	var dummy int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM databases WHERE id = $1 FOR UPDATE", dbID).Scan(&dummy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return customerrors.ErrNotFound
		}
		return fmt.Errorf("failed to lock database: %w", err)
	}
	return nil
}

// AddCustomField adds a new custom field to an existing database.
func (r *PostgresRepository) AddCustomField(ctx context.Context, dbID repo.ULID, field repo.CustomFieldDef) (repo.CustomFieldDef, error) {
	if field.Name == "" {
		return repo.CustomFieldDef{}, fmt.Errorf("%w: field name cannot be empty", customerrors.ErrValidation)
	}

	if !field.Type.IsValid() {
		return repo.CustomFieldDef{}, fmt.Errorf("%w: invalid custom field type", customerrors.ErrValidation)
	}
	datatype := field.Type.String()
	pgDatatype := mapToPostgresType(datatype)
	if pgDatatype == "" {
		return repo.CustomFieldDef{}, fmt.Errorf("%w: unsupported custom field type '%s'", customerrors.ErrValidation, field.Type)
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.CustomFieldDef{}, err
	}
	defer tx.Rollback()

	if err := r.lockEntriesTable(ctx, tx, dbID, "ACCESS EXCLUSIVE"); err != nil {
		return repo.CustomFieldDef{}, err
	}

	if err := r.lockDatabaseForUpdate(ctx, tx, dbID); err != nil {
		return repo.CustomFieldDef{}, err
	}

	existingFields, err := r.getCustomFields(ctx, tx, dbID)
	if err != nil {
		return repo.CustomFieldDef{}, err
	}

	for _, f := range existingFields {
		if strings.EqualFold(f.Name, field.Name) {
			return repo.CustomFieldDef{}, customerrors.ErrConflict
		}
	}

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

	query, args, err := r.Builder.Insert("database_custom_fields").
		Columns("database_id", "field_id", "name", "type", "is_indexed").
		Values(dbID, field.ID, field.Name, datatype, field.IsIndexed).
		ToSql()
	if err != nil {
		return repo.CustomFieldDef{}, err
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		if isPQUniqueViolation(err) {
			return repo.CustomFieldDef{}, customerrors.ErrConflict
		}
		return repo.CustomFieldDef{}, fmt.Errorf("failed to insert custom field: %w", err)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	alterSQL := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN "%s%d" %s`, tableName, customFieldsPrefix, field.ID, pgDatatype)
	if _, err := tx.ExecContext(ctx, alterSQL); err != nil {
		return repo.CustomFieldDef{}, fmt.Errorf("failed to add column to entries table: %w", err)
	}

	if field.IsIndexed {
		indexSQL := BuildCustomFieldIndexSQL(dbID, field)
		if _, err := tx.ExecContext(ctx, indexSQL); err != nil {
			return repo.CustomFieldDef{}, fmt.Errorf("failed to create index on custom field: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return repo.CustomFieldDef{}, err
	}

	return field, nil
}

// UpdateCustomField updates an existing custom field.
func (r *PostgresRepository) UpdateCustomField(ctx context.Context, dbID repo.ULID, fieldID int, name *string, isIndexed *bool) (repo.CustomFieldDef, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.CustomFieldDef{}, err
	}
	defer tx.Rollback()

	if isIndexed != nil {
		mode := "ACCESS EXCLUSIVE"
		if *isIndexed {
			mode = "SHARE"
		}
		if err := r.lockEntriesTable(ctx, tx, dbID, mode); err != nil {
			return repo.CustomFieldDef{}, err
		}
	}

	if err := r.lockDatabaseForUpdate(ctx, tx, dbID); err != nil {
		return repo.CustomFieldDef{}, err
	}

	existingFields, err := r.getCustomFields(ctx, tx, dbID)
	if err != nil {
		return repo.CustomFieldDef{}, err
	}

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

	newName := targetField.Name
	if name != nil {
		newName = *name
		if newName == "" {
			return repo.CustomFieldDef{}, fmt.Errorf("%w: name cannot be empty", customerrors.ErrValidation)
		}
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

	if newName == targetField.Name && newIsIndexed == targetField.IsIndexed {
		return *targetField, nil
	}

	if newIsIndexed != targetField.IsIndexed {
		if newIsIndexed {
			cfForIndex := *targetField
			cfForIndex.IsIndexed = true
			indexSQL := BuildCustomFieldIndexSQL(dbID, cfForIndex)
			if _, err := tx.ExecContext(ctx, indexSQL); err != nil {
				return repo.CustomFieldDef{}, fmt.Errorf("failed to create index: %w", err)
			}
		} else {
			dropIndexSQL := BuildCustomFieldDropIndexSQL(dbID, fieldID)
			if _, err := tx.ExecContext(ctx, dropIndexSQL); err != nil {
				return repo.CustomFieldDef{}, fmt.Errorf("failed to drop index: %w", err)
			}
		}
	}

	query, args, err := r.Builder.Update("database_custom_fields").
		Set("name", newName).
		Set("is_indexed", newIsIndexed).
		Where(squirrel.Eq{"database_id": dbID, "field_id": fieldID}).
		ToSql()
	if err != nil {
		return repo.CustomFieldDef{}, err
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		if isPQUniqueViolation(err) {
			return repo.CustomFieldDef{}, customerrors.ErrConflict
		}
		return repo.CustomFieldDef{}, fmt.Errorf("failed to update custom field record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return repo.CustomFieldDef{}, err
	}

	updatedField := repo.CustomFieldDef{
		ID:        fieldID,
		Name:      newName,
		Type:      targetField.Type,
		IsIndexed: newIsIndexed,
	}
	return updatedField, nil
}

// DeleteCustomField deletes a custom field.
func (r *PostgresRepository) DeleteCustomField(ctx context.Context, dbID repo.ULID, fieldID int) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := r.lockEntriesTable(ctx, tx, dbID, "ACCESS EXCLUSIVE"); err != nil {
		return err
	}

	if err := r.lockDatabaseForUpdate(ctx, tx, dbID); err != nil {
		return err
	}

	existingFields, err := r.getCustomFields(ctx, tx, dbID)
	if err != nil {
		return err
	}

	var found bool
	for _, f := range existingFields {
		if f.ID == fieldID {
			found = true
			break
		}
	}
	if !found {
		return customerrors.ErrNotFound
	}

	dropIndexSQL := BuildCustomFieldDropIndexSQL(dbID, fieldID)
	if _, err := tx.ExecContext(ctx, dropIndexSQL); err != nil {
		return fmt.Errorf("failed to drop index: %w", err)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	dropColSQL := fmt.Sprintf(`ALTER TABLE %s DROP COLUMN "%s%d"`, tableName, customFieldsPrefix, fieldID)
	if _, err := tx.ExecContext(ctx, dropColSQL); err != nil {
		return fmt.Errorf("failed to drop column from entries table: %w", err)
	}

	query, args, err := r.Builder.Delete("database_custom_fields").
		Where(squirrel.Eq{"database_id": dbID, "field_id": fieldID}).
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

	return nil
}
