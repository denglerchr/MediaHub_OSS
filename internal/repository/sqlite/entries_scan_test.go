package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"

	"github.com/Masterminds/squirrel"
	"github.com/patrickmn/go-cache"
)

type scanErrDriver struct{}

func (d *scanErrDriver) Open(_ string) (driver.Conn, error) {
	return nil, errors.New("not used")
}

type scanErrConnector struct {
	err error
}

func (c *scanErrConnector) Connect(_ context.Context) (driver.Conn, error) {
	return &scanErrConn{err: c.err}, nil
}

func (c *scanErrConnector) Driver() driver.Driver { return &scanErrDriver{} }

type scanErrConn struct {
	err error
}

func (c *scanErrConn) Prepare(_ string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (c *scanErrConn) Close() error                          { return nil }
func (c *scanErrConn) Begin() (driver.Tx, error)             { return nil, errors.New("not used") }
func (c *scanErrConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "FROM database_custom_fields") {
		return &scanErrRows{cols: []string{"field_id", "name", "type", "is_indexed"}, eof: true}, nil
	}
	return &scanErrRows{cols: []string{"id"}, err: c.err}, nil
}

type scanErrRows struct {
	cols []string
	err  error
	eof  bool
}

func (r *scanErrRows) Columns() []string { return r.cols }
func (r *scanErrRows) Close() error      { return nil }
func (r *scanErrRows) Next(_ []driver.Value) error {
	if r.eof {
		return io.EOF
	}
	return r.err
}

func TestSQLite_ScanEntryRow_PropagatesRowsError(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	sentinelErr := context.Canceled

	db := sql.OpenDB(&scanErrConnector{err: sentinelErr})
	defer db.Close()

	r := &SQLiteRepository{
		DB:              db,
		Cache:           cache.New(0, 0),
		Builder:         squirrel.StatementBuilder.PlaceholderFormat(squirrel.Question),
		AllowedStatuses: repo.GetAllEntryStatuses(),
	}

	_, err := r.GetEntry(ctx, dbID, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled to propagate from scanEntryRow, got: %v", err)
	}
	if errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected error not to be masked as ErrNotFound")
	}
}
