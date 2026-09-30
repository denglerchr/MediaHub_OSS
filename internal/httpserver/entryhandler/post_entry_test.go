package entryhandler_test

import (
	"bytes"
	"context"
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

	"mediahub_oss/internal/httpserver/entryhandler"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/media"
	"mediahub_oss/internal/processing"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	_ "mediahub_oss/internal/repository/migrations/sqlite"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/storage/localstorage"

	"github.com/pressly/goose/v3"
)

type mockMediaConverter struct {
	canConvertFunc          func(inputMimeType string, opts media.ConversionOptions) media.ConversionCheck
	convertStreamToFileFunc func(ctx context.Context, inputData io.ReadSeeker, inputMimeType string, opts media.ConversionOptions) (*os.File, error)
}

func (m *mockMediaConverter) GetOutputMimeTypes(contentType string) []string {
	return []string{"image/jpeg"}
}
func (m *mockMediaConverter) CanCreatePreview(inputMimeType string) bool {
	return false
}
func (m *mockMediaConverter) CanConvert(inputMimeType string, opts media.ConversionOptions) media.ConversionCheck {
	if m.canConvertFunc != nil {
		return m.canConvertFunc(inputMimeType, opts)
	}
	if strings.Contains(opts.TargetMimeType, "unsupported") {
		return media.ConversionCheck{CanConvert: false}
	}
	needs := (opts.TargetMimeType != "" && opts.TargetMimeType != inputMimeType) || opts.Width > 0 || opts.Height > 0
	return media.ConversionCheck{CanConvert: true, NeedsConversion: needs}
}
func (m *mockMediaConverter) ConvertStreamToFile(ctx context.Context, inputData io.ReadSeeker, inputMimeType string, opts media.ConversionOptions) (*os.File, error) {
	if m.convertStreamToFileFunc != nil {
		return m.convertStreamToFileFunc(ctx, inputData, inputMimeType, opts)
	}
	tmp, err := os.CreateTemp("", "mock-stream-*.tmp")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(tmp, inputData); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	return tmp, nil
}
func (m *mockMediaConverter) ConvertFile(ctx context.Context, inputPath string, outputPath string, inputMimeType string, opts media.ConversionOptions) error {
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
func (m *mockMediaConverter) ReadMediaFieldsFromStream(ctx context.Context, inputData io.ReadSeeker, contentType string) (map[string]any, error) {
	return map[string]any{"width": 800, "height": 600}, nil
}
func (m *mockMediaConverter) ReadMediaFieldsFromFile(ctx context.Context, filepath string, contentType string) (map[string]any, error) {
	return map[string]any{"width": 800, "height": 600}, nil
}
func (m *mockMediaConverter) CreatePreviewFromStream(ctx context.Context, inputData io.ReadSeeker, outputWriter io.Writer, inputMimeType string) error {
	return nil
}
func (m *mockMediaConverter) CreatePreviewFromFile(ctx context.Context, filepath string, outputWriter io.Writer, inputMimeType string) error {
	return nil
}

func TestPostEntry_LargeFileAsyncUploadWithCleanup(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 1. Setup SQLite repository
	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// 2. Setup Local Storage
	tempDir := t.TempDir()
	storageRoot := filepath.Join(tempDir, "storage")
	_ = os.MkdirAll(storageRoot, 0755)
	store := &localstorage.LocalStorage{RootPath: storageRoot}

	// 3. Create test database
	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "test_images",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	conv := &mockMediaConverter{}
	proc, err := processing.NewProcessor(r, store, conv, 2, 4, logger)
	if err != nil {
		t.Fatalf("failed to create processor: %v", err)
	}

	handler := &entryhandler.EntryHandler{
		Repo:                   r,
		Storage:                store,
		MediaConverter:         conv,
		Processor:              proc,
		Auditor:                &mockAuditor{},
		Logger:                 logger,
		MaxSyncUploadSizeBytes: 1024, // 1KB threshold: files larger than 1KB will spool to disk
	}

	// 4. Create a 50KB payload (exceeds 1KB MaxSyncUploadSizeBytes to force disk spooling)
	largeData := bytes.Repeat([]byte("ABCDEFGHIJ"), 5000) // 50,000 bytes

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file part with explicit image/jpeg Content-Type
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="large_photo.jpg"`)
	partHeader.Set("Content-Type", "image/jpeg")

	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}
	if _, err := part.Write(largeData); err != nil {
		t.Fatalf("Write to form failed: %v", err)
	}

	// Add metadata part
	if err := writer.WriteField("metadata", `{"filename":"custom_photo.jpg"}`); err != nil {
		t.Fatalf("WriteField failed: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/database/"+db.ID.String()+"/entry", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("database_id", db.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "testuser", IsAdmin: true}))

	rec := httptest.NewRecorder()

	// Execute handler (PostEntry will return 202 Accepted and run defer r.MultipartForm.RemoveAll())
	handler.PostEntry(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Wait for the background worker to finish processing the file
	var finalEntry repo.Entry
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			time.Sleep(20 * time.Millisecond)
			entries, err := r.GetEntries(ctx, db.ID, repo.QueryOptions{})
			if err == nil && len(entries) > 0 {
				if entries[0].Status == repo.EntryStatusReady {
					finalEntry = entries[0]
					return
				}
			}
		}
	}()
	wg.Wait()

	if finalEntry.Status != repo.EntryStatusReady {
		t.Fatalf("expected async entry to transition to status 'ready', got: %v", finalEntry.Status)
	}

	// Verify file is stored properly and readable from storage
	storedStream, err := store.Read(ctx, db.ID.String(), finalEntry.ID, 0, -1)
	if err != nil {
		t.Fatalf("failed to read file from storage after async processing: %v", err)
	}
	defer storedStream.Close()

	readBytes, err := io.ReadAll(storedStream)
	if err != nil {
		t.Fatalf("failed to read stored bytes: %v", err)
	}
	if len(readBytes) != len(largeData) {
		t.Errorf("expected stored size %d, got %d", len(largeData), len(readBytes))
	}
}
