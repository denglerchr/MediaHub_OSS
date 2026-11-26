package sqlutil_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlutil"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
)

type stubRowScanner struct {
	vals []any
	err  error
}

func (s *stubRowScanner) Scan(dest ...any) error {
	if s.err != nil {
		return s.err
	}
	if len(dest) != len(s.vals) {
		return fmt.Errorf("column count mismatch: want %d, got %d", len(s.vals), len(dest))
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *repo.ULID:
			if err := d.Scan(s.vals[i]); err != nil {
				return err
			}
		case *string:
			*d = s.vals[i].(string)
		case *bool:
			*d = s.vals[i].(bool)
		case *int64:
			*d = s.vals[i].(int64)
		case *uint8:
			*d = s.vals[i].(uint8)
		case *sql.NullInt64:
			*d = s.vals[i].(sql.NullInt64)
		default:
			return fmt.Errorf("unsupported dest type %T", dest[i])
		}
	}
	return nil
}

func TestAPIKeySelectColumns(t *testing.T) {
	unaliased := sqlutil.APIKeySelectColumns("")
	if len(unaliased) != 13 || unaliased[0] != "id" || unaliased[12] != "last_used_at" {
		t.Fatalf("unexpected unaliased columns: %v", unaliased)
	}

	aliased := sqlutil.APIKeySelectColumns("ak")
	if len(aliased) != 13 || aliased[0] != "ak.id" || aliased[12] != "ak.last_used_at" {
		t.Fatalf("unexpected aliased columns: %v", aliased)
	}
}

func TestScanAPIKeyAndWithOwner(t *testing.T) {
	createdMs := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	expiresMs := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC).UnixMilli()

	keyScanner := &stubRowScanner{
		vals: []any{
			"01HGFB9Z5W7ABCDEFGHJKMNPQR",
			"01HGFB9Z5W7ABCDEFGHJKMNPQS",
			"ci-token",
			"hashval",
			"srv_...1234",
			true, false, true, false, false,
			createdMs,
			sql.NullInt64{Int64: expiresMs, Valid: true},
			sql.NullInt64{Valid: false},
		},
	}

	key, err := sqlutil.ScanAPIKey(keyScanner)
	if err != nil {
		t.Fatalf("ScanAPIKey failed: %v", err)
	}
	if key.ID != ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR") || key.UserID != ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQS") {
		t.Fatalf("unexpected IDs: %+v", key)
	}
	if !key.Scope.HasAccess(repo.AccessView|repo.AccessEdit) || key.Scope.HasAccess(repo.AccessCreate) || key.Scope.HasAccess(repo.AccessAdmin) {
		t.Fatalf("unexpected Scope: %v", key.Scope)
	}
	if key.CreatedAt.UnixMilli() != createdMs || key.ExpiresAt.UnixMilli() != expiresMs || !key.LastUsedAt.IsZero() {
		t.Fatalf("unexpected timestamps: %+v", key)
	}

	ownerScanner := &stubRowScanner{
		vals: append(slices.Clone(keyScanner.vals),
			"01HGFB9Z5W7ABCDEFGHJKMNPQS",
			"svc_user",
			"SERVICE_ACCOUNT_NO_LOGIN",
			false,
			uint8(repo.AccountTypeService),
		),
	}

	gotKey, gotUser, err := sqlutil.ScanAPIKeyWithOwner(ownerScanner)
	if err != nil {
		t.Fatalf("ScanAPIKeyWithOwner failed: %v", err)
	}
	if gotKey.ID != key.ID || gotUser.Username != "svc_user" || gotUser.AccountType != repo.AccountTypeService {
		t.Fatalf("unexpected key/user result: key=%+v user=%+v", gotKey, gotUser)
	}
}

var errStubIteration = errors.New("simulated iteration error")

type funcConnector struct {
	openFn func() (driver.Conn, error)
}

func (f *funcConnector) Connect(context.Context) (driver.Conn, error) { return f.openFn() }
func (f *funcConnector) Driver() driver.Driver                        { return nil }

type stubRowsIter struct {
	pos  int
	rows [][]driver.Value
	err  error
}

type stubQueryConn struct {
	iter driver.Rows
}

func (c *stubQueryConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *stubQueryConn) Close() error                        { return nil }
func (c *stubQueryConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *stubQueryConn) Query(string, []driver.Value) (driver.Rows, error) {
	return c.iter, nil
}

func (r *stubRowsIter) Columns() []string { return sqlutil.APIKeySelectColumns("") }
func (r *stubRowsIter) Close() error      { return nil }
func (r *stubRowsIter) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		if r.err != nil {
			return r.err
		}
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

func TestScanAPIKeyRows_PropagatesRowIterationError(t *testing.T) {
	// Exercise ScanAPIKeyRows directly through database/sql with a minimal driver
	// that returns an iteration error after one row.
	db := sql.OpenDB(&funcConnector{
		openFn: func() (driver.Conn, error) {
			return &stubQueryConn{
				iter: &stubRowsIter{
					rows: [][]driver.Value{{
						"01HGFB9Z5W7ABCDEFGHJKMNPQR",
						"01HGFB9Z5W7ABCDEFGHJKMNPQS",
						"k1", "h1", "srv_...1",
						true, false, false, false, false,
						int64(1000), nil, nil,
					}},
					err: errStubIteration,
				},
			}, nil
		},
	})
	defer db.Close()

	rows, err := db.Query("SELECT 1")
	if err != nil {
		t.Fatalf("db.Query failed: %v", err)
	}
	defer rows.Close()

	_, err = sqlutil.ScanAPIKeyRows(rows)
	if !errors.Is(err, errStubIteration) {
		t.Fatalf("expected wrapped errStubIteration, got %v", err)
	}
}

func TestIsValidSearchOperator(t *testing.T) {
	for _, op := range []string{"=", "!=", ">", ">=", "<", "<=", "LIKE", "ILIKE", "like", "ilike"} {
		if !sqlutil.IsValidSearchOperator(op) {
			t.Errorf("expected operator %q to be valid", op)
		}
	}
	for _, op := range []string{"DROP", "IN", "in_box", ""} {
		if sqlutil.IsValidSearchOperator(op) {
			t.Errorf("expected operator %q to be invalid", op)
		}
	}
}

func TestBuildSearchQuery_PostgresAndSQLiteDialects(t *testing.T) {
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	cfs := []repo.CustomFieldDef{
		{ID: 3, Name: "title", Type: repo.CustomFieldTypeText},
		{ID: 7, Name: "location", Type: repo.CustomFieldTypeCoordinate},
	}
	mediaAllowed := func(f string) bool { return f == "width" }

	pgDialect := sqlutil.SearchDialect{
		CustomFieldsPrefix: "cf_",
		MediaFieldAllowed:  mediaAllowed,
		CoordBoxExpr: func(cf repo.CustomFieldDef, box repo.BoundingBox) squirrel.Sqlizer {
			if box.MinLng <= box.MaxLng {
				return squirrel.Expr(
					fmt.Sprintf(`"cf_%d" <@ box(point(?, ?), point(?, ?))`, cf.ID),
					box.MinLng, box.MinLat, box.MaxLng, box.MaxLat,
				)
			}
			return squirrel.Expr(
				fmt.Sprintf(`("cf_%d" <@ box(point(?, ?), point(?, ?)) OR "cf_%d" <@ box(point(?, ?), point(?, ?)))`, cf.ID, cf.ID),
				box.MinLng, box.MinLat, 180.0, box.MaxLat, -180.0, box.MinLat, box.MaxLng, box.MaxLat,
			)
		},
	}

	sqliteDialect := sqlutil.SearchDialect{
		CustomFieldsPrefix: "cf_",
		MediaFieldAllowed:  mediaAllowed,
		FormatOperator: func(op string) string {
			u := strings.ToUpper(op)
			if u == "ILIKE" {
				return "LIKE"
			}
			return u
		},
		CoordBoxExpr: func(cf repo.CustomFieldDef, box repo.BoundingBox) squirrel.Sqlizer {
			if box.MinLng <= box.MaxLng {
				return squirrel.Expr(
					fmt.Sprintf(`"cf_%d_lat" >= ? AND "cf_%d_lat" <= ? AND "cf_%d_lng" >= ? AND "cf_%d_lng" <= ?`, cf.ID, cf.ID, cf.ID, cf.ID),
					box.MinLat, box.MaxLat, box.MinLng, box.MaxLng,
				)
			}
			return squirrel.Expr(
				fmt.Sprintf(`"cf_%d_lat" >= ? AND "cf_%d_lat" <= ? AND ("cf_%d_lng" >= ? OR "cf_%d_lng" <= ?)`, cf.ID, cf.ID, cf.ID, cf.ID),
				box.MinLat, box.MaxLat, box.MinLng, box.MaxLng,
			)
		},
	}

	req := repo.SearchRequest{
		Filter: &repo.FilterGroup{
			Operator: "or",
			Conditions: []repo.Condition{
				{Field: "title", Operator: "ilike", Value: "%sun%"},
				{Field: "width", Operator: ">=", Value: 800},
				{Field: "location", Operator: "in_box", Value: repo.BoundingBox{MinLat: 10, MaxLat: 20, MinLng: 170, MaxLng: -170}},
			},
		},
		Sort:       &repo.SortCriteria{Field: "width", Direction: "asc"},
		Pagination: repo.Pagination{Limit: 25, Offset: 50},
	}

	pgQuery, pgArgs, err := sqlutil.BuildSearchQuery(squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar), dbID, req, cfs, pgDialect)
	if err != nil {
		t.Fatalf("Postgres BuildSearchQuery failed: %v", err)
	}
	if !strings.Contains(pgQuery, `"cf_3" ILIKE $1`) || !strings.Contains(pgQuery, `"width" >= $2`) || !strings.Contains(pgQuery, `point($9, $10)`) || !strings.Contains(pgQuery, `ORDER BY "width" ASC LIMIT 25 OFFSET 50`) {
		t.Fatalf("unexpected Postgres query: %s", pgQuery)
	}
	if len(pgArgs) != 10 {
		t.Fatalf("expected 10 args for antimeridian PG search, got %d", len(pgArgs))
	}

	sqQuery, sqArgs, err := sqlutil.BuildSearchQuery(squirrel.StatementBuilder.PlaceholderFormat(squirrel.Question), dbID, req, cfs, sqliteDialect)
	if err != nil {
		t.Fatalf("SQLite BuildSearchQuery failed: %v", err)
	}
	if !strings.Contains(sqQuery, `"cf_3" LIKE ?`) || !strings.Contains(sqQuery, `"cf_7_lat" >= ? AND "cf_7_lat" <= ? AND ("cf_7_lng" >= ? OR "cf_7_lng" <= ?)`) {
		t.Fatalf("unexpected SQLite query: %s", sqQuery)
	}
	if len(sqArgs) != 6 {
		t.Fatalf("expected 6 args for antimeridian SQLite search, got %d", len(sqArgs))
	}

	// Validation error cases
	invalidReqs := []struct {
		name string
		dbID repo.ULID
		req  repo.SearchRequest
	}{
		{"invalid dbID", repo.ULID{}, repo.SearchRequest{}},
		{"scalar op on coordinate", dbID, repo.SearchRequest{Filter: &repo.FilterGroup{Conditions: []repo.Condition{{Field: "location", Operator: "=", Value: "x"}}}}},
		{"in_box on non-coordinate", dbID, repo.SearchRequest{Filter: &repo.FilterGroup{Conditions: []repo.Condition{{Field: "title", Operator: "in_box", Value: "x"}}}}},
		{"unknown field", dbID, repo.SearchRequest{Filter: &repo.FilterGroup{Conditions: []repo.Condition{{Field: "unknown_col", Operator: "=", Value: 1}}}}},
		{"invalid operator", dbID, repo.SearchRequest{Filter: &repo.FilterGroup{Conditions: []repo.Condition{{Field: "title", Operator: "DROP", Value: 1}}}}},
		{"sort by coordinate", dbID, repo.SearchRequest{Sort: &repo.SortCriteria{Field: "location", Direction: "asc"}}},
	}

	for _, tc := range invalidReqs {
		if _, _, err := sqlutil.BuildSearchQuery(squirrel.StatementBuilder, tc.dbID, tc.req, cfs, pgDialect); !errors.Is(err, customerrors.ErrValidation) {
			t.Errorf("%s: expected ErrValidation, got %v", tc.name, err)
		}
	}
}
