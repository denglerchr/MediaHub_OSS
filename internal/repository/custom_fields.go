package repository

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// CustomFieldType represents the supported data types for database custom fields.
type CustomFieldType uint8

const (
	CustomFieldTypeUnknown    CustomFieldType = 0x00
	CustomFieldTypeText       CustomFieldType = 0x01
	CustomFieldTypeInteger    CustomFieldType = 0x02
	CustomFieldTypeReal       CustomFieldType = 0x03
	CustomFieldTypeBoolean    CustomFieldType = 0x04
	CustomFieldTypeCoordinate CustomFieldType = 0x05
)

// String returns the canonical uppercase string representation of the custom field type.
func (c CustomFieldType) String() string {
	switch c {
	case CustomFieldTypeText:
		return "TEXT"
	case CustomFieldTypeInteger:
		return "INTEGER"
	case CustomFieldTypeReal:
		return "REAL"
	case CustomFieldTypeBoolean:
		return "BOOLEAN"
	case CustomFieldTypeCoordinate:
		return "COORDINATE"
	default:
		return "UNKNOWN"
	}
}

// IsValid returns true if the type is one of the recognized custom field types.
func (c CustomFieldType) IsValid() bool {
	return c >= CustomFieldTypeText && c <= CustomFieldTypeCoordinate
}

// IsCoordinate returns true if the type is CustomFieldTypeCoordinate.
func (c CustomFieldType) IsCoordinate() bool {
	return c == CustomFieldTypeCoordinate
}

// ParseCustomFieldType converts a string (case-insensitive, with common aliases) to a CustomFieldType.
func ParseCustomFieldType(s string) (CustomFieldType, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TEXT", "STRING", "STR", "TXT", "VARCHAR":
		return CustomFieldTypeText, nil
	case "INTEGER", "INT", "INT32", "INT64", "UINT", "UINT32", "UINT64", "BIGINT", "SMALLINT", "NUMBER":
		return CustomFieldTypeInteger, nil
	case "REAL", "FLOAT", "FLOAT32", "FLOAT64", "DOUBLE", "DOUBLE PRECISION", "DECIMAL", "NUMERIC":
		return CustomFieldTypeReal, nil
	case "BOOLEAN", "BOOL":
		return CustomFieldTypeBoolean, nil
	case "COORDINATE", "COORD", "POINT", "LOCATION", "GEO":
		return CustomFieldTypeCoordinate, nil
	default:
		return CustomFieldTypeUnknown, fmt.Errorf("unsupported custom field type '%s', must be TEXT, INTEGER, REAL, BOOLEAN, or COORDINATE", s)
	}
}

// MarshalJSON serializes the CustomFieldType as its canonical string.
func (c CustomFieldType) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

// UnmarshalJSON deserializes a string (or numeric representation) into CustomFieldType.
func (c *CustomFieldType) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*c = CustomFieldTypeUnknown
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "" {
			*c = CustomFieldTypeUnknown
			return nil
		}
		parsed, err := ParseCustomFieldType(s)
		if err != nil {
			return err
		}
		*c = parsed
		return nil
	}
	var i uint8
	if err := json.Unmarshal(data, &i); err == nil {
		t := CustomFieldType(i)
		if !t.IsValid() {
			return fmt.Errorf("invalid custom field type: %d", i)
		}
		*c = t
		return nil
	}
	return fmt.Errorf("invalid custom field type")
}

// Scan implements the sql.Scanner interface for reading from database columns.
func (c *CustomFieldType) Scan(value any) error {
	if value == nil {
		*c = CustomFieldTypeUnknown
		return nil
	}
	switch v := value.(type) {
	case string:
		t, err := ParseCustomFieldType(v)
		if err != nil {
			return err
		}
		*c = t
		return nil
	case []byte:
		t, err := ParseCustomFieldType(string(v))
		if err != nil {
			return err
		}
		*c = t
		return nil
	case int64:
		t := CustomFieldType(v)
		if !t.IsValid() {
			return fmt.Errorf("invalid custom field type id: %d", v)
		}
		*c = t
		return nil
	default:
		return fmt.Errorf("cannot scan type %T into CustomFieldType", value)
	}
}

// Value implements the driver.Valuer interface for writing to database columns.
func (c CustomFieldType) Value() (driver.Value, error) {
	if !c.IsValid() {
		return nil, fmt.Errorf("invalid custom field type: %d", c)
	}
	return c.String(), nil
}
