package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	"mediahub_oss/internal/repository/sqlutil"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"

	"github.com/Masterminds/squirrel"
	"github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// TestInterfaceImplementation verifies PostgresRepository implements repository.Repository interface.
func TestInterfaceImplementation(t *testing.T) {
	var _ repo.Repository = (*PostgresRepository)(nil)
}

func TestBuildDynamicTableSchema(t *testing.T) {
	r := &PostgresRepository{
		Builder:         squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
		AllowedStatuses: repo.GetAllEntryStatuses(),
		MediaFields: map[string][]MediaField{
			"image": {
				{Name: "width", PostgresType: "INTEGER"},
				{Name: "height", PostgresType: "INTEGER"},
			},
		},
	}

	customFields := []repo.CustomFieldDef{
		{ID: 0, Name: "artist", Type: repo.CustomFieldTypeText, IsIndexed: true},
		{ID: 1, Name: "year", Type: repo.CustomFieldTypeInteger, IsIndexed: false},
		{ID: 2, Name: "rating", Type: repo.CustomFieldTypeReal, IsIndexed: true},
		{ID: 3, Name: "is_favorite", Type: repo.CustomFieldTypeBoolean, IsIndexed: false},
		{ID: 4, Name: "score", Type: repo.CustomFieldTypeReal, IsIndexed: false},
		{ID: 5, Name: "count", Type: repo.CustomFieldTypeInteger, IsIndexed: false},
		{ID: 6, Name: "location", Type: repo.CustomFieldTypeCoordinate, IsIndexed: true},
	}

	sql, err := r.BuildDynamicTableSchema(ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"), "image", customFields)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if !strings.Contains(sql, `"entries_01HGFB9Z5W7ABCDEFGHJKMNPQR"`) {
		t.Errorf("expected quoted table name in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, `"cf_0" TEXT`) {
		t.Errorf("expected cf_0 column in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, `"cf_1" BIGINT`) {
		t.Errorf("expected cf_1 BIGINT column in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, `"cf_2" DOUBLE PRECISION`) {
		t.Errorf("expected cf_2 column in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, `"cf_3" BOOLEAN`) {
		t.Errorf("expected cf_3 column in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, `"cf_4" DOUBLE PRECISION`) {
		t.Errorf("expected cf_4 column in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, `"cf_5" BIGINT`) {
		t.Errorf("expected cf_5 BIGINT column in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, `"cf_6" POINT`) {
		t.Errorf("expected cf_6 POINT column in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, `id SERIAL PRIMARY KEY`) {
		t.Errorf("expected SERIAL id column in SQL, got: %s", sql)
	}
}

func TestBuildIndexesSQL_Coordinate(t *testing.T) {
	customFields := []repo.CustomFieldDef{
		{ID: 0, Name: "artist", Type: repo.CustomFieldTypeText, IsIndexed: true},
		{ID: 1, Name: "location", Type: repo.CustomFieldTypeCoordinate, IsIndexed: true},
		{ID: 2, Name: "rating", Type: repo.CustomFieldTypeReal, IsIndexed: false},
	}

	sqls := BuildIndexesSQL(ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"), customFields)
	foundGist := false
	foundBtree := false
	for _, s := range sqls {
		if strings.Contains(s, `USING gist("cf_1")`) {
			foundGist = true
		}
		if strings.Contains(s, `ON "entries_01HGFB9Z5W7ABCDEFGHJKMNPQR"("cf_0")`) {
			foundBtree = true
		}
	}
	if !foundGist {
		t.Errorf("expected GiST index for coordinate field, got indexes: %v", sqls)
	}
	if !foundBtree {
		t.Errorf("expected standard B-Tree index for cf_0, got indexes: %v", sqls)
	}
}

func TestPQUniqueViolation(t *testing.T) {
	err1 := &pq.Error{Code: "23505"}
	if !isPQUniqueViolation(err1) {
		t.Errorf("expected isPQUniqueViolation to return true for 23505 error")
	}

	err2 := errors.New("duplicate key value violates unique constraint")
	if !isPQUniqueViolation(err2) {
		t.Errorf("expected isPQUniqueViolation to return true for text match")
	}

	err3 := errors.New("some other error")
	if isPQUniqueViolation(err3) {
		t.Errorf("expected isPQUniqueViolation to return false")
	}
}

func TestPQUndefinedTable(t *testing.T) {
	if isPQUndefinedTable(nil) {
		t.Errorf("expected isPQUndefinedTable(nil) to return false")
	}
	if !isPQUndefinedTable(&pq.Error{Code: "42P01"}) {
		t.Errorf("expected isPQUndefinedTable to return true for 42P01")
	}
	if isPQUndefinedTable(&pq.Error{Code: "23505"}) {
		t.Errorf("expected isPQUndefinedTable to return false for 23505")
	}
	if !isPQUndefinedTable(errors.New(`pq: relation "entries_123" does not exist`)) {
		t.Errorf("expected isPQUndefinedTable to return true for 'does not exist'")
	}
	if !isPQUndefinedTable(errors.New("ERROR: UNDEFINED_TABLE")) {
		t.Errorf("expected isPQUndefinedTable to return true for 'undefined_table'")
	}
	if isPQUndefinedTable(errors.New("some other error")) {
		t.Errorf("expected isPQUndefinedTable to return false for unrelated error")
	}
}

func TestValidOperator(t *testing.T) {
	validOps := []string{"=", "!=", ">", ">=", "<", "<=", "LIKE", "ILIKE", "like", "ilike"}
	for _, op := range validOps {
		if !sqlutil.IsValidSearchOperator(op) {
			t.Errorf("expected operator %s to be valid", op)
		}
	}

	if sqlutil.IsValidSearchOperator("DROP") {
		t.Errorf("expected invalid operator to return false")
	}
}

func TestMapToPostgresType(t *testing.T) {
	tests := map[string]string{
		"INTEGER":    "BIGINT",
		"INT":        "BIGINT",
		"INT32":      "INTEGER",
		"UINT32":     "INTEGER",
		"REAL":       "DOUBLE PRECISION",
		"TEXT":       "TEXT",
		"BOOLEAN":    "BOOLEAN",
		"COORDINATE": "POINT",
		"POINT":      "POINT",
		"uint64":     "BIGINT",
		"INT64":      "BIGINT",
		"float64":    "DOUBLE PRECISION",
		"uint8":      "SMALLINT",
		"SMALLINT":   "SMALLINT",
	}
	for in, expected := range tests {
		got := mapToPostgresType(in)
		if got != expected {
			t.Errorf("mapToPostgresType(%s) = %s, expected %s", in, got, expected)
		}
	}
}

func TestAllMediaTypesConfigured(t *testing.T) {
	for _, contentType := range media.GetContentTypes() {
		_, err := media.GetMetadataFields(contentType)
		if err != nil {
			t.Errorf("failed to get metadata fields for content type %s: %v", contentType, err)
		}
	}
}

func TestAcquireLockQuery(t *testing.T) {
	r := &PostgresRepository{
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}

	ttlMs := int64(30000)
	query, _, err := r.Builder.Insert("system_locks").
		Columns("lock_name", "locked_at", "locked_by", "expires_at").
		Values(
			"hk_lock",
			squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT"),
			"worker-1",
			squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 + ?)::BIGINT", ttlMs),
		).
		Suffix(`
            ON CONFLICT (lock_name) DO UPDATE 
            SET locked_at = EXCLUDED.locked_at, 
                locked_by = EXCLUDED.locked_by, 
                expires_at = EXCLUDED.expires_at 
            WHERE system_locks.expires_at < (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)
               OR system_locks.locked_by = EXCLUDED.locked_by
            RETURNING lock_name
        `).
		ToSql()

	if err != nil {
		t.Fatalf("failed to build query: %v", err)
	}

	if !strings.Contains(query, "clock_timestamp()") {
		t.Errorf("expected clock_timestamp() in query, got: %s", query)
	}
	if !strings.Contains(query, "system_locks.locked_by = EXCLUDED.locked_by") {
		t.Errorf("expected owner renewal check in query, got: %s", query)
	}
}

func TestCreateEntryQuery_DynamicTimestamp(t *testing.T) {
	r := &PostgresRepository{
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}

	dbNowExpr := squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")

	t.Run("zero timestamp uses clock_timestamp expression", func(t *testing.T) {
		insertData := map[string]any{
			"created_at":       dbNowExpr,
			"updated_at":       dbNowExpr,
			"timestamp":        dbNowExpr,
			"filesize":         1024,
			"preview_filesize": 0,
			"filename":         "test.jpg",
			"status":           repo.EntryStatusReady,
			"mime_type":        "image/jpeg",
		}

		query, args, err := r.Builder.Insert(`"entries_01HGFB9Z5W7ABCDEFGHJKMNPQR"`).
			SetMap(insertData).
			Suffix("RETURNING id, timestamp, created_at, updated_at").
			ToSql()

		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}

		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in query, got: %s", query)
		}
		if !strings.Contains(query, "RETURNING id, timestamp, created_at, updated_at") {
			t.Errorf("expected RETURNING clause in query, got: %s", query)
		}
		// Since created_at, updated_at, timestamp are raw expressions, args only contain filesize, preview_filesize, filename, status, mime_type
		if len(args) != 5 {
			t.Errorf("expected 5 args for raw timestamp expressions, got: %d (%v)", len(args), args)
		}
	})

	t.Run("explicit timestamp uses parameter binding", func(t *testing.T) {
		explicitTS := int64(1700000000000)
		insertData := map[string]any{
			"created_at":       dbNowExpr,
			"updated_at":       dbNowExpr,
			"timestamp":        explicitTS,
			"filesize":         1024,
			"preview_filesize": 0,
			"filename":         "test.jpg",
			"status":           repo.EntryStatusReady,
			"mime_type":        "image/jpeg",
		}

		query, args, err := r.Builder.Insert(`"entries_01HGFB9Z5W7ABCDEFGHJKMNPQR"`).
			SetMap(insertData).
			Suffix("RETURNING id, timestamp, created_at, updated_at").
			ToSql()

		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}

		if !strings.Contains(query, "RETURNING id, timestamp, created_at, updated_at") {
			t.Errorf("expected RETURNING clause in query, got: %s", query)
		}

		if len(args) != 6 {
			t.Errorf("expected 6 args when timestamp is provided, got: %d (%v)", len(args), args)
		}

		foundTS := false
		for _, arg := range args {
			if arg == explicitTS {
				foundTS = true
				break
			}
		}
		if !foundTS {
			t.Errorf("expected explicit timestamp %d in args, got: %v", explicitTS, args)
		}
	})
}

func TestDynamicTimestampQueries(t *testing.T) {
	r := &PostgresRepository{
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}

	t.Run("UpdateEntriesStatus query", func(t *testing.T) {
		query, _, err := r.Builder.Update(`"entries_01HGFB9Z5W7ABCDEFGHJKMNPQR"`).
			Set("status", repo.EntryStatusReady).
			Set("updated_at", squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")).
			Where(squirrel.Eq{"id": []int64{1, 2, 3}}).
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in UpdateEntriesStatus query, got: %s", query)
		}
	})

	t.Run("ClaimQueuedEntry query", func(t *testing.T) {
		query, _, err := r.Builder.Update(`"entries_01HGFB9Z5W7ABCDEFGHJKMNPQR"`).
			Set("status", repo.EntryStatusProcessing).
			Set("updated_at", squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")).
			Where(squirrel.Eq{"id": int64(10), "status": repo.EntryStatusQueued}).
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in ClaimQueuedEntry query, got: %s", query)
		}
	})

	t.Run("UpdateAPIKeyLastUsed query", func(t *testing.T) {
		query, args, err := r.Builder.Update("api_keys").
			Set("last_used_at", squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 - ?)::BIGINT", int64(5000))).
			Where(squirrel.Eq{"id": "01HGFB9Z5W7ABCDEFGHJKMNPQR"}).
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in UpdateAPIKeyLastUsed query, got: %s", query)
		}
		if len(args) != 2 || args[0] != int64(5000) {
			t.Errorf("expected lastUsed duration in args, got: %v", args)
		}
	})

	t.Run("DeleteExpiredAPIKeys query", func(t *testing.T) {
		query, _, err := r.Builder.Delete("api_keys").
			Where(squirrel.Expr("expires_at IS NOT NULL AND expires_at < (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)")).
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in DeleteExpiredAPIKeys query, got: %s", query)
		}
	})

	t.Run("StoreRefreshToken query", func(t *testing.T) {
		query, args, err := r.Builder.Insert("refresh_tokens").
			Columns("user_id", "token_hash", "expiry").
			Values("user1", "hash1", squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 + ?)::BIGINT", int64(3600000))).
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in StoreRefreshToken query, got: %s", query)
		}
		if len(args) != 3 {
			t.Errorf("expected 3 args, got: %v", args)
		}
	})

	t.Run("ValidateRefreshToken query", func(t *testing.T) {
		query, args, err := r.Builder.Select("user_id").
			From("refresh_tokens").
			Where(squirrel.Expr("token_hash = ? AND expiry >= (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)", "hash1")).
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in ValidateRefreshToken query, got: %s", query)
		}
		if len(args) != 1 || args[0] != "hash1" {
			t.Errorf("expected hash1 in args, got: %v", args)
		}
	})

	t.Run("DeleteExpiredRefreshTokens query", func(t *testing.T) {
		query, _, err := r.Builder.Delete("refresh_tokens").
			Where(squirrel.Expr("expiry < (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)")).
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in DeleteExpiredRefreshTokens query, got: %s", query)
		}
	})

	t.Run("DeleteLogs query", func(t *testing.T) {
		query, args, err := r.Builder.Delete("audit_logs").
			Where(squirrel.Expr("timestamp < (EXTRACT(EPOCH FROM clock_timestamp()) * 1000 - ?)", int64(86400000))).
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in DeleteLogs query, got: %s", query)
		}
		if len(args) != 1 || args[0] != int64(86400000) {
			t.Errorf("expected maxAge in args, got: %v", args)
		}
	})

	t.Run("HouseKeepingWasCalled query", func(t *testing.T) {
		query, _, err := r.Builder.Update("databases").
			Set("hk_last_run", squirrel.Expr("(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT")).
			Where(squirrel.Eq{"id": "01HGFB9Z5W7ABCDEFGHJKMNPQR"}).
			Suffix("RETURNING hk_last_run").
			ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		if !strings.Contains(query, "clock_timestamp()") {
			t.Errorf("expected clock_timestamp() in HouseKeepingWasCalled query, got: %s", query)
		}
		if !strings.Contains(query, "RETURNING hk_last_run") {
			t.Errorf("expected RETURNING in HouseKeepingWasCalled query, got: %s", query)
		}
	})
}

func TestPostgres_TimeFilterQueryBuilder(t *testing.T) {
	r := &PostgresRepository{
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}

	t.Run("GetEntries with epoch 0 and pre-1970 timestamp builds WHERE clauses", func(t *testing.T) {
		tStart := time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC)
		tEnd := time.Unix(0, 0).UTC() // Epoch = 0

		opts := repo.QueryOptions{
			TStart:    tStart,
			TEnd:      tEnd,
			TimeField: "timestamp",
		}
		if err := opts.Validate(); err != nil {
			t.Fatalf("opts validate failed: %v", err)
		}

		builder := r.Builder.Select("*").From(`"entries_01HGFB9Z5W7ABCDEFGHJKMNPQR"`)
		if !opts.TStart.IsZero() {
			builder = builder.Where(squirrel.GtOrEq{opts.TimeField: opts.TStart.UnixMilli()})
		}
		if !opts.TEnd.IsZero() {
			builder = builder.Where(squirrel.LtOrEq{opts.TimeField: opts.TEnd.UnixMilli()})
		}

		query, args, err := builder.ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}

		if !strings.Contains(query, "timestamp >=") || !strings.Contains(query, "timestamp <=") {
			t.Errorf("expected timestamp >= and <= in query, got: %s", query)
		}
		if len(args) != 2 {
			t.Fatalf("expected 2 args, got %d (%v)", len(args), args)
		}
		if args[0] != tStart.UnixMilli() || args[1] != int64(0) {
			t.Errorf("expected args [%d, 0], got %v", tStart.UnixMilli(), args)
		}
	})

	t.Run("GetLogs with epoch 0 builds WHERE clauses", func(t *testing.T) {
		tEpoch := time.Unix(0, 0).UTC()
		opts := repo.QueryOptions{
			TStart: tEpoch,
			TEnd:   tEpoch,
		}
		if err := opts.Validate(); err != nil {
			t.Fatalf("opts validate failed: %v", err)
		}

		builder := r.Builder.Select("id", "timestamp", "action", "actor", "resource", "details").From("audit_logs")
		if !opts.TStart.IsZero() {
			builder = builder.Where(squirrel.GtOrEq{"timestamp": opts.TStart.UnixMilli()})
		}
		if !opts.TEnd.IsZero() {
			builder = builder.Where(squirrel.LtOrEq{"timestamp": opts.TEnd.UnixMilli()})
		}

		query, args, err := builder.ToSql()
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}

		if !strings.Contains(query, "timestamp >=") || !strings.Contains(query, "timestamp <=") {
			t.Errorf("expected timestamp >= and <= in query, got: %s", query)
		}
		if len(args) != 2 || args[0] != int64(0) || args[1] != int64(0) {
			t.Errorf("expected args [0, 0], got %v", args)
		}
	})
}

func TestPostgresRepository_InvalidULIDValidation(t *testing.T) {
	r := &PostgresRepository{
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}
	ctx := t.Context()

	if _, err := r.BuildDynamicTableSchema(repo.ULID{}, "image", nil); !errors.Is(err, customerrors.ErrValidation) {
		t.Errorf("expected ErrValidation from BuildDynamicTableSchema, got: %v", err)
	}

	validID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	if _, err := r.EnqueueEntry(ctx, validID, repo.Entry{ID: 1}, 0); !errors.Is(err, customerrors.ErrUnavailable) {
		t.Errorf("expected ErrUnavailable from EnqueueEntry with maxQueued=0, got: %v", err)
	}

	if _, err := r.UpdateDatabase(ctx, repo.Database{ID: validID, Name: "invalid name!"}); !errors.Is(err, customerrors.ErrInvalidName) {
		t.Errorf("expected ErrInvalidName from UpdateDatabase with invalid name, got: %v", err)
	}
}

type lockRecQuery struct {
	sql  string
	inTx bool
}

type lockRecRecorder struct {
	queries      []lockRecQuery
	inTx         bool
	customFields [][]driver.Value
	lockTableErr error
}

type lockRecDriver struct{}

func (d *lockRecDriver) Open(name string) (driver.Conn, error) {
	return nil, errors.New("lockRecDriver only supports OpenDB")
}

type lockRecConnector struct {
	rec *lockRecRecorder
}

func (c *lockRecConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return &lockRecConn{rec: c.rec}, nil
}

func (c *lockRecConnector) Driver() driver.Driver {
	return &lockRecDriver{}
}

type lockRecConn struct {
	rec *lockRecRecorder
}

func (c *lockRecConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (c *lockRecConn) Close() error { return nil }

func (c *lockRecConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *lockRecConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.rec.inTx = true
	return &lockRecTx{rec: c.rec}, nil
}

func (c *lockRecConn) CheckNamedValue(nv *driver.NamedValue) error {
	return nil
}

func (c *lockRecConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.rec.queries = append(c.rec.queries, lockRecQuery{sql: query, inTx: c.rec.inTx})
	if strings.HasPrefix(query, "LOCK TABLE ") && c.rec.lockTableErr != nil {
		return nil, c.rec.lockTableErr
	}
	return &lockRecResult{rowsAffected: 1}, nil
}

func (c *lockRecConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.rec.queries = append(c.rec.queries, lockRecQuery{sql: query, inTx: c.rec.inTx})
	switch {
	case strings.Contains(query, "SELECT filesize, preview_filesize, status"):
		return &lockRecRows{
			cols: []string{"filesize", "preview_filesize", "status"},
			data: [][]driver.Value{{int64(100), int64(0), int64(repo.EntryStatusProcessing)}},
		}, nil
	case strings.Contains(query, "SELECT id, status"):
		return &lockRecRows{
			cols: []string{"id", "status"},
			data: [][]driver.Value{
				{int64(1), int64(repo.EntryStatusQueued)},
				{int64(2), int64(repo.EntryStatusProcessing)},
			},
		}, nil
	case strings.Contains(query, "RETURNING updated_at"):
		return &lockRecRows{
			cols: []string{"updated_at"},
			data: [][]driver.Value{{int64(1700000000000)}},
		}, nil
	case strings.Contains(query, "SELECT 1 FROM databases WHERE id = $1 FOR UPDATE"):
		return &lockRecRows{
			cols: []string{"one"},
			data: [][]driver.Value{{int64(1)}},
		}, nil
	case strings.Contains(query, "FROM database_custom_fields"):
		return &lockRecRows{
			cols: []string{"field_id", "name", "type", "is_indexed"},
			data: c.rec.customFields,
		}, nil
	default:
		return nil, fmt.Errorf("unexpected query in lockRecConn: %s", query)
	}
}

type lockRecTx struct {
	rec *lockRecRecorder
}

func (tx *lockRecTx) Commit() error {
	tx.rec.inTx = false
	return nil
}

func (tx *lockRecTx) Rollback() error {
	tx.rec.inTx = false
	return nil
}

type lockRecResult struct {
	rowsAffected int64
}

func (r *lockRecResult) LastInsertId() (int64, error) { return 0, nil }
func (r *lockRecResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

type lockRecRows struct {
	cols []string
	data [][]driver.Value
	idx  int
}

func (r *lockRecRows) Columns() []string { return r.cols }
func (r *lockRecRows) Close() error      { return nil }
func (r *lockRecRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.idx])
	r.idx++
	return nil
}

func newRecordingPostgresRepo(t *testing.T) (*PostgresRepository, *lockRecRecorder) {
	t.Helper()
	rec := &lockRecRecorder{}
	db := sql.OpenDB(&lockRecConnector{rec: rec})
	t.Cleanup(func() { _ = db.Close() })
	return &PostgresRepository{
		DB:      db,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}, rec
}

func TestPostgres_LockingQueries(t *testing.T) {
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	entriesTable := `"entries_` + dbID.String() + `"`
	ctx := t.Context()

	t.Run("UpdateEntry acquires row lock with FOR UPDATE", func(t *testing.T) {
		r, rec := newRecordingPostgresRepo(t)
		_, err := r.UpdateEntry(ctx, dbID, repo.Entry{
			ID:       42,
			Size:     256,
			FileName: "test.jpg",
			Status:   repo.EntryStatusReady,
			MimeType: "image/jpeg",
		})
		if err != nil {
			t.Fatalf("UpdateEntry failed: %v", err)
		}

		foundLock := false
		for _, q := range rec.queries {
			if q.inTx && strings.HasPrefix(q.sql, "SELECT ") && strings.Contains(q.sql, entriesTable) && strings.HasSuffix(strings.TrimSpace(q.sql), "FOR UPDATE") {
				foundLock = true
				break
			}
		}
		if !foundLock {
			t.Fatalf("expected SELECT ... FOR UPDATE on %s inside transaction, got queries: %+v", entriesTable, rec.queries)
		}
	})

	t.Run("EnqueueEntry acquires row lock with FOR UPDATE before updating databases", func(t *testing.T) {
		r, rec := newRecordingPostgresRepo(t)
		_, err := r.EnqueueEntry(ctx, dbID, repo.Entry{
			ID:       42,
			Size:     256,
			FileName: "test.jpg",
			MimeType: "image/jpeg",
		}, 10)
		if err != nil {
			t.Fatalf("EnqueueEntry failed: %v", err)
		}

		lockIdx, entryUpdateIdx, dbUpdateIdx := -1, -1, -1
		for i, q := range rec.queries {
			if q.inTx && strings.HasPrefix(q.sql, "SELECT ") && strings.Contains(q.sql, entriesTable) && strings.HasSuffix(strings.TrimSpace(q.sql), "FOR UPDATE") {
				lockIdx = i
			}
			if q.inTx && strings.HasPrefix(q.sql, "UPDATE "+entriesTable) {
				entryUpdateIdx = i
			}
			if q.inTx && strings.HasPrefix(q.sql, "UPDATE databases") {
				dbUpdateIdx = i
			}
		}
		if lockIdx == -1 || entryUpdateIdx == -1 || dbUpdateIdx == -1 {
			t.Fatalf("missing expected queries (lockIdx=%d, entryUpdateIdx=%d, dbUpdateIdx=%d), got: %+v", lockIdx, entryUpdateIdx, dbUpdateIdx, rec.queries)
		}
		if !(lockIdx < entryUpdateIdx && entryUpdateIdx < dbUpdateIdx) {
			t.Fatalf("expected lock (%d) -> update %s (%d) -> update databases (%d), got queries: %+v", lockIdx, entriesTable, entryUpdateIdx, dbUpdateIdx, rec.queries)
		}
	})

	t.Run("UpdateEntriesStatus acquires row locks with FOR UPDATE", func(t *testing.T) {
		r, rec := newRecordingPostgresRepo(t)
		err := r.UpdateEntriesStatus(ctx, dbID, []int64{1, 2}, repo.EntryStatusReady)
		if err != nil {
			t.Fatalf("UpdateEntriesStatus failed: %v", err)
		}

		foundLock := false
		for _, q := range rec.queries {
			if q.inTx && strings.HasPrefix(q.sql, "SELECT id, status") && strings.Contains(q.sql, entriesTable) && strings.HasSuffix(strings.TrimSpace(q.sql), "FOR UPDATE") {
				foundLock = true
				break
			}
		}
		if !foundLock {
			t.Fatalf("expected SELECT id, status ... FOR UPDATE on %s inside transaction, got queries: %+v", entriesTable, rec.queries)
		}
	})

	t.Run("AddCustomField locks entries table ACCESS EXCLUSIVE before database row FOR UPDATE", func(t *testing.T) {
		r, rec := newRecordingPostgresRepo(t)
		_, err := r.AddCustomField(ctx, dbID, repo.CustomFieldDef{
			Name:      "artist",
			Type:      repo.CustomFieldTypeText,
			IsIndexed: true,
		})
		if err != nil {
			t.Fatalf("AddCustomField failed: %v", err)
		}

		expectedLockTable := fmt.Sprintf(`LOCK TABLE %s IN ACCESS EXCLUSIVE MODE`, entriesTable)
		tableLockIdx, dbLockIdx, fieldsIdx := -1, -1, -1
		for i, q := range rec.queries {
			if q.inTx && q.sql == expectedLockTable {
				tableLockIdx = i
			}
			if q.inTx && q.sql == "SELECT 1 FROM databases WHERE id = $1 FOR UPDATE" {
				dbLockIdx = i
			}
			if q.inTx && strings.Contains(q.sql, "FROM database_custom_fields") {
				fieldsIdx = i
			}
		}
		if tableLockIdx == -1 || dbLockIdx == -1 || fieldsIdx == -1 {
			t.Fatalf("missing expected queries (tableLockIdx=%d, dbLockIdx=%d, fieldsIdx=%d), got: %+v", tableLockIdx, dbLockIdx, fieldsIdx, rec.queries)
		}
		if !(tableLockIdx < dbLockIdx && dbLockIdx < fieldsIdx) {
			t.Fatalf("expected LOCK TABLE (%d) -> database FOR UPDATE (%d) -> query database_custom_fields (%d), got queries: %+v", tableLockIdx, dbLockIdx, fieldsIdx, rec.queries)
		}
	})

	t.Run("DeleteCustomField locks entries table ACCESS EXCLUSIVE before database row FOR UPDATE", func(t *testing.T) {
		r, rec := newRecordingPostgresRepo(t)
		rec.customFields = [][]driver.Value{{int64(0), "artist", "TEXT", true}}
		if err := r.DeleteCustomField(ctx, dbID, 0); err != nil {
			t.Fatalf("DeleteCustomField failed: %v", err)
		}

		expectedLockTable := fmt.Sprintf(`LOCK TABLE %s IN ACCESS EXCLUSIVE MODE`, entriesTable)
		tableLockIdx, dbLockIdx := -1, -1
		for i, q := range rec.queries {
			if q.inTx && q.sql == expectedLockTable {
				tableLockIdx = i
			}
			if q.inTx && q.sql == "SELECT 1 FROM databases WHERE id = $1 FOR UPDATE" {
				dbLockIdx = i
			}
		}
		if tableLockIdx == -1 || dbLockIdx == -1 {
			t.Fatalf("missing expected queries (tableLockIdx=%d, dbLockIdx=%d), got: %+v", tableLockIdx, dbLockIdx, rec.queries)
		}
		if tableLockIdx >= dbLockIdx {
			t.Fatalf("expected LOCK TABLE (%d) before database FOR UPDATE (%d), got queries: %+v", tableLockIdx, dbLockIdx, rec.queries)
		}
	})

	t.Run("UpdateCustomField uses SHARE lock when enabling index", func(t *testing.T) {
		r, rec := newRecordingPostgresRepo(t)
		rec.customFields = [][]driver.Value{{int64(0), "artist", "TEXT", false}}
		enableIdx := true
		if _, err := r.UpdateCustomField(ctx, dbID, 0, nil, &enableIdx); err != nil {
			t.Fatalf("UpdateCustomField failed: %v", err)
		}

		expectedLockTable := fmt.Sprintf(`LOCK TABLE %s IN SHARE MODE`, entriesTable)
		tableLockIdx, dbLockIdx := -1, -1
		for i, q := range rec.queries {
			if q.inTx && q.sql == expectedLockTable {
				tableLockIdx = i
			}
			if q.inTx && q.sql == "SELECT 1 FROM databases WHERE id = $1 FOR UPDATE" {
				dbLockIdx = i
			}
		}
		if tableLockIdx == -1 || dbLockIdx == -1 {
			t.Fatalf("missing expected queries (tableLockIdx=%d, dbLockIdx=%d), got: %+v", tableLockIdx, dbLockIdx, rec.queries)
		}
		if tableLockIdx >= dbLockIdx {
			t.Fatalf("expected LOCK TABLE SHARE (%d) before database FOR UPDATE (%d), got queries: %+v", tableLockIdx, dbLockIdx, rec.queries)
		}
	})

	t.Run("UpdateCustomField uses ACCESS EXCLUSIVE lock when disabling index", func(t *testing.T) {
		r, rec := newRecordingPostgresRepo(t)
		rec.customFields = [][]driver.Value{{int64(0), "artist", "TEXT", true}}
		disableIdx := false
		if _, err := r.UpdateCustomField(ctx, dbID, 0, nil, &disableIdx); err != nil {
			t.Fatalf("UpdateCustomField failed: %v", err)
		}

		expectedLockTable := fmt.Sprintf(`LOCK TABLE %s IN ACCESS EXCLUSIVE MODE`, entriesTable)
		tableLockIdx, dbLockIdx := -1, -1
		for i, q := range rec.queries {
			if q.inTx && q.sql == expectedLockTable {
				tableLockIdx = i
			}
			if q.inTx && q.sql == "SELECT 1 FROM databases WHERE id = $1 FOR UPDATE" {
				dbLockIdx = i
			}
		}
		if tableLockIdx == -1 || dbLockIdx == -1 {
			t.Fatalf("missing expected queries (tableLockIdx=%d, dbLockIdx=%d), got: %+v", tableLockIdx, dbLockIdx, rec.queries)
		}
		if tableLockIdx >= dbLockIdx {
			t.Fatalf("expected LOCK TABLE ACCESS EXCLUSIVE (%d) before database FOR UPDATE (%d), got queries: %+v", tableLockIdx, dbLockIdx, rec.queries)
		}
	})

	t.Run("UpdateCustomField skips LOCK TABLE when isIndexed is nil", func(t *testing.T) {
		r, rec := newRecordingPostgresRepo(t)
		rec.customFields = [][]driver.Value{{int64(0), "artist", "TEXT", false}}
		newName := "creator"
		if _, err := r.UpdateCustomField(ctx, dbID, 0, &newName, nil); err != nil {
			t.Fatalf("UpdateCustomField rename failed: %v", err)
		}

		dbLockFound := false
		for _, q := range rec.queries {
			if strings.HasPrefix(q.sql, "LOCK TABLE ") {
				t.Fatalf("unexpected LOCK TABLE query on rename-only UpdateCustomField: %s", q.sql)
			}
			if q.inTx && q.sql == "SELECT 1 FROM databases WHERE id = $1 FOR UPDATE" {
				dbLockFound = true
			}
		}
		if !dbLockFound {
			t.Fatalf("expected SELECT 1 FROM databases WHERE id = $1 FOR UPDATE, got queries: %+v", rec.queries)
		}
	})

	t.Run("CustomField LOCK TABLE undefined_table 42P01 returns ErrNotFound", func(t *testing.T) {
		undefinedErr := &pq.Error{Code: "42P01"}

		r1, rec1 := newRecordingPostgresRepo(t)
		rec1.lockTableErr = undefinedErr
		if _, err := r1.AddCustomField(ctx, dbID, repo.CustomFieldDef{Name: "artist", Type: repo.CustomFieldTypeText}); !errors.Is(err, customerrors.ErrNotFound) {
			t.Fatalf("AddCustomField: expected ErrNotFound for 42P01, got %v", err)
		}

		r2, rec2 := newRecordingPostgresRepo(t)
		rec2.lockTableErr = undefinedErr
		if err := r2.DeleteCustomField(ctx, dbID, 0); !errors.Is(err, customerrors.ErrNotFound) {
			t.Fatalf("DeleteCustomField: expected ErrNotFound for 42P01, got %v", err)
		}

		r3, rec3 := newRecordingPostgresRepo(t)
		rec3.lockTableErr = undefinedErr
		enableIdx := true
		if _, err := r3.UpdateCustomField(ctx, dbID, 0, nil, &enableIdx); !errors.Is(err, customerrors.ErrNotFound) {
			t.Fatalf("UpdateCustomField: expected ErrNotFound for 42P01, got %v", err)
		}
	})
}

func TestPostgres_NewMigrationProvider(t *testing.T) {
	r, _ := newRecordingPostgresRepo(t)

	provider, err := r.newMigrationProvider()
	if err != nil {
		t.Fatalf("newMigrationProvider failed: %v", err)
	}
	if provider == nil {
		t.Fatal("expected non-nil goose provider")
	}

	sources := provider.ListSources()
	if len(sources) == 0 {
		t.Fatal("expected embedded postgres migrations to be discovered, got 0")
	}

	versions := make([]int64, 0, len(sources))
	for _, s := range sources {
		if s.Type != goose.TypeSQL {
			t.Errorf("expected SQL migration source, got %s (version %d)", s.Type, s.Version)
		}
		if s.Version < 3003 {
			t.Errorf("unexpected non-postgres migration version %d (%s)", s.Version, s.Path)
		}
		versions = append(versions, s.Version)
	}

	if versions[len(versions)-1] != int64(migrations.RequiredVersion) {
		t.Errorf("expected latest discovered postgres migration version %d, got %d (all: %v)", migrations.RequiredVersion, versions[len(versions)-1], versions)
	}
}

type pgHandlerFunc func(ctx context.Context, inTx bool, query string, args []driver.NamedValue) (driver.Rows, driver.Result, error)

type pgMockConnector struct {
	mu      sync.Mutex
	queries []lockRecQuery
	handler pgHandlerFunc
}

func (c *pgMockConnector) Connect(_ context.Context) (driver.Conn, error) {
	return &pgMockConn{c: c}, nil
}

func (c *pgMockConnector) Driver() driver.Driver { return &lockRecDriver{} }

type pgMockConn struct {
	c    *pgMockConnector
	inTx bool
}

func (m *pgMockConn) Prepare(_ string) (driver.Stmt, error) {
	return nil, errors.New("Prepare not implemented")
}
func (m *pgMockConn) Close() error              { return nil }
func (m *pgMockConn) Begin() (driver.Tx, error) { return m.BeginTx(context.Background(), driver.TxOptions{}) }
func (m *pgMockConn) BeginTx(_ context.Context, _ driver.TxOptions) (driver.Tx, error) {
	m.inTx = true
	return &pgMockTx{m: m}, nil
}
func (m *pgMockConn) CheckNamedValue(_ *driver.NamedValue) error { return nil }

func (m *pgMockConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	m.c.mu.Lock()
	m.c.queries = append(m.c.queries, lockRecQuery{sql: query, inTx: m.inTx})
	m.c.mu.Unlock()
	_, res, err := m.c.handler(ctx, m.inTx, query, args)
	if err != nil {
		return nil, err
	}
	if res != nil {
		return res, nil
	}
	return &lockRecResult{rowsAffected: 1}, nil
}

func (m *pgMockConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	m.c.mu.Lock()
	m.c.queries = append(m.c.queries, lockRecQuery{sql: query, inTx: m.inTx})
	m.c.mu.Unlock()
	rows, _, err := m.c.handler(ctx, m.inTx, query, args)
	return rows, err
}

type pgMockTx struct{ m *pgMockConn }

func (tx *pgMockTx) Commit() error {
	tx.m.inTx = false
	return nil
}
func (tx *pgMockTx) Rollback() error {
	tx.m.inTx = false
	return nil
}

func newMockPostgresRepo(t *testing.T, handler pgHandlerFunc) (*PostgresRepository, *pgMockConnector) {
	t.Helper()
	conn := &pgMockConnector{handler: handler}
	db := sql.OpenDB(conn)
	t.Cleanup(func() { _ = db.Close() })
	return &PostgresRepository{
		DB:              db,
		Builder:         squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
		AllowedStatuses: repo.GetAllEntryStatuses(),
	}, conn
}

func TestPostgres_LastAdminProtection(t *testing.T) {
	ctx := t.Context()
	admin1 := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNP01")
	admin2 := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNP02")
	member := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNP03")
	missing := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNP99")

	t.Run("DeleteUser rejects deleting sole admin with ErrConflict", func(t *testing.T) {
		r, conn := newMockPostgresRepo(t, func(_ context.Context, inTx bool, query string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
			if strings.Contains(query, "FOR UPDATE") {
				if !inTx {
					t.Errorf("expected FOR UPDATE inside transaction")
				}
				return &lockRecRows{
					cols: []string{"id", "is_admin"},
					data: [][]driver.Value{{admin1.String(), true}},
				}, nil, nil
			}
			return nil, nil, fmt.Errorf("unexpected query: %s", query)
		})

		err := r.DeleteUser(ctx, admin1)
		if !errors.Is(err, customerrors.ErrConflict) {
			t.Fatalf("expected ErrConflict, got: %v", err)
		}
		if len(conn.queries) != 1 || !strings.Contains(conn.queries[0].sql, "ORDER BY id FOR UPDATE") {
			t.Fatalf("expected single lock query before aborting, got: %+v", conn.queries)
		}
	})

	t.Run("DeleteUser succeeds when two admins exist", func(t *testing.T) {
		r, conn := newMockPostgresRepo(t, func(_ context.Context, inTx bool, query string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
			if strings.Contains(query, "FOR UPDATE") {
				return &lockRecRows{
					cols: []string{"id", "is_admin"},
					data: [][]driver.Value{
						{admin1.String(), true},
						{admin2.String(), true},
					},
				}, nil, nil
			}
			if strings.HasPrefix(query, "DELETE FROM users") && inTx {
				return nil, &lockRecResult{rowsAffected: 1}, nil
			}
			return nil, nil, fmt.Errorf("unexpected query: %s", query)
		})

		if err := r.DeleteUser(ctx, admin1); err != nil {
			t.Fatalf("expected DeleteUser with 2 admins to succeed, got: %v", err)
		}
		if len(conn.queries) != 2 {
			t.Fatalf("expected lock + delete queries, got: %+v", conn.queries)
		}
	})

	t.Run("DeleteUser returns ErrNotFound when user missing", func(t *testing.T) {
		r, _ := newMockPostgresRepo(t, func(_ context.Context, _ bool, query string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
			if strings.Contains(query, "FOR UPDATE") {
				return &lockRecRows{
					cols: []string{"id", "is_admin"},
					data: [][]driver.Value{{admin1.String(), true}},
				}, nil, nil
			}
			return nil, nil, fmt.Errorf("unexpected query: %s", query)
		})

		err := r.DeleteUser(ctx, missing)
		if !errors.Is(err, customerrors.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got: %v", err)
		}
	})

	t.Run("UpdateUser rejects demoting sole admin with ErrConflict", func(t *testing.T) {
		r, _ := newMockPostgresRepo(t, func(_ context.Context, _ bool, query string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
			if strings.Contains(query, "FOR UPDATE") {
				return &lockRecRows{
					cols: []string{"id", "is_admin"},
					data: [][]driver.Value{{admin1.String(), true}},
				}, nil, nil
			}
			return nil, nil, fmt.Errorf("unexpected query: %s", query)
		})

		_, err := r.UpdateUser(ctx, repo.User{
			ID:       admin1,
			Username: "admin",
			IsAdmin:  false,
		})
		if !errors.Is(err, customerrors.ErrConflict) {
			t.Fatalf("expected ErrConflict, got: %v", err)
		}
	})

	t.Run("UpdateUser maps unique violation to ErrUserExists and allows demoting when 2 admins", func(t *testing.T) {
		rConflict, _ := newMockPostgresRepo(t, func(_ context.Context, _ bool, query string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
			if strings.Contains(query, "FOR UPDATE") {
				return &lockRecRows{
					cols: []string{"id", "is_admin"},
					data: [][]driver.Value{
						{admin1.String(), true},
						{member.String(), false},
					},
				}, nil, nil
			}
			if strings.HasPrefix(query, "UPDATE users") {
				return nil, nil, &pq.Error{Code: "23505"}
			}
			return nil, nil, fmt.Errorf("unexpected query: %s", query)
		})

		if _, err := rConflict.UpdateUser(ctx, repo.User{ID: member, Username: "admin"}); !errors.Is(err, customerrors.ErrUserExists) {
			t.Fatalf("expected ErrUserExists, got: %v", err)
		}

		rDemote, _ := newMockPostgresRepo(t, func(_ context.Context, inTx bool, query string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
			if strings.Contains(query, "FOR UPDATE") {
				return &lockRecRows{
					cols: []string{"id", "is_admin"},
					data: [][]driver.Value{
						{admin1.String(), true},
						{admin2.String(), true},
					},
				}, nil, nil
			}
			if strings.HasPrefix(query, "UPDATE users") && inTx {
				return nil, &lockRecResult{rowsAffected: 1}, nil
			}
			return nil, nil, fmt.Errorf("unexpected query: %s", query)
		})

		if _, err := rDemote.UpdateUser(ctx, repo.User{ID: admin1, Username: "admin1", IsAdmin: false}); err != nil {
			t.Fatalf("expected demote with 2 admins to succeed, got: %v", err)
		}
	})
}

func TestPostgres_ConsumeRefreshToken(t *testing.T) {
	ctx := t.Context()
	userID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNP01")

	t.Run("valid token atomically deletes and returns user ID", func(t *testing.T) {
		r, conn := newMockPostgresRepo(t, func(_ context.Context, _ bool, query string, args []driver.NamedValue) (driver.Rows, driver.Result, error) {
			if !strings.Contains(query, "DELETE FROM refresh_tokens") || !strings.Contains(query, "clock_timestamp()") || !strings.Contains(query, "RETURNING user_id") {
				t.Fatalf("unexpected ConsumeRefreshToken SQL: %s", query)
			}
			if len(args) != 1 || args[0].Value != "hash-ok" {
				t.Fatalf("unexpected args: %+v", args)
			}
			return &lockRecRows{
				cols: []string{"user_id"},
				data: [][]driver.Value{{userID.String()}},
			}, nil, nil
		})

		got, err := r.ConsumeRefreshToken(ctx, "hash-ok")
		if err != nil {
			t.Fatalf("ConsumeRefreshToken failed: %v", err)
		}
		if got != userID {
			t.Errorf("expected %s, got %s", userID, got)
		}
		if len(conn.queries) != 1 {
			t.Errorf("expected single atomic SQL query, got %d", len(conn.queries))
		}
	})

	t.Run("missing or expired token returns ErrNotFound", func(t *testing.T) {
		r, _ := newMockPostgresRepo(t, func(_ context.Context, _ bool, _ string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
			return &lockRecRows{cols: []string{"user_id"}, data: nil}, nil, nil
		})

		_, err := r.ConsumeRefreshToken(ctx, "hash-missing")
		if !errors.Is(err, customerrors.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got: %v", err)
		}
	})
}

func TestPostgres_CreateOIDCUser_Hardening(t *testing.T) {
	ctx := t.Context()
	existingID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNP77")

	t.Run("escaped LIKE, server clock, and retry up to 5 attempts returning ErrUserExists", func(t *testing.T) {
		var attempts int
		r, conn := newMockPostgresRepo(t, func(_ context.Context, _ bool, query string, args []driver.NamedValue) (driver.Rows, driver.Result, error) {
			switch {
			case strings.Contains(query, "SELECT username FROM users"):
				if !strings.Contains(query, `LIKE $2 ESCAPE '\'`) {
					t.Errorf("expected escaped LIKE with $2 placeholder in Postgres query, got: %s", query)
				}
				if len(args) < 2 || args[1].Value != `user\_a\_%` {
					t.Errorf("expected escaped LIKE arg 'user\\_a\\_%%', got: %+v", args)
				}
				return &lockRecRows{cols: []string{"username"}, data: nil}, nil, nil
			case strings.HasPrefix(query, "INSERT INTO users"):
				attempts++
				return nil, nil, &pq.Error{Code: "23505"}
			default:
				return nil, nil, fmt.Errorf("unexpected query: %s", query)
			}
		})

		_, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
			Issuer:            "https://issuer",
			Subject:           "sub-1",
			PreferredUsername: "user_a",
		})
		if !errors.Is(err, customerrors.ErrUserExists) {
			t.Fatalf("expected ErrUserExists after exhausted retries, got: %v", err)
		}
		if attempts != maxCreateOIDCUserAttempts {
			t.Errorf("expected %d attempts, got %d", maxCreateOIDCUserAttempts, attempts)
		}
		_ = conn
	})

	t.Run("duplicate identity on user_oidc_identities falls back to GetUserByOIDCIdentity", func(t *testing.T) {
		r, _ := newMockPostgresRepo(t, func(_ context.Context, inTx bool, query string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
			switch {
			case strings.Contains(query, "SELECT username FROM users"):
				return &lockRecRows{cols: []string{"username"}, data: nil}, nil, nil
			case strings.HasPrefix(query, "INSERT INTO users"):
				return nil, &lockRecResult{rowsAffected: 1}, nil
			case strings.HasPrefix(query, "INSERT INTO user_oidc_identities"):
				if !strings.Contains(query, "clock_timestamp()") {
					t.Errorf("expected clock_timestamp() for created_at, got: %s", query)
				}
				return nil, nil, &pq.Error{Code: "23505"}
			case strings.Contains(query, "JOIN user_oidc_identities"):
				if inTx {
					t.Errorf("expected GetUserByOIDCIdentity outside rolled-back tx")
				}
				return &lockRecRows{
					cols: []string{"id", "username", "password_hash", "is_admin", "account_type"},
					data: [][]driver.Value{{existingID.String(), "user_a", "OIDC_ACCOUNT_NO_PASSWORD", false, int64(repo.AccountTypeOIDC)}},
				}, nil, nil
			default:
				return nil, nil, fmt.Errorf("unexpected query: %s", query)
			}
		})

		user, err := r.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
			Issuer:            "https://issuer",
			Subject:           "sub-dup",
			PreferredUsername: "user_a",
		})
		if err != nil {
			t.Fatalf("expected fallback to existing OIDC user, got: %v", err)
		}
		if user.ID != existingID || user.Username != "user_a" {
			t.Errorf("unexpected user returned: %+v", user)
		}
	})
}

type errNextRows struct {
	cols []string
	err  error
}

func (r *errNextRows) Columns() []string { return r.cols }
func (r *errNextRows) Close() error      { return nil }
func (r *errNextRows) Next(_ []driver.Value) error {
	return r.err
}

func TestPostgres_ScanEntryRow_PropagatesRowsError(t *testing.T) {
	ctx := t.Context()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	sentinelErr := errors.New("stream read aborted")

	r, _ := newMockPostgresRepo(t, func(_ context.Context, _ bool, query string, _ []driver.NamedValue) (driver.Rows, driver.Result, error) {
		if strings.Contains(query, "FROM database_custom_fields") {
			return &lockRecRows{cols: []string{"field_id", "name", "type", "is_indexed"}, data: nil}, nil, nil
		}
		return &errNextRows{cols: []string{"id"}, err: sentinelErr}, nil, nil
	})

	_, err := r.GetEntry(ctx, dbID, 1)
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("expected propagated stream error %v, got: %v", sentinelErr, err)
	}
	if errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected error not to be masked as ErrNotFound")
	}
}
