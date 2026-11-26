package sqlutil

import (
	"fmt"
	"strings"

	"github.com/Masterminds/squirrel"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

var standardSearchFields = map[string]bool{
	"id": true, "timestamp": true, "created_at": true, "updated_at": true,
	"filesize": true, "preview_filesize": true, "filename": true, "status": true, "mime_type": true,
}

var validSearchOperators = map[string]bool{
	"=": true, "!=": true, ">": true, ">=": true, "<": true, "<=": true, "LIKE": true, "ILIKE": true,
}

// SearchDialect configures driver-specific SQL rendering differences for BuildSearchQuery.
type SearchDialect struct {
	CustomFieldsPrefix string
	MediaFieldAllowed  func(field string) bool
	CoordBoxExpr       func(cf repo.CustomFieldDef, box repo.BoundingBox) squirrel.Sqlizer
	FormatOperator     func(op string) string // optional; defaults to strings.ToUpper(op)
}

// IsValidSearchOperator reports whether op is an allowed scalar entry search operator.
func IsValidSearchOperator(op string) bool {
	return validSearchOperators[strings.ToUpper(op)]
}

// ValidateAndFormatSearchField validates field against standard, media, and non-coordinate
// custom fields and returns its safely double-quoted SQL column identifier.
func ValidateAndFormatSearchField(
	field string,
	mediaFieldAllowed func(string) bool,
	customFields []repo.CustomFieldDef,
	customFieldsPrefix string,
) (string, error) {
	if standardSearchFields[field] {
		return fmt.Sprintf(`"%s"`, field), nil
	}
	if mediaFieldAllowed != nil && mediaFieldAllowed(field) {
		return fmt.Sprintf(`"%s"`, field), nil
	}
	for _, cf := range customFields {
		if cf.Name == field {
			if cf.Type.IsCoordinate() {
				return "", fmt.Errorf("field '%s' is a coordinate field and cannot be used with scalar operators", field)
			}
			return fmt.Sprintf(`"%s%d"`, customFieldsPrefix, cf.ID), nil
		}
	}
	return "", fmt.Errorf("field '%s' is not allowed or does not exist", field)
}

// BuildSearchQuery constructs a parameterized SQL query and arguments for entry searches.
func BuildSearchQuery(
	builder squirrel.StatementBuilderType,
	dbID repo.ULID,
	req repo.SearchRequest,
	customFields []repo.CustomFieldDef,
	dialect SearchDialect,
) (string, []any, error) {
	if dbID.IsZero() {
		return "", nil, fmt.Errorf("%w: invalid database id", customerrors.ErrValidation)
	}

	tableName := fmt.Sprintf(`"entries_%s"`, dbID.String())
	b := builder.Select("*").From(tableName)

	if req.Filter != nil && len(req.Filter.Conditions) > 0 {
		whereExpr, err := buildSearchFilter(req.Filter, customFields, dialect)
		if err != nil {
			return "", nil, err
		}
		b = b.Where(whereExpr)
	}

	orderBy, err := buildSearchOrderBy(req.Sort, customFields, dialect)
	if err != nil {
		return "", nil, err
	}
	b = b.OrderBy(orderBy)

	if req.Pagination.Limit > 0 {
		b = b.Limit(uint64(req.Pagination.Limit))
	}
	if req.Pagination.Offset > 0 {
		b = b.Offset(uint64(req.Pagination.Offset))
	}

	query, args, err := b.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("failed to build search query: %w", err)
	}
	return query, args, nil
}

func buildSearchFilter(
	filter *repo.FilterGroup,
	customFields []repo.CustomFieldDef,
	dialect SearchDialect,
) (squirrel.Sqlizer, error) {
	isOr := strings.ToLower(filter.Operator) == "or"
	exprs := make([]squirrel.Sqlizer, 0, len(filter.Conditions))

	for _, cond := range filter.Conditions {
		expr, err := buildConditionExpr(cond, customFields, dialect)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
	}

	if isOr {
		return squirrel.Or(exprs), nil
	}
	return squirrel.And(exprs), nil
}

func buildConditionExpr(
	cond repo.Condition,
	customFields []repo.CustomFieldDef,
	dialect SearchDialect,
) (squirrel.Sqlizer, error) {
	var matchedCF *repo.CustomFieldDef
	for i := range customFields {
		if customFields[i].Name == cond.Field {
			matchedCF = &customFields[i]
			break
		}
	}

	if matchedCF != nil && matchedCF.Type.IsCoordinate() {
		if strings.ToLower(cond.Operator) != "in_box" {
			return nil, fmt.Errorf("%w: operator '%s' is not supported on coordinate fields; only 'in_box' is allowed", customerrors.ErrValidation, cond.Operator)
		}
		box, err := repo.ParseBoundingBox(cond.Value)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
		}
		if dialect.CoordBoxExpr == nil {
			return nil, fmt.Errorf("missing coordinate bounding-box dialect builder")
		}
		return dialect.CoordBoxExpr(*matchedCF, box), nil
	}

	if strings.ToLower(cond.Operator) == "in_box" {
		return nil, fmt.Errorf("%w: 'in_box' operator is only supported on coordinate fields", customerrors.ErrValidation)
	}

	safeField, err := ValidateAndFormatSearchField(cond.Field, dialect.MediaFieldAllowed, customFields, dialect.CustomFieldsPrefix)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
	}

	if !IsValidSearchOperator(cond.Operator) {
		return nil, fmt.Errorf("%w: invalid operator '%s'", customerrors.ErrValidation, cond.Operator)
	}

	sqlOp := strings.ToUpper(cond.Operator)
	if dialect.FormatOperator != nil {
		sqlOp = dialect.FormatOperator(cond.Operator)
	}

	return squirrel.Expr(fmt.Sprintf("%s %s ?", safeField, sqlOp), cond.Value), nil
}

func buildSearchOrderBy(
	sort *repo.SortCriteria,
	customFields []repo.CustomFieldDef,
	dialect SearchDialect,
) (string, error) {
	if sort == nil || sort.Field == "" {
		return "timestamp DESC", nil
	}

	for _, cf := range customFields {
		if cf.Name == sort.Field && cf.Type.IsCoordinate() {
			return "", fmt.Errorf("%w: sorting by coordinate fields is not supported", customerrors.ErrValidation)
		}
	}

	safeField, err := ValidateAndFormatSearchField(sort.Field, dialect.MediaFieldAllowed, customFields, dialect.CustomFieldsPrefix)
	if err != nil {
		return "", fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
	}

	dir := "DESC"
	if strings.ToLower(sort.Direction) == "asc" {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s", safeField, dir), nil
}
