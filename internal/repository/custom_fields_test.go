package repository_test

import (
	"encoding/json"
	"testing"

	repo "mediahub_oss/internal/repository"
)

func TestCustomFieldType_StringAndValidity(t *testing.T) {
	tests := []struct {
		c        repo.CustomFieldType
		expected string
		valid    bool
		isCoord  bool
	}{
		{repo.CustomFieldTypeUnknown, "UNKNOWN", false, false},
		{repo.CustomFieldTypeText, "TEXT", true, false},
		{repo.CustomFieldTypeInteger, "INTEGER", true, false},
		{repo.CustomFieldTypeReal, "REAL", true, false},
		{repo.CustomFieldTypeBoolean, "BOOLEAN", true, false},
		{repo.CustomFieldTypeCoordinate, "COORDINATE", true, true},
		{repo.CustomFieldType(99), "UNKNOWN", false, false},
	}

	for _, tc := range tests {
		if tc.c.String() != tc.expected {
			t.Errorf("expected string %s, got %s", tc.expected, tc.c.String())
		}
		if tc.c.IsValid() != tc.valid {
			t.Errorf("expected valid %v for %s, got %v", tc.valid, tc.c.String(), tc.c.IsValid())
		}
		if tc.c.IsCoordinate() != tc.isCoord {
			t.Errorf("expected isCoordinate %v for %s, got %v", tc.isCoord, tc.c.String(), tc.c.IsCoordinate())
		}
	}
}

func TestParseCustomFieldType(t *testing.T) {
	validCases := map[string]repo.CustomFieldType{
		"TEXT":             repo.CustomFieldTypeText,
		"text":             repo.CustomFieldTypeText,
		"string":           repo.CustomFieldTypeText,
		"str":              repo.CustomFieldTypeText,
		"varchar":          repo.CustomFieldTypeText,
		"INTEGER":          repo.CustomFieldTypeInteger,
		"int":              repo.CustomFieldTypeInteger,
		"int64":            repo.CustomFieldTypeInteger,
		"bigint":           repo.CustomFieldTypeInteger,
		"smallint":         repo.CustomFieldTypeInteger,
		"REAL":             repo.CustomFieldTypeReal,
		"float":            repo.CustomFieldTypeReal,
		"float64":          repo.CustomFieldTypeReal,
		"double":           repo.CustomFieldTypeReal,
		"double precision": repo.CustomFieldTypeReal,
		"BOOLEAN":          repo.CustomFieldTypeBoolean,
		"bool":             repo.CustomFieldTypeBoolean,
		"COORDINATE":       repo.CustomFieldTypeCoordinate,
		"coord":            repo.CustomFieldTypeCoordinate,
		"point":            repo.CustomFieldTypeCoordinate,
		"location":         repo.CustomFieldTypeCoordinate,
		"geo":              repo.CustomFieldTypeCoordinate,
	}

	for input, expected := range validCases {
		parsed, err := repo.ParseCustomFieldType(input)
		if err != nil {
			t.Errorf("expected parsing %q to succeed, got error: %v", input, err)
		}
		if parsed != expected {
			t.Errorf("expected %q -> %v, got %v", input, expected, parsed)
		}
	}

	invalidCases := []string{"", "blob", "json", "unknown", "invalid_type"}
	for _, input := range invalidCases {
		_, err := repo.ParseCustomFieldType(input)
		if err == nil {
			t.Errorf("expected parsing %q to fail, got nil error", input)
		}
	}
}

func TestCustomFieldType_JSON(t *testing.T) {
	// Marshal
	val := repo.CustomFieldTypeCoordinate
	bytes, err := json.Marshal(val)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if string(bytes) != `"COORDINATE"` {
		t.Errorf("expected %q, got %q", `"COORDINATE"`, string(bytes))
	}

	// Unmarshal string
	var unmarshaled repo.CustomFieldType
	if err := json.Unmarshal([]byte(`"coordinate"`), &unmarshaled); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if unmarshaled != repo.CustomFieldTypeCoordinate {
		t.Errorf("expected COORDINATE, got %v", unmarshaled)
	}

	// Unmarshal null
	var nullTarget repo.CustomFieldType = repo.CustomFieldTypeText
	if err := json.Unmarshal([]byte(`null`), &nullTarget); err != nil {
		t.Fatalf("unmarshal null error: %v", err)
	}
	if nullTarget != repo.CustomFieldTypeUnknown {
		t.Errorf("expected UNKNOWN for null, got %v", nullTarget)
	}

	// Unmarshal invalid string
	if err := json.Unmarshal([]byte(`"invalid"`), &unmarshaled); err == nil {
		t.Errorf("expected error for invalid type string")
	}
}

func TestCustomFieldType_SQLScanAndValue(t *testing.T) {
	var c repo.CustomFieldType

	// Scan string
	if err := c.Scan("COORDINATE"); err != nil {
		t.Fatalf("scan string error: %v", err)
	}
	if c != repo.CustomFieldTypeCoordinate {
		t.Errorf("expected COORDINATE, got %v", c)
	}

	// Scan []byte
	if err := c.Scan([]byte("INTEGER")); err != nil {
		t.Fatalf("scan bytes error: %v", err)
	}
	if c != repo.CustomFieldTypeInteger {
		t.Errorf("expected INTEGER, got %v", c)
	}

	// Scan nil
	if err := c.Scan(nil); err != nil {
		t.Fatalf("scan nil error: %v", err)
	}
	if c != repo.CustomFieldTypeUnknown {
		t.Errorf("expected UNKNOWN for nil, got %v", c)
	}

	// Value valid
	c = repo.CustomFieldTypeReal
	v, err := c.Value()
	if err != nil || v != "REAL" {
		t.Errorf("expected Value() to return 'REAL', got %v, err: %v", v, err)
	}

	// Value invalid
	c = repo.CustomFieldTypeUnknown
	if _, err := c.Value(); err == nil {
		t.Errorf("expected error calling Value() on unknown type")
	}
}
