package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"

	"github.com/Masterminds/squirrel"
)

// LogAudit inserts a new audit log into the database.
func (r *PostgresRepository) LogAudit(ctx context.Context, log repository.AuditLog) error {
	detailsJSON, err := json.Marshal(log.Details)
	if err != nil {
		detailsJSON = []byte("{}")
	}

	var tsVal any
	if !log.Timestamp.IsZero() {
		tsVal = log.Timestamp.UnixMilli()
	} else {
		tsVal = squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")
	}

	query, args, err := r.Builder.Insert("audit_logs").
		Columns("timestamp", "action", "actor", "resource", "details").
		Values(tsVal, log.Action, log.Actor, log.Resource, string(detailsJSON)).
		ToSql()

	if err != nil {
		return err
	}

	if _, err := r.DB.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}
	return nil
}

// GetLogs retrieves a paginated list of audit logs, optionally filtered by a time range.
func (r *PostgresRepository) GetLogs(ctx context.Context, opts repository.QueryOptions) ([]repository.AuditLog, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	builder := r.Builder.Select("id", "timestamp", "action", "actor", "resource", "details").
		From("audit_logs")

	if !opts.TStart.IsZero() {
		builder = builder.Where(squirrel.GtOrEq{"timestamp": opts.TStart.UnixMilli()})
	}
	if !opts.TEnd.IsZero() {
		builder = builder.Where(squirrel.LtOrEq{"timestamp": opts.TEnd.UnixMilli()})
	}
	if opts.IDStart > 0 {
		builder = builder.Where(squirrel.GtOrEq{"id": opts.IDStart})
	}
	if opts.IDEnd > 0 {
		builder = builder.Where(squirrel.LtOrEq{"id": opts.IDEnd})
	}

	if strings.ToLower(opts.Order) == "asc" {
		builder = builder.OrderBy("timestamp ASC")
	} else {
		builder = builder.OrderBy("timestamp DESC")
	}

	if opts.Limit > 0 {
		builder = builder.Limit(uint64(opts.Limit))
	}
	if opts.Offset > 0 {
		builder = builder.Offset(uint64(opts.Offset))
	}

	query, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get logs query: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit logs: %w", err)
	}
	defer rows.Close()

	var logs []repository.AuditLog
	var logTimestamp int64
	for rows.Next() {
		var log repository.AuditLog
		var detailsStr string

		if err := rows.Scan(&log.ID, &logTimestamp, &log.Action, &log.Actor, &log.Resource, &detailsStr); err != nil {
			return nil, fmt.Errorf("failed to scan audit log row: %w", err)
		}

		if logTimestamp != shared.MissingTimestamp {
			log.Timestamp = time.UnixMilli(logTimestamp)
		}

		if detailsStr != "" {
			if err := json.Unmarshal([]byte(detailsStr), &log.Details); err != nil {
				log.Details = make(map[string]any)
			}
		} else {
			log.Details = make(map[string]any)
		}

		logs = append(logs, log)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return logs, nil
}

// DeleteLogs removes all audit logs older than the provided age.
func (r *PostgresRepository) DeleteLogs(ctx context.Context, maxAge time.Duration) error {
	if maxAge <= 0 {
		return nil
	}

	query, args, err := r.Builder.Delete("audit_logs").
		Where(squirrel.Expr("timestamp < (EXTRACT(EPOCH FROM clock_timestamp()) * 1000 - ?)", maxAge.Milliseconds())).
		ToSql()

	if err != nil {
		return fmt.Errorf("failed to build delete logs query: %w", err)
	}

	_, err = r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete older audit logs: %w", err)
	}

	return nil
}
