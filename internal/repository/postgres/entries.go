package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/customerrors"
)

// CreateEntry inserts a new entry into the database's specific table and updates global stats.
func (r *PostgresRepository) CreateEntry(ctx context.Context, db repo.Database, entry repo.Entry) (repo.Entry, error) {
	if !shared.IsValidULID(db.ID.String()) {
		return repo.Entry{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	isValidMime, err := media.IsMimeOfType(db.ContentType, entry.MimeType)
	if !isValidMime {
		return repo.Entry{}, customerrors.ErrBadMimeType
	}
	if err != nil {
		return repo.Entry{}, err
	}

	dbNowExpr := squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")

	insertData := map[string]any{
		"created_at":       dbNowExpr,
		"updated_at":       dbNowExpr,
		"filesize":         entry.Size,
		"preview_filesize": entry.PreviewSize,
		"filename":         entry.FileName,
		"status":           entry.Status,
		"mime_type":        entry.MimeType,
	}

	if !entry.Timestamp.IsZero() {
		insertData["timestamp"] = entry.Timestamp.UnixMilli()
	} else {
		insertData["timestamp"] = dbNowExpr
	}

	if entry.ID > 0 {
		insertData["id"] = entry.ID
	}

	for key, value := range entry.MediaFields {
		insertData[key] = value
	}
	if err := mapCustomFieldsToPostgresColumns(db.CustomFields, entry.CustomFields, insertData); err != nil {
		return repo.Entry{}, err
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	tableName := fmt.Sprintf(`"entries_%s"`, db.ID)
	insertQuery, args, err := r.Builder.Insert(tableName).
		SetMap(insertData).
		Suffix("RETURNING id, timestamp, created_at, updated_at").
		ToSql()
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to build insert query: %w", err)
	}

	var tsMillis, createdMillis, updatedMillis int64
	if err := tx.QueryRowContext(ctx, insertQuery, args...).Scan(&entry.ID, &tsMillis, &createdMillis, &updatedMillis); err != nil {
		if isPQUniqueViolation(err) {
			return repo.Entry{}, customerrors.ErrConflict
		}
		return repo.Entry{}, fmt.Errorf("failed to insert entry and scan returning fields: %w", err)
	}

	entry.Timestamp = time.UnixMilli(tsMillis)
	entry.CreatedAt = time.UnixMilli(createdMillis)
	entry.UpdatedAt = time.UnixMilli(updatedMillis)

	if entry.ID > 0 {
		// Synchronize the sequence so subsequent auto-generated IDs don't collide
		seqQuery := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', 'id'), (SELECT COALESCE(MAX(id), 1) FROM %s))`, tableName, tableName)
		if _, err := tx.ExecContext(ctx, seqQuery); err != nil {
			return repo.Entry{}, fmt.Errorf("failed to synchronize sequence: %w", err)
		}
	}

	totalSizeDelta := entry.Size + entry.PreviewSize

	statsQuery, statsArgs, err := r.Builder.Update("databases").
		Set("entry_count", squirrel.Expr("entry_count + 1")).
		Set("total_disk_space_bytes", squirrel.Expr("total_disk_space_bytes + ?", totalSizeDelta)).
		Where(squirrel.Eq{"id": db.ID.String()}).
		ToSql()
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to build stats update query: %w", err)
	}

	if _, err := tx.ExecContext(ctx, statsQuery, statsArgs...); err != nil {
		return repo.Entry{}, fmt.Errorf("failed to update database stats: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return repo.Entry{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return entry, nil
}

// GetEntry retrieves a single entry by its ID using a dynamic row scanner.
func (r *PostgresRepository) GetEntry(ctx context.Context, dbID repo.ULID, id int64) (repo.Entry, error) {
	if !shared.IsValidULID(dbID.String()) {
		return repo.Entry{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	customFields, err := r.getCustomFields(ctx, r.DB, dbID)
	if err != nil {
		return repo.Entry{}, err
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	query, args, err := r.Builder.Select("*").From(tableName).Where(squirrel.Eq{"id": id}).ToSql()
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to build query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to query entry: %w", err)
	}
	defer rows.Close()

	entry, err := r.scanEntryRow(rows, customFields)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to scan entry: %w", err)
	}

	return entry, nil
}

// GetEntries retrieves a paginated list of entries, optionally filtered by a time range.
func (r *PostgresRepository) GetEntries(ctx context.Context, dbID repo.ULID, opts repo.QueryOptions) ([]repo.Entry, error) {
	if !shared.IsValidULID(dbID.String()) {
		return nil, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	if err := opts.Validate(); err != nil {
		return nil, err
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	builder := r.Builder.Select("*").From(tableName)

	if !opts.TStart.IsZero() {
		builder = builder.Where(squirrel.GtOrEq{opts.TimeField: opts.TStart.UnixMilli()})
	}
	if !opts.TEnd.IsZero() {
		builder = builder.Where(squirrel.LtOrEq{opts.TimeField: opts.TEnd.UnixMilli()})
	}

	builder = builder.OrderBy(fmt.Sprintf("%s %s", opts.SortBy, strings.ToUpper(opts.Order)))

	if opts.Limit > 0 {
		builder = builder.Limit(uint64(opts.Limit))
	}
	if opts.Offset > 0 {
		builder = builder.Offset(uint64(opts.Offset))
	}

	customFields, err := r.getCustomFields(ctx, r.DB, dbID)
	if err != nil {
		return nil, err
	}

	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}
	defer rows.Close()

	entries, err := r.scanEntryRows(rows, customFields)
	if err != nil {
		return nil, fmt.Errorf("failed to scan entry: %w", err)
	}

	return entries, nil
}

// UpdateEntry modifies an existing entry's metadata and safely adjusts the parent database's size statistics.
func (r *PostgresRepository) UpdateEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) (repo.Entry, error) {
	if !shared.IsValidULID(dbID.String()) {
		return repo.Entry{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())

	var entryTime time.Time
	if !entry.Timestamp.IsZero() {
		entryTime = entry.Timestamp
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	customFields, err := r.getCustomFields(ctx, tx, dbID)
	if err != nil {
		return repo.Entry{}, err
	}

	var oldSize, oldPreviewSize uint64
	queryOld, argsOld, err := r.Builder.Select("filesize", "preview_filesize").
		From(tableName).
		Where(squirrel.Eq{"id": entry.ID}).
		ToSql()
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to build select old sizes query: %w", err)
	}

	err = tx.QueryRowContext(ctx, queryOld, argsOld...).Scan(&oldSize, &oldPreviewSize)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.Entry{}, customerrors.ErrNotFound
		}
		return repo.Entry{}, fmt.Errorf("failed to query old sizes: %w", err)
	}

	updateData := map[string]any{
		"timestamp":        entryTime.UnixMilli(),
		"updated_at":       squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT"),
		"filesize":         entry.Size,
		"preview_filesize": entry.PreviewSize,
		"filename":         entry.FileName,
		"status":           entry.Status,
		"mime_type":        entry.MimeType,
	}

	for key, value := range entry.MediaFields {
		updateData[key] = value
	}
	if err := mapCustomFieldsToPostgresColumns(customFields, entry.CustomFields, updateData); err != nil {
		return repo.Entry{}, err
	}

	updateQuery, argsUpdate, err := r.Builder.Update(tableName).
		SetMap(updateData).
		Where(squirrel.Eq{"id": entry.ID}).
		Suffix("RETURNING updated_at").
		ToSql()
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to build update query: %w", err)
	}

	var updatedMillis int64
	if err = tx.QueryRowContext(ctx, updateQuery, argsUpdate...).Scan(&updatedMillis); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.Entry{}, customerrors.ErrNotFound
		}
		return repo.Entry{}, fmt.Errorf("failed to update entry: %w", err)
	}

	delta := (int64(entry.Size) + int64(entry.PreviewSize)) - (int64(oldSize) + int64(oldPreviewSize))

	if delta != 0 {
		statsQuery, statsArgs, err := r.Builder.Update("databases").
			Set("total_disk_space_bytes", squirrel.Expr("GREATEST(0, total_disk_space_bytes + ?)", delta)).
			Where(squirrel.Eq{"id": dbID.String()}).
			ToSql()
		if err != nil {
			return repo.Entry{}, fmt.Errorf("failed to build stats update query: %w", err)
		}

		if _, err := tx.ExecContext(ctx, statsQuery, statsArgs...); err != nil {
			return repo.Entry{}, fmt.Errorf("failed to update database stats: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return repo.Entry{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	entry.UpdatedAt = time.UnixMilli(updatedMillis)
	return entry, nil
}

// UpdateEntriesStatus modifies the status of multiple entries at once using ANY($1).
func (r *PostgresRepository) UpdateEntriesStatus(ctx context.Context, dbID repo.ULID, entryIDs []int64, status repo.EntryStatus) error {
	if !shared.IsValidULID(dbID.String()) {
		return fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	if len(entryIDs) == 0 {
		return nil
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())

	query, args, err := r.Builder.Update(tableName).
		Set("status", status).
		Set("updated_at", squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")).
		Where(squirrel.Eq{"id": entryIDs}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build update status query: %w", err)
	}

	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update entries status: %w", err)
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return customerrors.ErrNotFound
	}

	return nil
}

// DeleteEntry removes a single entry and atomically decrements parent database stats.
func (r *PostgresRepository) DeleteEntry(ctx context.Context, dbID repo.ULID, id int64) (repo.DeletedEntryMeta, error) {
	if !shared.IsValidULID(dbID.String()) {
		return repo.DeletedEntryMeta{}, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.DeletedEntryMeta{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	deleteQuery, deleteArgs, err := r.Builder.Delete(tableName).
		Where(squirrel.Eq{"id": id}).
		Suffix("RETURNING id, filesize, preview_filesize").
		ToSql()
	if err != nil {
		return repo.DeletedEntryMeta{}, fmt.Errorf("failed to build delete query: %w", err)
	}

	var meta repo.DeletedEntryMeta
	err = tx.QueryRowContext(ctx, deleteQuery, deleteArgs...).Scan(&meta.ID, &meta.Filesize, &meta.PreviewSize)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.DeletedEntryMeta{}, customerrors.ErrNotFound
		}
		return repo.DeletedEntryMeta{}, fmt.Errorf("failed to execute delete: %w", err)
	}

	totalDeletedSize := meta.Filesize + meta.PreviewSize
	statsQuery, statsArgs, err := r.Builder.Update("databases").
		Set("entry_count", squirrel.Expr("GREATEST(0, entry_count - 1)")).
		Set("total_disk_space_bytes", squirrel.Expr("GREATEST(0, total_disk_space_bytes - ?)", totalDeletedSize)).
		Where(squirrel.Eq{"id": dbID.String()}).
		ToSql()
	if err != nil {
		return repo.DeletedEntryMeta{}, fmt.Errorf("failed to build stats update query: %w", err)
	}

	if _, err := tx.ExecContext(ctx, statsQuery, statsArgs...); err != nil {
		return repo.DeletedEntryMeta{}, fmt.Errorf("failed to update database stats: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return repo.DeletedEntryMeta{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return meta, nil
}

// DeleteEntries removes multiple entries in a single transaction and updates stats once.
func (r *PostgresRepository) DeleteEntries(ctx context.Context, dbID repo.ULID, entryIDs []int64) ([]repo.DeletedEntryMeta, error) {
	if !shared.IsValidULID(dbID.String()) {
		return nil, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	if len(entryIDs) == 0 {
		return nil, customerrors.ErrNotFound
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	deleteQuery, deleteArgs, err := r.Builder.Delete(tableName).
		Where(squirrel.Eq{"id": entryIDs}).
		Suffix("RETURNING id, filesize, preview_filesize").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build bulk delete query: %w", err)
	}

	rows, err := tx.QueryContext(ctx, deleteQuery, deleteArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute bulk delete: %w", err)
	}
	defer rows.Close()

	var deletedMetas []repo.DeletedEntryMeta
	var totalDeletedSize uint64
	var deletedCount int

	for rows.Next() {
		var meta repo.DeletedEntryMeta
		if err := rows.Scan(&meta.ID, &meta.Filesize, &meta.PreviewSize); err != nil {
			return nil, fmt.Errorf("failed to scan deleted entry meta: %w", err)
		}
		deletedMetas = append(deletedMetas, meta)
		totalDeletedSize += meta.Filesize + meta.PreviewSize
		deletedCount++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	rows.Close()

	if deletedCount == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit empty transaction: %w", err)
		}
		return deletedMetas, nil
	}

	statsQuery, statsArgs, err := r.Builder.Update("databases").
		Set("entry_count", squirrel.Expr("GREATEST(0, entry_count - ?)", deletedCount)).
		Set("total_disk_space_bytes", squirrel.Expr("GREATEST(0, total_disk_space_bytes - ?)", totalDeletedSize)).
		Where(squirrel.Eq{"id": dbID.String()}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build stats update query: %w", err)
	}

	if _, err := tx.ExecContext(ctx, statsQuery, statsArgs...); err != nil {
		return nil, fmt.Errorf("failed to update database stats: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return deletedMetas, nil
}

// SearchEntries retrieves entries matching complex nested filter criteria.
func (r *PostgresRepository) SearchEntries(ctx context.Context, dbID repo.ULID, req repo.SearchRequest, customFields []repo.CustomFieldDef) ([]repo.Entry, error) {
	if !shared.IsValidULID(dbID.String()) {
		return nil, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	builder := r.Builder.Select("*").From(tableName)

	if req.Filter != nil && len(req.Filter.Conditions) > 0 {
		var andExpr squirrel.And
		var orExpr squirrel.Or
		isOr := strings.ToLower(req.Filter.Operator) == "or"

		for _, cond := range req.Filter.Conditions {
			var cf *repo.CustomFieldDef
			for i := range customFields {
				if customFields[i].Name == cond.Field {
					cf = &customFields[i]
					break
				}
			}

			if cf != nil && cf.Type.IsCoordinate() {
				if strings.ToLower(cond.Operator) != "in_box" {
					return nil, fmt.Errorf("%w: operator '%s' is not supported on coordinate fields; only 'in_box' is allowed", customerrors.ErrValidation, cond.Operator)
				}
				box, err := repo.ParseBoundingBox(cond.Value)
				if err != nil {
					return nil, fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
				}
				var expr squirrel.Sqlizer
				if box.MinLng <= box.MaxLng {
					expr = squirrel.Expr(
						fmt.Sprintf(`"%s%d" <@ box(point(?, ?), point(?, ?))`, customFieldsPrefix, cf.ID),
						box.MinLng, box.MinLat, box.MaxLng, box.MaxLat,
					)
				} else {
					expr = squirrel.Expr(
						fmt.Sprintf(`("%s%d" <@ box(point(?, ?), point(?, ?)) OR "%s%d" <@ box(point(?, ?), point(?, ?)))`, customFieldsPrefix, cf.ID, customFieldsPrefix, cf.ID),
						box.MinLng, box.MinLat, 180.0, box.MaxLat, -180.0, box.MinLat, box.MaxLng, box.MaxLat,
					)
				}
				if isOr {
					orExpr = append(orExpr, expr)
				} else {
					andExpr = append(andExpr, expr)
				}
			} else {
				if strings.ToLower(cond.Operator) == "in_box" {
					return nil, fmt.Errorf("%w: 'in_box' operator is only supported on coordinate fields", customerrors.ErrValidation)
				}
				safeField, err := r.validateAndFormatSearchField(cond.Field, customFields)
				if err != nil {
					return nil, fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
				}

				if !isValidOperator(cond.Operator) {
					return nil, fmt.Errorf("%w: invalid operator '%s'", customerrors.ErrValidation, cond.Operator)
				}

				expr := squirrel.Expr(fmt.Sprintf("%s %s ?", safeField, cond.Operator), cond.Value)
				if isOr {
					orExpr = append(orExpr, expr)
				} else {
					andExpr = append(andExpr, expr)
				}
			}
		}

		if isOr {
			builder = builder.Where(orExpr)
		} else {
			builder = builder.Where(andExpr)
		}
	}

	if req.Sort != nil && req.Sort.Field != "" {
		for _, cf := range customFields {
			if cf.Name == req.Sort.Field && cf.Type.IsCoordinate() {
				return nil, fmt.Errorf("%w: sorting by coordinate fields is not supported", customerrors.ErrValidation)
			}
		}

		safeField, err := r.validateAndFormatSearchField(req.Sort.Field, customFields)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
		}

		dir := "DESC"
		if strings.ToLower(req.Sort.Direction) == "asc" {
			dir = "ASC"
		}
		builder = builder.OrderBy(fmt.Sprintf("%s %s", safeField, dir))
	} else {
		builder = builder.OrderBy("timestamp DESC")
	}

	if req.Pagination.Limit > 0 {
		builder = builder.Limit(uint64(req.Pagination.Limit))
	}
	if req.Pagination.Offset > 0 {
		builder = builder.Offset(uint64(req.Pagination.Offset))
	}

	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build search query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search query: %w", err)
	}
	defer rows.Close()

	entries, err := r.scanEntryRows(rows, customFields)
	if err != nil {
		return nil, fmt.Errorf("failed to scan search results: %w", err)
	}

	return entries, nil
}

// ClaimQueuedEntry atomically claims a queued entry by changing its status to processing.
// Optimistic Concurrency Control (OCC): PostgreSQL executes an atomic row update with WHERE status = repo.EntryStatusQueued.
// In a multi-node / multi-pod deployment, if multiple background worker routines attempt to claim the same queued file simultaneously,
// only the worker receiving RowsAffected == 1 claims the task; other workers silently back off.
func (r *PostgresRepository) ClaimQueuedEntry(ctx context.Context, dbID repo.ULID, entryID int64) (bool, error) {
	if !shared.IsValidULID(dbID.String()) {
		return false, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	query, args, err := r.Builder.Update(tableName).
		Set("status", repo.EntryStatusProcessing).
		Set("updated_at", squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")).
		Where(squirrel.Eq{"id": entryID, "status": repo.EntryStatusQueued}).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("failed to build claim query: %w", err)
	}

	res, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("failed to execute claim update: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to retrieve rows affected: %w", err)
	}
	return rows == 1, nil
}

// GetEntriesByStatus retrieves entries matching a status, ordered by ID ascending (oldest first).
func (r *PostgresRepository) GetEntriesByStatus(ctx context.Context, dbID repo.ULID, status repo.EntryStatus, limit uint64) ([]repo.Entry, error) {
	if !shared.IsValidULID(dbID.String()) {
		return nil, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	customFields, err := r.getCustomFields(ctx, r.DB, dbID)
	if err != nil {
		return nil, err
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	b := r.Builder.Select("*").From(tableName).Where(squirrel.Eq{"status": status}).OrderBy("id ASC")
	if limit > 0 {
		b = b.Limit(limit)
	}
	query, args, err := b.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get-by-status query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries by status: %w", err)
	}
	defer rows.Close()

	entries, err := r.scanEntryRows(rows, customFields)
	if err != nil {
		return nil, fmt.Errorf("failed to scan entries by status: %w", err)
	}

	return entries, nil
}

// CountEntriesByStatus counts the number of entries with the specified status.
func (r *PostgresRepository) CountEntriesByStatus(ctx context.Context, dbID repo.ULID, status repo.EntryStatus) (int64, error) {
	if !shared.IsValidULID(dbID.String()) {
		return 0, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	query, args, err := r.Builder.Select("COUNT(*)").From(tableName).Where(squirrel.Eq{"status": status}).ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build count-by-status query: %w", err)
	}

	var count int64
	err = r.DB.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to execute count query: %w", err)
	}

	return count, nil
}
