package postgres

import (
	"database/sql"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
)

type entryScanner struct {
	cols           []string
	colVals        []any
	columnPointers []any
	cleanNames     []string
	isCustom       []bool
	isCoord        []bool
}

func newEntryScanner(rows *sql.Rows, customFields []repo.CustomFieldDef) (entryScanner, error) {
	cols, err := rows.Columns()
	if err != nil {
		return entryScanner{}, err
	}

	cfMap := make(map[string]repo.CustomFieldDef)
	for _, cf := range customFields {
		cfMap[fmt.Sprintf("%s%d", customFieldsPrefix, cf.ID)] = cf
	}

	size := len(cols)
	s := entryScanner{
		cols:           cols,
		colVals:        make([]any, size),
		columnPointers: make([]any, size),
		cleanNames:     make([]string, size),
		isCustom:       make([]bool, size),
		isCoord:        make([]bool, size),
	}

	for i, colName := range cols {
		s.columnPointers[i] = &s.colVals[i]

		if strings.HasPrefix(colName, customFieldsPrefix) {
			s.isCustom[i] = true
			if cf, ok := cfMap[colName]; ok {
				s.cleanNames[i] = cf.Name
				if cf.Type.IsCoordinate() {
					s.isCoord[i] = true
				}
			} else {
				s.cleanNames[i] = strings.TrimPrefix(colName, customFieldsPrefix)
			}
		} else {
			s.isCustom[i] = false
			s.cleanNames[i] = colName
		}
	}

	return s, nil
}

func (s entryScanner) scan(rows *sql.Rows) (repo.Entry, error) {
	if err := rows.Scan(s.columnPointers...); err != nil {
		return repo.Entry{}, err
	}

	entry := repo.Entry{
		MediaFields:  make(map[string]any),
		CustomFields: make(map[string]any),
	}

	for i, colName := range s.cols {
		val := s.colVals[i]
		if val == nil {
			continue
		}

		switch colName {
		case "id":
			entry.ID = asInt64(val)
		case "timestamp":
			tsMs := asInt64(val)
			entry.Timestamp = time.UnixMilli(tsMs)
		case "created_at":
			tsMs := asInt64(val)
			if tsMs > 0 {
				entry.CreatedAt = time.UnixMilli(tsMs)
			}
		case "updated_at":
			tsMs := asInt64(val)
			if tsMs > 0 {
				entry.UpdatedAt = time.UnixMilli(tsMs)
			}
		case "filesize":
			entry.Size = uint64(asInt64(val))
		case "preview_filesize":
			entry.PreviewSize = uint64(asInt64(val))
		case "filename":
			entry.FileName = asString(val)
		case "status":
			entry.Status = repo.EntryStatus(asInt64(val))
		case "mime_type":
			entry.MimeType = asString(val)
		default:
			if s.isCustom[i] {
				if s.isCoord[i] {
					str := asString(val)
					str = strings.Trim(str, "()")
					parts := strings.Split(str, ",")
					if len(parts) == 2 {
						lng, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
						lat, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
						if err1 == nil && err2 == nil {
							entry.CustomFields[s.cleanNames[i]] = repo.Coordinate{
								Latitude:  lat,
								Longitude: lng,
							}
						}
					}
				} else {
					if b, ok := val.([]byte); ok {
						val = string(b)
					}
					entry.CustomFields[s.cleanNames[i]] = val
				}
			} else {
				if b, ok := val.([]byte); ok {
					val = string(b)
				}
				entry.MediaFields[s.cleanNames[i]] = val
			}
		}
	}

	return entry, nil
}

func asInt64(val any) int64 {
	switch v := val.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int16:
		return int64(v)
	case int8:
		return int64(v)
	case uint64:
		return int64(v)
	case uint:
		return int64(v)
	case uint32:
		return int64(v)
	case uint16:
		return int64(v)
	case uint8:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case []byte:
		parsed, _ := strconv.ParseInt(string(v), 10, 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(v, 10, 64)
		return parsed
	}
	return 0
}

func asString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func (r *PostgresRepository) scanEntryRow(rows *sql.Rows, customFields []repo.CustomFieldDef) (repo.Entry, error) {
	scanner, err := newEntryScanner(rows, customFields)
	if err != nil {
		return repo.Entry{}, err
	}

	if !rows.Next() {
		return repo.Entry{}, customerrors.ErrNotFound
	}

	return scanner.scan(rows)
}

func (r *PostgresRepository) scanEntryRows(rows *sql.Rows, customFields []repo.CustomFieldDef) ([]repo.Entry, error) {
	scanner, err := newEntryScanner(rows, customFields)
	if err != nil {
		return nil, err
	}

	var entries []repo.Entry
	for rows.Next() {
		entry, err := scanner.scan(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return entries, nil
}

func (r *PostgresRepository) validateAndFormatSearchField(field string, customFields []repo.CustomFieldDef) (string, error) {
	standardFields := map[string]bool{
		"id": true, "timestamp": true, "created_at": true, "updated_at": true,
		"filesize": true, "preview_filesize": true, "filename": true, "status": true, "mime_type": true,
	}
	if standardFields[field] {
		return fmt.Sprintf(`"%s"`, field), nil
	}

	for _, fields := range r.MediaFields {
		for _, mediaField := range fields {
			if mediaField.Name == field {
				return fmt.Sprintf(`"%s"`, field), nil
			}
		}
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

// mapCustomFieldsToPostgresColumns maps entry custom fields into the target SQL column-data map.
func mapCustomFieldsToPostgresColumns(
	customFields []repo.CustomFieldDef,
	entryCustomFields map[string]any,
	target map[string]any,
) error {
	cfMap := make(map[string]repo.CustomFieldDef, len(customFields)*3)
	for _, cf := range customFields {
		cfMap[cf.Name] = cf
		cfMap[fmt.Sprintf("%s%d", customFieldsPrefix, cf.ID)] = cf
		cfMap[fmt.Sprintf("%d", cf.ID)] = cf
	}
	for key, value := range entryCustomFields {
		if cf, ok := cfMap[key]; ok {
			if cf.Type.IsCoordinate() {
				if value == nil {
					target[fmt.Sprintf("%s%d", customFieldsPrefix, cf.ID)] = nil
				} else {
					coord, err := repo.ParseCoordinate(value)
					if err != nil {
						return fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
					}
					target[fmt.Sprintf("%s%d", customFieldsPrefix, cf.ID)] = squirrel.Expr("point(?, ?)", coord.Longitude, coord.Latitude)
				}
			} else {
				target[fmt.Sprintf("%s%d", customFieldsPrefix, cf.ID)] = value
			}
		} else {
			target[customFieldsPrefix+key] = value
		}
	}
	return nil
}

func isValidOperator(op string) bool {
	valid := map[string]bool{
		"=": true, "!=": true, ">": true, ">=": true, "<": true, "<=": true, "LIKE": true, "ILIKE": true,
	}
	return valid[strings.ToUpper(op)]
}

