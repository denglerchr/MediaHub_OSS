package entryhandler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
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

type limitTestAuditor struct{}

func (a *limitTestAuditor) Log(ctx context.Context, action, actor, resource string, details map[string]any) {
}

type limitTestRepo struct {
	repo.Repository
	db          repo.Database
	entries     map[int64]repo.Entry
	nextID      int64
	createCalls int
}

func (r *limitTestRepo) GetDatabase(ctx context.Context, dbID repo.ULID) (repo.Database, error) {
	return r.db, nil
}

func (r *limitTestRepo) GetEntry(ctx context.Context, dbID repo.ULID, id int64) (repo.Entry, error) {
	if e, ok := r.entries[id]; ok {
		return e, nil
	}
	return repo.Entry{}, customerrors.ErrNotFound
}

func (r *limitTestRepo) CreateEntry(ctx context.Context, db repo.Database, entry repo.Entry) (repo.Entry, error) {
	r.createCalls++
	r.nextID++
	entry.ID = r.nextID
	if r.entries == nil {
		r.entries = make(map[int64]repo.Entry)
	}
	r.entries[entry.ID] = entry
	return entry, nil
}

func (r *limitTestRepo) UpdateEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) (repo.Entry, error) {
	r.entries[entry.ID] = entry
	return entry, nil
}

func (r *limitTestRepo) DeleteEntry(ctx context.Context, dbID repo.ULID, id int64) (repo.DeletedEntryMeta, error) {
	delete(r.entries, id)
	return repo.DeletedEntryMeta{ID: id}, nil
}

type limitTestStorage struct {
	storage.StorageProvider
	mu               sync.Mutex
	writePreviewHits int
	deleteHits       int
}

func (s *limitTestStorage) Write(ctx context.Context, dbID ulid.ULID, id int64, r io.Reader) (int64, error) {
	return io.Copy(io.Discard, r)
}

func (s *limitTestStorage) WritePreview(ctx context.Context, dbID ulid.ULID, id int64, r io.Reader) (int64, error) {
	s.mu.Lock()
	s.writePreviewHits++
	s.mu.Unlock()
	return io.Copy(io.Discard, r)
}

func (s *limitTestStorage) Delete(ctx context.Context, dbID ulid.ULID, id int64) error {
	s.mu.Lock()
	s.deleteHits++
	s.mu.Unlock()
	return nil
}

func (s *limitTestStorage) DeletePreview(ctx context.Context, dbID ulid.ULID, id int64) error {
	return nil
}

type limitTestConverter struct {
	media.MediaConverter
}

func (c *limitTestConverter) ReadMediaFieldsFromFile(ctx context.Context, filePath string, contentType string) (map[string]any, error) {
	return map[string]any{"width": 100, "height": 100}, nil
}

func buildTestImportMultipart(t *testing.T, zipBytes []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="export.zip"`)
	partHeader.Set("Content-Type", "application/zip")
	part, err := mw.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("failed to create multipart part: %v", err)
	}
	_, _ = part.Write(zipBytes)
	_ = mw.Close()
	return &body, mw.FormDataContentType()
}

func writeTestZipToFile(t *testing.T, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
	return path
}

func makeCSVBytes(rows [][]string) []byte {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"id", "filename", "timestamp", "filesize", "previewsize", "mime_type", "status"})
	for _, r := range rows {
		_ = cw.Write(r)
	}
	cw.Flush()
	return buf.Bytes()
}

func TestImportSizeConstants(t *testing.T) {
	if maxImportUploadBytes != 2<<30 {
		t.Errorf("maxImportUploadBytes = %d, want %d", maxImportUploadBytes, int64(2<<30))
	}
	if maxImportCSVBytes != 64<<20 {
		t.Errorf("maxImportCSVBytes = %d, want %d", maxImportCSVBytes, int64(64<<20))
	}
	if maxImportMemberBytes != 2<<30 {
		t.Errorf("maxImportMemberBytes = %d, want %d", maxImportMemberBytes, int64(2<<30))
	}
	if maxImportPreviewBytes != 16<<20 {
		t.Errorf("maxImportPreviewBytes = %d, want %d", maxImportPreviewBytes, int64(16<<20))
	}
	if maxImportTotalUncompressedBytes != 10<<30 {
		t.Errorf("maxImportTotalUncompressedBytes = %d, want %d", maxImportTotalUncompressedBytes, int64(10<<30))
	}
}

func TestImportManager_ConcurrencyCapAndShutdown(t *testing.T) {
	mgr := NewImportManager(2)

	ctx1, rel1, ok1 := mgr.TryStart()
	ctx2, rel2, ok2 := mgr.TryStart()
	if !ok1 || !ok2 {
		t.Fatalf("expected first two TryStart calls to succeed, got %v, %v", ok1, ok2)
	}

	// Third concurrent import via HTTP handler returns 503
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	cw, _ := zw.Create("entries.csv")
	_, _ = cw.Write([]byte("id,filename,timestamp,filesize,previewsize,mime_type,status\n"))
	_ = zw.Close()

	mockRepo := &limitTestRepo{
		db: repo.Database{ID: ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"), Name: "db", ContentType: "image"},
	}
	h := &EntryHandler{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auditor:        &limitTestAuditor{},
		Repo:           mockRepo,
		Storage:        &limitTestStorage{},
		MediaConverter: &limitTestConverter{},
		Imports:        mgr,
	}

	body, ctype := buildTestImportMultipart(t, zipBuf.Bytes())
	req := httptest.NewRequest(http.MethodPost, "/api/database/01HGFB9Z5W7ABCDEFGHJKMNPQR/entries/import", body)
	req.Header.Set("Content-Type", ctype)
	req.SetPathValue("database_id", "01HGFB9Z5W7ABCDEFGHJKMNPQR")
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "testuser"}))
	rec := httptest.NewRecorder()

	h.ImportEntries(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when import queue is full, got %d: %s", rec.Code, rec.Body.String())
	}

	// Shutdown cancels active contexts and rejects new TryStart
	shutdownErrCh := make(chan error, 1)
	go func() {
		shutdownErrCh <- mgr.Shutdown(context.Background())
	}()

	select {
	case <-ctx1.Done():
	case <-time.After(time.Second):
		t.Fatal("expected Shutdown to cancel ctx1")
	}
	select {
	case <-ctx2.Done():
	case <-time.After(time.Second):
		t.Fatal("expected Shutdown to cancel ctx2")
	}

	if _, _, ok3 := mgr.TryStart(); ok3 {
		t.Fatal("expected TryStart to return false once shutting down")
	}

	rel1()
	rel2()

	select {
	case err := <-shutdownErrCh:
		if err != nil {
			t.Fatalf("expected Shutdown to succeed after active jobs release, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return after active jobs released")
	}

	// Shutdown timeout returns ctx.Err() when active job does not finish
	mgr2 := NewImportManager(1)
	_, relBlocked, ok := mgr2.TryStart()
	if !ok {
		t.Fatal("expected TryStart on mgr2 to succeed")
	}
	defer relBlocked()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := mgr2.Shutdown(timeoutCtx); err == nil {
		t.Fatal("expected Shutdown timeout to return context error")
	}
}

func TestImportEntries_OversizedUploadReturns413(t *testing.T) {
	r := &limitTestRepo{
		db: repo.Database{ID: ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"), Name: "db", ContentType: "image"},
	}
	h := &EntryHandler{
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auditor:              &limitTestAuditor{},
		Repo:                 r,
		Storage:              &limitTestStorage{},
		MediaConverter:       &limitTestConverter{},
		maxImportUploadBytes: 512,
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	ph := make(textproto.MIMEHeader)
	ph.Set("Content-Disposition", `form-data; name="file"; filename="big.zip"`)
	ph.Set("Content-Type", "application/zip")
	part, _ := mw.CreatePart(ph)
	_, _ = part.Write(bytes.Repeat([]byte("Z"), 2048))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/database/01HGFB9Z5W7ABCDEFGHJKMNPQR/entries/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("database_id", "01HGFB9Z5W7ABCDEFGHJKMNPQR")
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin"}))
	rec := httptest.NewRecorder()

	h.ImportEntries(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 Request Entity Too Large for oversized import upload, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestImportEntries_RejectsPreviewZipBomb(t *testing.T) {
	mockDB := repo.Database{
		ID:          ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Name:        "TestImageDB",
		ContentType: "image",
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	csvWriter, _ := zw.Create("entries.csv")
	w := csv.NewWriter(csvWriter)
	_ = w.Write([]string{"id", "filename", "timestamp", "filesize", "previewsize", "mime_type", "status"})
	_ = w.Write([]string{"101", "photo1.jpg", "2026-01-15T10:00:00Z", "10", "0", "image/jpeg", "0"})
	w.Flush()

	mediaWriter, _ := zw.Create("files/101_photo1.jpg")
	_, _ = mediaWriter.Write([]byte("0123456789"))

	// Write a preview exceeding 16 MiB (highly compressible zeros)
	prevWriter, _ := zw.Create("previews/101.webp")
	zeros := make([]byte, 1<<20)
	for range 17 {
		_, _ = prevWriter.Write(zeros)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip: %v", err)
	}

	mockRepo := &limitTestRepo{
		db:      mockDB,
		entries: map[int64]repo.Entry{},
	}
	spyStore := &limitTestStorage{}
	mgr := NewImportManager(2)

	h := &EntryHandler{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auditor:        &limitTestAuditor{},
		Repo:           mockRepo,
		Storage:        spyStore,
		MediaConverter: &limitTestConverter{},
		Imports:        mgr,
	}

	body, ctype := buildTestImportMultipart(t, zipBuf.Bytes())
	req := httptest.NewRequest(http.MethodPost, "/api/database/01HGFB9Z5W7ABCDEFGHJKMNPQR/entries/import", body)
	req.Header.Set("Content-Type", ctype)
	req.SetPathValue("database_id", "01HGFB9Z5W7ABCDEFGHJKMNPQR")
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "testuser"}))
	rec := httptest.NewRecorder()

	h.ImportEntries(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	deleted := false
	for time.Now().Before(deadline) {
		spyStore.mu.Lock()
		hits := spyStore.deleteHits
		spyStore.mu.Unlock()
		if hits > 0 {
			deleted = true
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = mgr.Shutdown(shutdownCtx)

	if !deleted {
		t.Fatal("expected oversized preview bomb to trigger rollback Delete on main file")
	}
	spyStore.mu.Lock()
	previewHits := spyStore.writePreviewHits
	spyStore.mu.Unlock()
	if previewHits != 0 {
		t.Fatalf("expected WritePreview not to be called for oversized preview, got %d calls", previewHits)
	}
}

func TestProcessImportJobWithLimits_ZipBombProtection(t *testing.T) {
	db := repo.Database{ID: ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"), Name: "db", ContentType: "image"}
	cfg := ImportConfigPayload{Mode: "generate_new", UnmappedFields: "ignore"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("member exceeding memberBytes is rejected without inserting entry", func(t *testing.T) {
		r := &limitTestRepo{db: db, entries: map[int64]repo.Entry{}}
		h := &EntryHandler{
			Logger:         logger,
			Repo:           r,
			Storage:        &limitTestStorage{},
			MediaConverter: &limitTestConverter{},
		}
		csvData := makeCSVBytes([][]string{
			{"1", "bomb.jpg", "2026-01-15T10:00:00Z", "10", "0", "image/jpeg", "0"},
		})
		zipPath := writeTestZipToFile(t, map[string][]byte{
			"entries.csv":      csvData,
			"files/1_bomb.jpg": bytes.Repeat([]byte("A"), 200),
		})
		limits := importSizeLimits{
			csvBytes:               1024,
			memberBytes:            100,
			previewBytes:           100,
			totalUncompressedBytes: 10000,
		}
		h.processImportJobWithLimits(context.Background(), db, "admin", zipPath, cfg, limits)
		if r.createCalls != 0 {
			t.Fatalf("expected 0 entries created when member exceeds memberBytes, got %d", r.createCalls)
		}
	})

	t.Run("entries.csv exceeding csvBytes aborts job", func(t *testing.T) {
		r := &limitTestRepo{db: db, entries: map[int64]repo.Entry{}}
		h := &EntryHandler{
			Logger:         logger,
			Repo:           r,
			Storage:        &limitTestStorage{},
			MediaConverter: &limitTestConverter{},
		}
		csvData := makeCSVBytes([][]string{
			{"1", "ok.jpg", "2026-01-15T10:00:00Z", "10", "0", "image/jpeg", "0"},
			{"2", strings.Repeat("x", 200) + ".jpg", "2026-01-15T10:00:00Z", "10", "0", "image/jpeg", "0"},
		})
		zipPath := writeTestZipToFile(t, map[string][]byte{
			"entries.csv":    csvData,
			"files/1_ok.jpg": []byte("small"),
			"files/2_" + strings.Repeat("x", 200) + ".jpg": []byte("small"),
		})
		limits := importSizeLimits{
			csvBytes:               120,
			memberBytes:            1024,
			previewBytes:           1024,
			totalUncompressedBytes: 10000,
		}
		h.processImportJobWithLimits(context.Background(), db, "admin", zipPath, cfg, limits)
		if len(r.entries) != 1 {
			t.Fatalf("expected row 1 to process and oversized row 2 to abort CSV reading, got %d entries", len(r.entries))
		}

		// Also verify header/single-row CSV exceeding csvBytes aborts with 0 entries
		r2 := &limitTestRepo{db: db, entries: map[int64]repo.Entry{}}
		h2 := &EntryHandler{
			Logger:         logger,
			Repo:           r2,
			Storage:        &limitTestStorage{},
			MediaConverter: &limitTestConverter{},
		}
		bigFirstRowCSV := makeCSVBytes([][]string{
			{"1", strings.Repeat("y", 200) + ".jpg", "2026-01-15T10:00:00Z", "10", "0", "image/jpeg", "0"},
		})
		zipPath2 := writeTestZipToFile(t, map[string][]byte{
			"entries.csv": bigFirstRowCSV,
		})
		h2.processImportJobWithLimits(context.Background(), db, "admin", zipPath2, cfg, limits)
		if r2.createCalls != 0 {
			t.Fatalf("expected 0 entries when first CSV row exceeds csvBytes, got %d", r2.createCalls)
		}
	})

	t.Run("cumulative totalUncompressedBytes aborts remaining rows", func(t *testing.T) {
		r := &limitTestRepo{db: db, entries: map[int64]repo.Entry{}}
		h := &EntryHandler{
			Logger:         logger,
			Repo:           r,
			Storage:        &limitTestStorage{},
			MediaConverter: &limitTestConverter{},
		}
		csvData := makeCSVBytes([][]string{
			{"1", "a.jpg", "2026-01-15T10:00:00Z", "50", "0", "image/jpeg", "0"},
			{"2", "b.jpg", "2026-01-15T10:00:00Z", "50", "0", "image/jpeg", "0"},
		})
		zipPath := writeTestZipToFile(t, map[string][]byte{
			"entries.csv":   csvData,
			"files/1_a.jpg": bytes.Repeat([]byte("A"), 50),
			"files/2_b.jpg": bytes.Repeat([]byte("B"), 50),
		})
		limits := importSizeLimits{
			csvBytes:               1024,
			memberBytes:            100,
			previewBytes:           100,
			totalUncompressedBytes: int64(len(csvData)) + 60,
		}
		h.processImportJobWithLimits(context.Background(), db, "admin", zipPath, cfg, limits)
		if len(r.entries) != 1 {
			t.Fatalf("expected 1 entry before total archive limit aborted row 2, got %d", len(r.entries))
		}
	})

	t.Run("context cancellation aborts before processing rows", func(t *testing.T) {
		r := &limitTestRepo{db: db, entries: map[int64]repo.Entry{}}
		h := &EntryHandler{
			Logger:         logger,
			Repo:           r,
			Storage:        &limitTestStorage{},
			MediaConverter: &limitTestConverter{},
		}
		csvData := makeCSVBytes([][]string{
			{"1", "a.jpg", "2026-01-15T10:00:00Z", "5", "0", "image/jpeg", "0"},
		})
		zipPath := writeTestZipToFile(t, map[string][]byte{
			"entries.csv":   csvData,
			"files/1_a.jpg": []byte("hello"),
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		h.processImportJobWithLimits(ctx, db, "admin", zipPath, cfg, defaultImportSizeLimits())
		if r.createCalls != 0 {
			t.Fatalf("expected 0 entries when context is already cancelled, got %d", r.createCalls)
		}
	})
}
