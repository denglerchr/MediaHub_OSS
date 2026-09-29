package sqlite

import (
	"database/sql"
	"fmt"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"strconv"
	"strings"
	"time"
)

type colKind int

const (
	colKindStandardMedia colKind = iota
	colKindCustomScalar
	colKindCustomCoordLat
	colKindCustomCoordLng
)

// entryScanner holds pre-allocated slices and pre-computed field names
type entryScanner struct {
	cols           []string
	colVals        []any
	columnPointers []any
	cleanNames     []string // Pre-trimmed names for Custom/Media fields
	colKinds       []colKind
}

// newEntryScanner initializes the scanner once per query result.
func newEntryScanner(rows *sql.Rows, customFields []repo.CustomFieldDef) (entryScanner, error) {
	cols, err := rows.Columns()
	if err != nil {
		return entryScanner{}, err
	}

	scalarMap := make(map[string]string)
	coordLatMap := make(map[string]string)
	coordLngMap := make(map[string]string)

	for _, cf := range customFields {
		if cf.Type.IsCoordinate() {
			coordLatMap[fmt.Sprintf("%s%d_lat", customFieldsPrefix, cf.ID)] = cf.Name
			coordLngMap[fmt.Sprintf("%s%d_lng", customFieldsPrefix, cf.ID)] = cf.Name
		} else {
			scalarMap[fmt.Sprintf("%s%d", customFieldsPrefix, cf.ID)] = cf.Name
		}
	}

	size := len(cols)
	s := entryScanner{
		cols:           cols,
		colVals:        make([]any, size),
		columnPointers: make([]any, size),
		cleanNames:     make([]string, size),
		colKinds:       make([]colKind, size),
	}

	for i, colName := range cols {
		s.columnPointers[i] = &s.colVals[i]

		if name, ok := coordLatMap[colName]; ok {
			s.colKinds[i] = colKindCustomCoordLat
			s.cleanNames[i] = name
		} else if name, ok := coordLngMap[colName]; ok {
			s.colKinds[i] = colKindCustomCoordLng
			s.cleanNames[i] = name
		} else if name, ok := scalarMap[colName]; ok {
			s.colKinds[i] = colKindCustomScalar
			s.cleanNames[i] = name
		} else if strings.HasPrefix(colName, customFieldsPrefix) {
			s.colKinds[i] = colKindCustomScalar
			s.cleanNames[i] = strings.TrimPrefix(colName, customFieldsPrefix)
		} else {
			s.colKinds[i] = colKindStandardMedia
			s.cleanNames[i] = colName
		}
	}

	return s, nil
}

// scan maps the current row into a repo.Entry, reusing the allocated memory.
func (s entryScanner) scan(rows *sql.Rows) (repo.Entry, error) {
	if err := rows.Scan(s.columnPointers...); err != nil {
		return repo.Entry{}, err
	}

	entry := repo.Entry{
		MediaFields:  make(map[string]any),
		CustomFields: make(map[string]any),
	}

	var coordLats map[string]float64
	var coordLngs map[string]float64
	var coordHasLat map[string]bool
	var coordHasLng map[string]bool

	for i, colName := range s.cols {
		val := s.colVals[i]
		if val == nil {
			continue
		}

		switch s.colKinds[i] {
		case colKindCustomCoordLat:
			if coordLats == nil {
				coordLats = make(map[string]float64)
				coordHasLat = make(map[string]bool)
			}
			coordLats[s.cleanNames[i]] = asFloat64(val)
			coordHasLat[s.cleanNames[i]] = true
		case colKindCustomCoordLng:
			if coordLngs == nil {
				coordLngs = make(map[string]float64)
				coordHasLng = make(map[string]bool)
			}
			coordLngs[s.cleanNames[i]] = asFloat64(val)
			coordHasLng[s.cleanNames[i]] = true
		case colKindCustomScalar:
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			entry.CustomFields[s.cleanNames[i]] = val
		default:
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
				// We MUST convert []byte to string here to prevent Base64 JSON encoding!
				if b, ok := val.([]byte); ok {
					val = string(b)
				}
				entry.MediaFields[s.cleanNames[i]] = val
			}
		}
	}

	for name, hasLat := range coordHasLat {
		if hasLat && coordHasLng[name] {
			entry.CustomFields[name] = repo.Coordinate{
				Latitude:  coordLats[name],
				Longitude: coordLngs[name],
			}
		}
	}

	return entry, nil
}

func asFloat64(val any) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case []byte:
		f, _ := strconv.ParseFloat(string(v), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	default:
		return 0
	}
}

// asInt64 is a safe type-assertion helper for SQLite integer scans
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

// Helper to safely extract a string from the database interface
func asString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v) // Fallback for safety
	}
}

// Scan a single row
func (r *SQLiteRepository) scanEntryRow(rows *sql.Rows, customFields []repo.CustomFieldDef) (repo.Entry, error) {
	scanner, err := newEntryScanner(rows, customFields)
	if err != nil {
		return repo.Entry{}, err
	}

	if !rows.Next() {
		return repo.Entry{}, customerrors.ErrNotFound
	}

	return scanner.scan(rows)
}

// Scan multiple rows
func (r *SQLiteRepository) scanEntryRows(rows *sql.Rows, customFields []repo.CustomFieldDef) ([]repo.Entry, error) {
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

// validateAndFormatSearchField prevents SQL injection by ensuring a field name exists.
func (r *SQLiteRepository) validateAndFormatSearchField(field string, customFields []repo.CustomFieldDef) (string, error) {
	// 1. Whitelist Standard Fields
	standardFields := map[string]bool{
		"id": true, "timestamp": true, "created_at": true, "updated_at": true,
		"filesize": true, "preview_filesize": true, "filename": true, "status": true, "mime_type": true,
	}
	if standardFields[field] {
		return fmt.Sprintf(`"%s"`, field), nil
	}

	// 2. Whitelist Known Media Fields (dynamically from the repository)
	for _, fields := range r.MediaFields {
		for _, mediaField := range fields {
			if mediaField.Name == field {
				return fmt.Sprintf(`"%s"`, field), nil
			}
		}
	}

	// 3. Whitelist dynamically generated Custom Fields
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

// mapCustomFieldsToSQLiteColumns maps entry custom fields into the target SQL column-data map.
func mapCustomFieldsToSQLiteColumns(
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
					target[fmt.Sprintf("%s%d_lat", customFieldsPrefix, cf.ID)] = nil
					target[fmt.Sprintf("%s%d_lng", customFieldsPrefix, cf.ID)] = nil
				} else {
					coord, err := repo.ParseCoordinate(value)
					if err != nil {
						return fmt.Errorf("%w: %v", customerrors.ErrValidation, err)
					}
					target[fmt.Sprintf("%s%d_lat", customFieldsPrefix, cf.ID)] = coord.Latitude
					target[fmt.Sprintf("%s%d_lng", customFieldsPrefix, cf.ID)] = coord.Longitude
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

// isValidOperator checks if the requested SQL operator is whitelisted.
func isValidOperator(op string) bool {
	valid := map[string]bool{
		"=": true, "!=": true, ">": true, ">=": true, "<": true, "<=": true, "LIKE": true,
	}
	return valid[strings.ToUpper(op)]
}
