package entryhandler

import (
	"testing"

	repo "mediahub_oss/internal/repository"
)

func TestMapCustomFields_EmptyAndInvalidCells(t *testing.T) {
	h := &EntryHandler{}
	db := repo.Database{
		CustomFields: []repo.CustomFieldDef{
			{ID: 1, Name: "count", Type: repo.CustomFieldTypeInteger},
			{ID: 2, Name: "score", Type: repo.CustomFieldTypeReal},
			{ID: 3, Name: "active", Type: repo.CustomFieldTypeBoolean},
			{ID: 4, Name: "notes", Type: repo.CustomFieldTypeText},
			{ID: 5, Name: "location", Type: repo.CustomFieldTypeCoordinate},
		},
	}
	headers := []string{
		"id", "timestamp", "filename", "initial_filename", "mime_type", "initial_mime_type", "status",
		"count", "score", "active", "notes", "location",
	}
	config := ImportConfigPayload{
		UnmappedFields: "ignore",
	}

	// Case 1: Empty cells should be omitted so they remain NULL in the database, not coerced to 0 / false / ""
	emptyRow := []string{
		"1", "1700000000", "file1.jpg", "file1.jpg", "image/jpeg", "image/jpeg", "2",
		"", "   ", "", "", "",
	}
	mappedEmpty, err := h.mapCustomFields(emptyRow, headers, db, config)
	if err != nil {
		t.Fatalf("unexpected error mapping empty row: %v", err)
	}
	if len(mappedEmpty) != 0 {
		t.Errorf("expected 0 mapped fields for empty cells, got %d: %v", len(mappedEmpty), mappedEmpty)
	}

	// Case 2: Valid cells should be parsed into their respective types
	validRow := []string{
		"2", "1700000000", "file2.jpg", "file2.jpg", "image/jpeg", "image/jpeg", "2",
		"42", "3.14", "true", "hello", "48.137, 11.576",
	}
	mappedValid, err := h.mapCustomFields(validRow, headers, db, config)
	if err != nil {
		t.Fatalf("unexpected error mapping valid row: %v", err)
	}
	if mappedValid["count"] != int64(42) {
		t.Errorf("expected count=42, got %v", mappedValid["count"])
	}
	if mappedValid["score"] != 3.14 {
		t.Errorf("expected score=3.14, got %v", mappedValid["score"])
	}
	if mappedValid["active"] != true {
		t.Errorf("expected active=true, got %v", mappedValid["active"])
	}
	if mappedValid["notes"] != "hello" {
		t.Errorf("expected notes='hello', got %v", mappedValid["notes"])
	}
	coord, ok := mappedValid["location"].(repo.Coordinate)
	if !ok || coord.Latitude != 48.137 || coord.Longitude != 11.576 {
		t.Errorf("expected location=(48.137, 11.576), got %v", mappedValid["location"])
	}

	// Case 3: Malformed numeric/boolean/coordinate strings should be skipped rather than defaulting to 0/false
	invalidRow := []string{
		"3", "1700000000", "file3.jpg", "file3.jpg", "image/jpeg", "image/jpeg", "2",
		"not_an_int", "not_a_float", "not_a_bool", "text_ok", "999, 999",
	}
	mappedInvalid, err := h.mapCustomFields(invalidRow, headers, db, config)
	if err != nil {
		t.Fatalf("unexpected error mapping invalid row: %v", err)
	}
	if _, exists := mappedInvalid["count"]; exists {
		t.Errorf("expected malformed integer to be omitted, got %v", mappedInvalid["count"])
	}
	if _, exists := mappedInvalid["score"]; exists {
		t.Errorf("expected malformed float to be omitted, got %v", mappedInvalid["score"])
	}
	if _, exists := mappedInvalid["active"]; exists {
		t.Errorf("expected malformed boolean to be omitted, got %v", mappedInvalid["active"])
	}
	if _, exists := mappedInvalid["location"]; exists {
		t.Errorf("expected out-of-bounds coordinate to be omitted, got %v", mappedInvalid["location"])
	}
	if mappedInvalid["notes"] != "text_ok" {
		t.Errorf("expected valid text field to be preserved, got %v", mappedInvalid["notes"])
	}
}

func TestParseStandardFields_UntrustedSizesAndStatusNormalization(t *testing.T) {
	h := &EntryHandler{}

	tests := []struct {
		name           string
		row            []string
		expectedStatus repo.EntryStatus
		expectErr      bool
	}{
		{
			name:           "ready status preserved, sizes set to 0",
			row:            []string{"1", "test.jpg", "2026-01-01T00:00:00Z", "999999", "88888", "image/jpeg", "0"},
			expectedStatus: repo.EntryStatusReady,
			expectErr:      false,
		},
		{
			name:           "processing status normalized to ready",
			row:            []string{"2", "test.jpg", "2026-01-01T00:00:00Z", "123", "456", "image/jpeg", "1"},
			expectedStatus: repo.EntryStatusReady,
			expectErr:      false,
		},
		{
			name:           "deleting status normalized to ready",
			row:            []string{"3", "test.jpg", "2026-01-01T00:00:00Z", "123", "456", "image/jpeg", "3"},
			expectedStatus: repo.EntryStatusReady,
			expectErr:      false,
		},
		{
			name:           "queued status normalized to ready",
			row:            []string{"4", "test.jpg", "2026-01-01T00:00:00Z", "123", "456", "image/jpeg", "4"},
			expectedStatus: repo.EntryStatusReady,
			expectErr:      false,
		},
		{
			name:           "error status preserved",
			row:            []string{"5", "test.jpg", "2026-01-01T00:00:00Z", "123", "456", "image/jpeg", "2"},
			expectedStatus: repo.EntryStatusError,
			expectErr:      false,
		},
		{
			name:      "invalid status rejected",
			row:       []string{"6", "test.jpg", "2026-01-01T00:00:00Z", "123", "456", "image/jpeg", "99"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := h.parseStandardFields(tc.row)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if entry.Size != 0 {
				t.Errorf("expected Size=0, got %d", entry.Size)
			}
			if entry.PreviewSize != 0 {
				t.Errorf("expected PreviewSize=0, got %d", entry.PreviewSize)
			}
			if entry.Status != tc.expectedStatus {
				t.Errorf("expected Status=%v, got %v", tc.expectedStatus, entry.Status)
			}
		})
	}
}

func TestValidateCustomFields_NullScalarsAllowed(t *testing.T) {
	defs := []repo.CustomFieldDef{
		{Name: "title", Type: repo.CustomFieldTypeText},
		{Name: "rating", Type: repo.CustomFieldTypeInteger},
		{Name: "score", Type: repo.CustomFieldTypeReal},
		{Name: "archived", Type: repo.CustomFieldTypeBoolean},
		{Name: "location", Type: repo.CustomFieldTypeCoordinate},
	}

	provided := map[string]any{
		"title":    nil,
		"rating":   nil,
		"score":    nil,
		"archived": nil,
		"location": nil,
	}

	err := validateCustomFields(provided, defs)
	if err != nil {
		t.Fatalf("expected nil error when setting custom fields to nil, got: %v", err)
	}

	// Unknown field should still fail
	err = validateCustomFields(map[string]any{"unknown_field": nil}, defs)
	if err == nil {
		t.Fatalf("expected error for unknown field with nil value, got nil")
	}
}
