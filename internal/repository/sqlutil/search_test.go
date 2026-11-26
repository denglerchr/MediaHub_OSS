package sqlutil_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/Masterminds/squirrel"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlutil"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
)

func TestBuildSearchQuery_StatusField(t *testing.T) {
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	dialect := sqlutil.SearchDialect{}

	t.Run("string status values", func(t *testing.T) {
		cases := []struct {
			input    string
			op       string
			expected uint8
		}{
			{"ready", "=", uint8(repo.EntryStatusReady)},
			{"PROCESSING", "=", uint8(repo.EntryStatusProcessing)},
			{" Error ", "!=", uint8(repo.EntryStatusError)},
			{"deleting", "=", uint8(repo.EntryStatusDeleting)},
			{"Queued", "!=", uint8(repo.EntryStatusQueued)},
		}

		for _, tc := range cases {
			req := repo.SearchRequest{
				Filter: &repo.FilterGroup{
					Operator: "and",
					Conditions: []repo.Condition{
						{Field: "status", Operator: tc.op, Value: tc.input},
					},
				},
			}

			query, args, err := sqlutil.BuildSearchQuery(
				squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
				dbID,
				req,
				nil,
				dialect,
			)
			if err != nil {
				t.Fatalf("unexpected error for %q %s: %v", tc.input, tc.op, err)
			}
			wantClause := `"status" ` + tc.op + ` $1`
			if !strings.Contains(query, wantClause) {
				t.Fatalf("expected query %q to contain %q", query, wantClause)
			}
			if len(args) != 1 || args[0] != tc.expected {
				t.Fatalf("expected args [%d], got %#v", tc.expected, args)
			}
		}
	})

	t.Run("numeric status values", func(t *testing.T) {
		cases := []struct {
			name     string
			value    any
			expected uint8
		}{
			{"float64", float64(0), 0},
			{"int", int(1), 1},
			{"int64", int64(2), 2},
			{"uint8", uint8(3), 3},
			{"EntryStatus", repo.EntryStatusQueued, 4},
		}

		for _, tc := range cases {
			req := repo.SearchRequest{
				Filter: &repo.FilterGroup{
					Operator: "and",
					Conditions: []repo.Condition{
						{Field: "status", Operator: "=", Value: tc.value},
					},
				},
			}

			_, args, err := sqlutil.BuildSearchQuery(squirrel.StatementBuilder, dbID, req, nil, dialect)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			if len(args) != 1 || args[0] != tc.expected {
				t.Fatalf("%s: expected args [%d], got %#v", tc.name, tc.expected, args)
			}
		}
	})

	t.Run("invalid values and disallowed operators", func(t *testing.T) {
		cases := []struct {
			name string
			op   string
			val  any
		}{
			{"invalid string", "=", "unknown"},
			{"empty string", "=", ""},
			{"deleted string", "=", "deleted"},
			{"negative int", "=", -1},
			{"out of range int", "=", 5},
			{"out of range float", "=", float64(99)},
			{"fractional float", "=", float64(1.5)},
			{"NaN float", "=", math.NaN()},
			{"Inf float", "=", math.Inf(1)},
			{"boolean value", "=", true},
			{"nil value", "=", nil},
			{"LIKE operator", "LIKE", "ready"},
			{"ILIKE operator", "ILIKE", "ready"},
			{"gt operator", ">", "ready"},
			{"gte operator", ">=", 0},
			{"lt operator", "<", 4},
			{"lte operator", "<=", 4},
			{"in_box operator", "in_box", "ready"},
		}

		for _, tc := range cases {
			req := repo.SearchRequest{
				Filter: &repo.FilterGroup{
					Operator: "and",
					Conditions: []repo.Condition{
						{Field: "status", Operator: tc.op, Value: tc.val},
					},
				},
			}
			if _, _, err := sqlutil.BuildSearchQuery(squirrel.StatementBuilder, dbID, req, nil, dialect); !errors.Is(err, customerrors.ErrValidation) {
				t.Errorf("%s: expected ErrValidation, got %v", tc.name, err)
			}
		}
	})
}
