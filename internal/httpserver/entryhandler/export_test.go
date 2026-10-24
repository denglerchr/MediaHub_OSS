package entryhandler_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"mediahub_oss/internal/httpserver/entryhandler"
	"mediahub_oss/internal/media"
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
	entries   map[int64]repo.Entry
	createdCh chan repo.Entry
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

func (m *mockExportRepo) CreateEntry(ctx context.Context, db repo.Database, entry repo.Entry) (repo.Entry, error) {
	entry.ID = 202
	if m.createdCh != nil {
		m.createdCh <- entry
	}
	return entry, nil
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

func (m *mockStorage) Write(ctx context.Context, dbID string, id int64, r io.Reader) (int64, error) {
	return io.Copy(io.Discard, r)
}

func (m *mockStorage) WritePreview(ctx context.Context, dbID string, id int64, r io.Reader) (int64, error) {
	return io.Copy(io.Discard, r)
}

type mockImportMediaConverter struct {
	media.MediaConverter
}

func (m *mockImportMediaConverter) ReadMediaFieldsFromFile(ctx context.Context, filePath string, contentType string) (map[string]any, error) {
	return map[string]any{"width": 1920, "height": 1080}, nil
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


func TestImportEntries_DiskSpooledMultipartFile(t *testing.T) {
	mockDB := repo.Database{
		ID:          "01HGFB9Z5W7ABCDEFGHJKMNPQR",
		Name:        "TestImageDB",
		ContentType: "image",
		CustomFields: []repo.CustomFieldDef{
			{Name: "photographer", Type: repo.CustomFieldTypeText},
		},
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	csvWriter, err := zw.Create("entries.csv")
	if err != nil {
		t.Fatalf("failed to create entries.csv in zip: %v", err)
	}
	w := csv.NewWriter(csvWriter)
	_ = w.Write([]string{"id", "filename", "timestamp", "filesize", "previewsize", "mime_type", "status", "photographer"})
	_ = w.Write([]string{"101", "photo1.jpg", "2026-01-15T10:00:00Z", "18", "0", "image/jpeg", "0", "Jane Doe"})
	w.Flush()

	mediaWriter, err := zw.Create("files/101_photo1.jpg")
	if err != nil {
		t.Fatalf("failed to create media file in zip: %v", err)
	}
	_, _ = mediaWriter.Write([]byte("dummy file content"))
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}

	createdCh := make(chan repo.Entry, 1)
	mockRepo := &mockExportRepo{
		db:        mockDB,
		entries:   map[int64]repo.Entry{},
		createdCh: createdCh,
	}

	h := &entryhandler.EntryHandler{
		Logger:                 slog.Default(),
		Auditor:                &mockAuditor{},
		Repo:                   mockRepo,
		Storage:                &mockStorage{},
		MediaConverter:         &mockImportMediaConverter{},
		MaxSyncUploadSizeBytes: 0, // Force multipart upload to be spooled to an *os.File on disk
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="export.zip"`)
	partHeader.Set("Content-Type", "application/zip")
	part, err := mw.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("failed to create multipart part: %v", err)
	}
	_, _ = part.Write(zipBuf.Bytes())
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/database/01HGFB9Z5W7ABCDEFGHJKMNPQR/entries/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("database_id", "01HGFB9Z5W7ABCDEFGHJKMNPQR")
	user := repo.User{Username: "testuser"}
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &user))

	rec := httptest.NewRecorder()
	h.ImportEntries(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case imported := <-createdCh:
		if imported.FileName != "photo1.jpg" {
			t.Errorf("expected imported filename 'photo1.jpg', got %q", imported.FileName)
		}
		if imported.CustomFields["photographer"] != "Jane Doe" {
			t.Errorf("expected custom field photographer='Jane Doe', got %v", imported.CustomFields["photographer"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for background import job to process disk-spooled multipart upload")
	}
}
