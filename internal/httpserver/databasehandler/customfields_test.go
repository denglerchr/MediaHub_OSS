package databasehandler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FE-049 regression specs: custom-field names colliding with the standard entry
// and media fields (id, status, timestamp, filename, filesize, mime_type, width,
// height, duration, channels) as well as with the standard search fields
// (created_at, updated_at, preview_filesize — internal/repository/sqlite/
// entries_utils.go standardFields) must be rejected — they break the frontend
// entry tables (duplicate keys, wrong filter column) and cannot be read back
// distinctly.

func TestIsReservedCustomFieldName(t *testing.T) {
	reserved := []string{
		"id", "status", "timestamp", "filename", "filesize", "mime_type",
		"width", "height", "duration", "channels",
		"created_at", "updated_at", "preview_filesize",
	}
	for _, name := range reserved {
		if !IsReservedCustomFieldName(name) {
			t.Errorf("expected %q to be reserved", name)
		}
		// Case-insensitive and whitespace-tolerant matching.
		if !IsReservedCustomFieldName(" " + strings.ToUpper(name) + " ") {
			t.Errorf("expected %q to be reserved (case-insensitive)", name)
		}
	}

	// `previewsize` is the CSV spelling of the standard column `preview_filesize`
	// (utils_import.go) and does not collide with any search field — allowed.
	allowed := []string{"artist", "location", "rating", "id2", "width_mm", "StatusUpdate", "previewsize"}
	for _, name := range allowed {
		if IsReservedCustomFieldName(name) {
			t.Errorf("expected %q to be allowed", name)
		}
	}
}

func TestCustomFieldToModel_RejectsReservedName(t *testing.T) {
	for _, name := range []string{"id", "STATUS", "mime_type", "duration", "created_at", "UPDATED_AT", "preview_filesize"} {
		cf := DatabaseCustomField{Name: name, Type: "TEXT"}
		_, err := cf.toModel()
		if err == nil {
			t.Fatalf("expected reserved name %q to be rejected", name)
		}
		if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("expected a 'reserved' error for %q, got: %v", name, err)
		}
	}

	ok := DatabaseCustomField{Name: "artist", Type: "TEXT"}
	if _, err := ok.toModel(); err != nil {
		t.Errorf("expected 'artist' to be accepted, got: %v", err)
	}
}

// The reserved-name check must fire *before* the repository is touched: AddField
// with a nil Repo must answer 400 instead of panicking on Repo.AddCustomField.
func TestAddField_RejectsReservedName(t *testing.T) {
	h := &DatabaseHandler{}

	for _, name := range []string{"status", "Filename", "created_at", "Preview_Filesize"} {
		body := `{"name":"` + name + `","type":"TEXT"}`
		req := httptest.NewRequest(http.MethodPost, "/api/database/db1/field", strings.NewReader(body))
		req.SetPathValue("database_id", "db1")
		rec := httptest.NewRecorder()

		h.AddField(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for reserved name %q, got %d", name, rec.Code)
		}

		var resp map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response body: %v", err)
		}
		if !strings.Contains(resp["error"], "reserved") {
			t.Errorf("expected a 'reserved' error message for %q, got: %q", name, resp["error"])
		}
	}
}

// Renaming a field onto a reserved name must be rejected the same way.
func TestUpdateField_RejectsReservedRename(t *testing.T) {
	h := &DatabaseHandler{}

	body := `{"name":"timestamp"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/database/db1/field/1", strings.NewReader(body))
	req.SetPathValue("database_id", "db1")
	req.SetPathValue("field_id", "1")
	rec := httptest.NewRecorder()

	h.UpdateField(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for reserved rename, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response body: %v", err)
	}
	if !strings.Contains(resp["error"], "reserved") {
		t.Errorf("expected a 'reserved' error message, got: %q", resp["error"])
	}
}
