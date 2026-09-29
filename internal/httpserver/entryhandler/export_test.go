package entryhandler_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mediahub_oss/internal/httpserver/entryhandler"
	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/storage"
)

type mockAuditor struct{}

func (m *mockAuditor) Log(ctx context.Context, action string, actor string, resource string, details map[string]any) {
}

type mockExportRepo struct {
	repo.Repository
	db      repo.Database
	entries map[int64]repo.Entry
}

func (m *mockExportRepo) GetDatabase(ctx context.Context, dbID repo.ULID) (repo.Database, error) {
	return m.db, nil
}

func (m *mockExportRepo) GetEntry(ctx context.Context, dbID repo.ULID, id int64) (repo.Entry, error) {
	if e, ok := m.entries[id]; ok {
		return e, nil
	}
	return repo.Entry{}, customerrors.ErrNotFound
}

type mockStorage struct {
	storage.StorageProvider
}

func (m *mockStorage) Read(ctx context.Context, dbID string, id int64, offset int64, length int64) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("dummy file content")), nil
}

func (m *mockStorage) ReadPreview(ctx context.Context, dbID string, id int64) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("dummy preview content")), nil
}

func TestExportEntries_IncludesMediaSpecificColumns(t *testing.T) {
	mockDB := repo.Database{
		ID:          "01HGFB9Z5W7ABCDEFGHJKMNPQR",
		Name:        "TestImageDB",
		ContentType: "image",
		CustomFields: []repo.CustomFieldDef{
			{Name: "photographer", Type: repo.CustomFieldTypeText},
			{Name: "rating", Type: repo.CustomFieldTypeInteger},
		},
	}

	entry1 := repo.Entry{
		ID:          101,
		FileName:    "photo1.jpg",
		Timestamp:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		Size:        10240,
		PreviewSize: 512,
		MimeType:    "image/jpeg",
		Status:      0,
		MediaFields: map[string]any{
			"width":  1920,
			"height": 1080,
		},
		CustomFields: map[string]any{
			"photographer": "John Doe",
			"rating":       5,
		},
	}

	mockRepo := &mockExportRepo{
		db: mockDB,
		entries: map[int64]repo.Entry{
			101: entry1,
		},
	}

	h := &entryhandler.EntryHandler{
		Logger:  slog.Default(),
		Auditor: &mockAuditor{},
		Repo:    mockRepo,
		Storage: &mockStorage{},
	}

	payload := entryhandler.ExportRequest{IDs: []int64{101}}
	bodyBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/database/01HGFB9Z5W7ABCDEFGHJKMNPQR/entries/export", bytes.NewReader(bodyBytes))
	req.SetPathValue("database_id", "01HGFB9Z5W7ABCDEFGHJKMNPQR")
	user := repo.User{Username: "testuser"}
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &user))

	rec := httptest.NewRecorder()

	h.ExportEntries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("failed to open zip response: %v", err)
	}

	var csvFile *zip.File
	for _, f := range zipReader.File {
		if f.Name == "entries.csv" {
			csvFile = f
			break
		}
	}
	if csvFile == nil {
		t.Fatalf("entries.csv not found in exported zip")
	}

	rc, err := csvFile.Open()
	if err != nil {
		t.Fatalf("failed to open entries.csv in zip: %v", err)
	}
	defer rc.Close()

	reader := csv.NewReader(rc)
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("failed to parse CSV: %v", err)
	}

	if len(rows) < 2 {
		t.Fatalf("expected at least 2 rows (header + 1 entry), got %d", len(rows))
	}

	// Verify Header: id, filename, timestamp, filesize, previewsize, mime_type, status, width, height, photographer, rating
	expectedHeader := []string{"id", "filename", "timestamp", "filesize", "previewsize", "mime_type", "status", "width", "height", "photographer", "rating"}
	header := rows[0]
	if len(header) != len(expectedHeader) {
		t.Fatalf("header length mismatch: expected %d, got %d. Header: %v", len(expectedHeader), len(header), header)
	}
	for i, col := range expectedHeader {
		if header[i] != col {
			t.Errorf("header col %d mismatch: expected %q, got %q", i, col, header[i])
		}
	}

	// Verify Row: 101, photo1.jpg, 2026-01-15T10:00:00Z, 10240, 512, image/jpeg, 0, 1920, 1080, John Doe, 5
	expectedRow := []string{"101", "photo1.jpg", "2026-01-15T10:00:00Z", "10240", "512", "image/jpeg", "0", "1920", "1080", "John Doe", "5"}
	row := rows[1]
	if len(row) != len(expectedRow) {
		t.Fatalf("row length mismatch: expected %d, got %d. Row: %v", len(expectedRow), len(row), row)
	}
	for i, val := range expectedRow {
		if row[i] != val {
			t.Errorf("row col %d (%s) mismatch: expected %q, got %q", i, expectedHeader[i], val, row[i])
		}
	}
}
