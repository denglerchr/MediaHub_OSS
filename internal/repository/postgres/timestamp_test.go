package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/shared/ulid"

	"github.com/Masterminds/squirrel"
)

type tsStubResultRow struct {
	cols []string
	rows [][]driver.Value
}

type tsStubRecordedQuery struct {
	query string
	args  []driver.Value
}

type tsStubConnector struct {
	handler func(query string, args []driver.NamedValue) (tsStubResultRow, error)
}

func (c *tsStubConnector) Connect(_ context.Context) (driver.Conn, error) {
	return &tsStubConn{handler: c.handler}, nil
}

func (c *tsStubConnector) Driver() driver.Driver {
	return nil
}

type tsStubConn struct {
	handler func(query string, args []driver.NamedValue) (tsStubResultRow, error)
}

func (c *tsStubConn) Prepare(_ string) (driver.Stmt, error) {
	return nil, errors.New("Prepare not implemented in tsStubConn")
}

func (c *tsStubConn) Close() error {
	return nil
}

func (c *tsStubConn) Begin() (driver.Tx, error) {
	return &tsStubTx{}, nil
}

func (c *tsStubConn) BeginTx(_ context.Context, _ driver.TxOptions) (driver.Tx, error) {
	return &tsStubTx{}, nil
}

func (c *tsStubConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	res, err := c.handler(query, args)
	if err != nil {
		return nil, err
	}
	return &tsStubRows{cols: res.cols, rows: res.rows}, nil
}

func (c *tsStubConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if _, err := c.handler(query, args); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

type tsStubTx struct{}

func (tx *tsStubTx) Commit() error   { return nil }
func (tx *tsStubTx) Rollback() error { return nil }

type tsStubRows struct {
	cols []string
	rows [][]driver.Value
	idx  int
}

func (r *tsStubRows) Columns() []string {
	return r.cols
}

func (r *tsStubRows) Close() error {
	return nil
}

func (r *tsStubRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

func newTestPostgresRepo(handler func(query string, args []driver.NamedValue) (tsStubResultRow, error)) (*PostgresRepository, func()) {
	db := sql.OpenDB(&tsStubConnector{handler: handler})
	r := &PostgresRepository{
		DB:              db,
		Builder:         squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
		AllowedStatuses: repo.GetAllEntryStatuses(),
	}
	return r, func() { _ = db.Close() }
}

func TestPostgres_GetEntry_Pre1970AndEpochTimestamps(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")

	t1950Noon := time.Date(1950, 1, 1, 12, 0, 0, 0, time.UTC)
	t1950Midnight := time.UnixMilli(-631152000000).UTC() // 1950-01-01T00:00:00Z
	tEpoch := time.Unix(0, 0).UTC()
	tModern := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		id        int64
		ts        time.Time
		createdAt time.Time
		updatedAt time.Time
	}{
		{
			name:      "pre-1970 noon",
			id:        1,
			ts:        t1950Noon,
			createdAt: t1950Noon,
			updatedAt: t1950Noon,
		},
		{
			name:      "pre-1970 midnight (-631152000000)",
			id:        2,
			ts:        t1950Midnight,
			createdAt: t1950Midnight,
			updatedAt: t1950Midnight,
		},
		{
			name:      "exact unix epoch (0)",
			id:        3,
			ts:        tEpoch,
			createdAt: tEpoch,
			updatedAt: tEpoch,
		},
		{
			name:      "modern timestamp",
			id:        4,
			ts:        tModern,
			createdAt: tModern,
			updatedAt: tModern,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, cleanup := newTestPostgresRepo(func(query string, args []driver.NamedValue) (tsStubResultRow, error) {
				if strings.Contains(query, "database_custom_fields") {
					return tsStubResultRow{
						cols: []string{"field_id", "name", "type", "is_indexed"},
						rows: nil,
					}, nil
				}
				return tsStubResultRow{
					cols: []string{"id", "timestamp", "created_at", "updated_at", "filesize", "preview_filesize", "filename", "status", "mime_type"},
					rows: [][]driver.Value{{
						tc.id,
						tc.ts.UnixMilli(),
						tc.createdAt.UnixMilli(),
						tc.updatedAt.UnixMilli(),
						int64(1024),
						int64(0),
						"test.jpg",
						int64(repo.EntryStatusReady),
						"image/jpeg",
					}},
				}, nil
			})
			defer cleanup()

			entry, err := r.GetEntry(ctx, dbID, tc.id)
			if err != nil {
				t.Fatalf("GetEntry failed: %v", err)
			}
			if entry.Timestamp.IsZero() || entry.Timestamp.Year() == 1 {
				t.Fatalf("expected non-Year-1 Timestamp, got %v", entry.Timestamp)
			}
			if !entry.Timestamp.Equal(tc.ts) {
				t.Errorf("expected Timestamp %v, got %v", tc.ts, entry.Timestamp)
			}
			if entry.CreatedAt.IsZero() || entry.CreatedAt.Year() == 1 || !entry.CreatedAt.Equal(tc.createdAt) {
				t.Errorf("expected CreatedAt %v, got %v", tc.createdAt, entry.CreatedAt)
			}
			if entry.UpdatedAt.IsZero() || entry.UpdatedAt.Year() == 1 || !entry.UpdatedAt.Equal(tc.updatedAt) {
				t.Errorf("expected UpdatedAt %v, got %v", tc.updatedAt, entry.UpdatedAt)
			}
		})
	}

	t.Run("missing timestamp sentinel scans as zero time.Time", func(t *testing.T) {
		r, cleanup := newTestPostgresRepo(func(query string, _ []driver.NamedValue) (tsStubResultRow, error) {
			if strings.Contains(query, "database_custom_fields") {
				return tsStubResultRow{
					cols: []string{"field_id", "name", "type", "is_indexed"},
					rows: nil,
				}, nil
			}
			return tsStubResultRow{
				cols: []string{"id", "timestamp", "created_at", "updated_at", "filesize", "preview_filesize", "filename", "status", "mime_type"},
				rows: [][]driver.Value{{
					int64(99),
					shared.MissingTimestamp,
					shared.MissingTimestamp,
					shared.MissingTimestamp,
					int64(512),
					int64(0),
					"missing_ts.jpg",
					int64(repo.EntryStatusReady),
					"image/jpeg",
				}},
			}, nil
		})
		defer cleanup()

		entry, err := r.GetEntry(ctx, dbID, 99)
		if err != nil {
			t.Fatalf("GetEntry failed: %v", err)
		}
		if !entry.Timestamp.IsZero() || !entry.CreatedAt.IsZero() || !entry.UpdatedAt.IsZero() {
			t.Fatalf("expected zero times for MissingTimestamp sentinel, got ts=%v created=%v updated=%v", entry.Timestamp, entry.CreatedAt, entry.UpdatedAt)
		}
	})
}

func TestPostgres_GetLogs_Pre1970AndEpochTimestamps(t *testing.T) {
	ctx := context.Background()

	t1950Noon := time.Date(1950, 1, 1, 12, 0, 0, 0, time.UTC)
	t1950Midnight := time.UnixMilli(-631152000000).UTC()
	tEpoch := time.Unix(0, 0).UTC()
	tModern := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	r, cleanup := newTestPostgresRepo(func(query string, _ []driver.NamedValue) (tsStubResultRow, error) {
		if !strings.Contains(query, "audit_logs") {
			return tsStubResultRow{}, nil
		}
		return tsStubResultRow{
			cols: []string{"id", "timestamp", "action", "actor", "resource", "details"},
			rows: [][]driver.Value{
				{int64(1), t1950Midnight.UnixMilli(), "CREATE", "admin", "db1", `{"label":"1950-midnight"}`},
				{int64(2), t1950Noon.UnixMilli(), "CREATE", "admin", "db1", `{"label":"1950-noon"}`},
				{int64(3), tEpoch.UnixMilli(), "CREATE", "admin", "db1", `{"label":"1970-epoch"}`},
				{int64(4), tModern.UnixMilli(), "CREATE", "admin", "db1", `{"label":"2025-modern"}`},
			},
		}, nil
	})
	defer cleanup()

	logs, err := r.GetLogs(ctx, repo.QueryOptions{})
	if err != nil {
		t.Fatalf("GetLogs failed: %v", err)
	}
	if len(logs) != 4 {
		t.Fatalf("expected 4 logs, got %d", len(logs))
	}

	expected := []time.Time{t1950Midnight, t1950Noon, tEpoch, tModern}
	for i, want := range expected {
		got := logs[i].Timestamp
		if got.IsZero() || got.Year() == 1 {
			t.Fatalf("log[%d] became Year 1 / zero time: %v", i, got)
		}
		if !got.Equal(want) {
			t.Errorf("log[%d] expected Timestamp %v, got %v", i, want, got)
		}
	}

	t.Run("missing timestamp sentinel scans as zero time.Time", func(t *testing.T) {
		rSentinel, cleanupSentinel := newTestPostgresRepo(func(query string, _ []driver.NamedValue) (tsStubResultRow, error) {
			if !strings.Contains(query, "audit_logs") {
				return tsStubResultRow{}, nil
			}
			return tsStubResultRow{
				cols: []string{"id", "timestamp", "action", "actor", "resource", "details"},
				rows: [][]driver.Value{
					{int64(5), shared.MissingTimestamp, "CREATE", "admin", "db1", `{"label":"missing-sentinel"}`},
				},
			}, nil
		})
		defer cleanupSentinel()

		sentinelLogs, err := rSentinel.GetLogs(ctx, repo.QueryOptions{})
		if err != nil {
			t.Fatalf("GetLogs failed: %v", err)
		}
		if len(sentinelLogs) != 1 || !sentinelLogs[0].Timestamp.IsZero() {
			t.Fatalf("expected 1 log with zero Timestamp for MissingTimestamp sentinel, got %+v", sentinelLogs)
		}
	})
}

func TestPostgres_UpdateEntryMetadata_TimestampHandling(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")

	t.Run("zero timestamp omits timestamp column from UPDATE", func(t *testing.T) {
		var recorded []tsStubRecordedQuery
		r, cleanup := newTestPostgresRepo(func(query string, args []driver.NamedValue) (tsStubResultRow, error) {
			if strings.Contains(query, "database_custom_fields") {
				return tsStubResultRow{
					cols: []string{"field_id", "name", "type", "is_indexed"},
					rows: nil,
				}, nil
			}
			vals := make([]driver.Value, len(args))
			for i, a := range args {
				vals[i] = a.Value
			}
			recorded = append(recorded, tsStubRecordedQuery{query: query, args: vals})
			return tsStubResultRow{
				cols: []string{"updated_at"},
				rows: [][]driver.Value{{int64(1700000000000)}},
			}, nil
		})
		defer cleanup()

		_, err := r.UpdateEntryMetadata(ctx, dbID, repo.Entry{
			ID:       7,
			FileName: "renamed.jpg",
		})
		if err != nil {
			t.Fatalf("UpdateEntryMetadata failed: %v", err)
		}
		if len(recorded) != 1 {
			t.Fatalf("expected 1 UPDATE query, got %d", len(recorded))
		}
		if strings.Contains(recorded[0].query, "timestamp =") {
			t.Errorf("expected UPDATE not to set timestamp when entry.Timestamp is zero, got: %s", recorded[0].query)
		}
	})

	t.Run("pre-1970 and epoch timestamps set timestamp column in UPDATE", func(t *testing.T) {
		for _, want := range []time.Time{
			time.UnixMilli(-631152000000).UTC(),
			time.Unix(0, 0).UTC(),
		} {
			var recorded []tsStubRecordedQuery
			r, cleanup := newTestPostgresRepo(func(query string, args []driver.NamedValue) (tsStubResultRow, error) {
				if strings.Contains(query, "database_custom_fields") {
					return tsStubResultRow{
						cols: []string{"field_id", "name", "type", "is_indexed"},
						rows: nil,
					}, nil
				}
				vals := make([]driver.Value, len(args))
				for i, a := range args {
					vals[i] = a.Value
				}
				recorded = append(recorded, tsStubRecordedQuery{query: query, args: vals})
				return tsStubResultRow{
					cols: []string{"updated_at"},
					rows: [][]driver.Value{{int64(1700000000000)}},
				}, nil
			})

			_, err := r.UpdateEntryMetadata(ctx, dbID, repo.Entry{
				ID:        7,
				FileName:  "historical.jpg",
				Timestamp: want,
			})
			cleanup()
			if err != nil {
				t.Fatalf("UpdateEntryMetadata(%v) failed: %v", want, err)
			}
			if len(recorded) != 1 {
				t.Fatalf("expected 1 UPDATE query, got %d", len(recorded))
			}
			if !strings.Contains(recorded[0].query, "timestamp =") {
				t.Errorf("expected UPDATE to set timestamp for %v, got: %s", want, recorded[0].query)
			}
			found := false
			for _, arg := range recorded[0].args {
				if arg == want.UnixMilli() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected arg %d in UPDATE args %v", want.UnixMilli(), recorded[0].args)
			}
		}
	})
}
