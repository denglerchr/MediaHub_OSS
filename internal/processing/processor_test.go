package processing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	_ "mediahub_oss/internal/repository/migrations/sqlite"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/storage"
	"mediahub_oss/internal/storage/localstorage"

	"github.com/pressly/goose/v3"
)

type testMockConverter struct {
	canConvertCheck media.ConversionCheck
	readMetaErr     error
	convertStreamErr error
	convertFileErr   error
	previewErr       error
}

func (m *testMockConverter) GetOutputMimeTypes(contentType string) []string {
	return []string{"image/jpeg", "image/png"}
}
func (m *testMockConverter) CanCreatePreview(inputMimeType string) bool {
	return true
}
func (m *testMockConverter) CanConvert(inputMimeType string, opts media.ConversionOptions) media.ConversionCheck {
	return m.canConvertCheck
}
func (m *testMockConverter) ConvertStreamToFile(ctx context.Context, inputData io.ReadSeeker, inputMimeType string, opts media.ConversionOptions) (*os.File, error) {
	if m.convertStreamErr != nil {
		return nil, m.convertStreamErr
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
func (m *testMockConverter) ConvertFile(ctx context.Context, inputPath string, outputPath string, inputMimeType string, opts media.ConversionOptions) error {
	if m.convertFileErr != nil {
		return m.convertFileErr
	}
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
func (m *testMockConverter) ReadMediaFieldsFromStream(ctx context.Context, inputData io.ReadSeeker, contentType string) (map[string]any, error) {
	if m.readMetaErr != nil {
		return nil, m.readMetaErr
	}
	return map[string]any{"width": 100, "height": 200}, nil
}
func (m *testMockConverter) ReadMediaFieldsFromFile(ctx context.Context, filepath string, contentType string) (map[string]any, error) {
	if m.readMetaErr != nil {
		return nil, m.readMetaErr
	}
	return map[string]any{"width": 100, "height": 200}, nil
}
func (m *testMockConverter) CreatePreviewFromStream(ctx context.Context, inputData io.ReadSeeker, outputWriter io.Writer, inputMimeType string) error {
	if m.previewErr != nil {
		return m.previewErr
	}
	_, err := outputWriter.Write([]byte("preview-data"))
	return err
}
func (m *testMockConverter) CreatePreviewFromFile(ctx context.Context, filepath string, outputWriter io.Writer, inputMimeType string) error {
	if m.previewErr != nil {
		return m.previewErr
	}
	_, err := outputWriter.Write([]byte("preview-data"))
	return err
}

type failingPreviewStorage struct {
	*localstorage.LocalStorage
}

func (f *failingPreviewStorage) WritePreview(ctx context.Context, dbID string, entryID int64, data io.Reader) (int64, error) {
	return 0, errors.New("simulated storage failure on preview write")
}

func setupTestProcessor(t *testing.T, conv media.MediaConverter, customStore storage.StorageProvider) (*Processor, repo.Repository, repo.Database) {
	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedFS)
	if err := goose.Up(r.DB, "sqlite"); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	tempDir := t.TempDir()
	baseStorage := &localstorage.LocalStorage{RootPath: tempDir}

	var storageProvider storage.StorageProvider = baseStorage
	if customStore != nil {
		storageProvider = customStore
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proc, err := NewProcessor(r, storageProvider, conv, 2, 4, logger)
	if err != nil {
		t.Fatalf("failed to create processor: %v", err)
	}

	db, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "test_db",
		ContentType: "image",
		Config: repo.DatabaseConfig{
			AutoConversion: "image/png",
			CreatePreview:  false,
		},
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	return proc, r, db
}

// Test 3.7: Orphan Processing Entry on Invalid Conversion
func TestHandleSmallFileSync_CannotConvertCleanup(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: false, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	fileData := bytes.NewReader([]byte("sample image data"))
	req := EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "test.jpg",
	}
	plan := ProcessingPlan{
		WantsConversion: true,
		NeedsConversion: true,
		CanConvert:      false,
		InitMimeType:    "image/jpeg",
		ResultMimeType:  "image/png",
		InitFileName:    "test.jpg",
		FinalFileName:   "test.png",
	}

	_, err := proc.handleSmallFileSync(context.Background(), fileData, db, req, plan)
	if err == nil {
		t.Fatal("expected error from handleSmallFileSync, got nil")
	}

	// Verify that the created entry was transitioned to EntryStatusError (3) and not left in EntryStatusProcessing (0)
	entries, err := r.GetEntries(context.Background(), db.ID, repo.QueryOptions{})
	if err != nil {
		t.Fatalf("failed to fetch entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in DB, found %d", len(entries))
	}
	if entries[0].Status != repo.EntryStatusError {
		t.Fatalf("expected entry status to be EntryStatusError (%v), got %v", repo.EntryStatusError, entries[0].Status)
	}
}

// Test 3.9: MediaFields Preserved on Metadata Extraction Failure
func TestRunConversionAndFinalize_PreservesDefaultMediaFieldsOnMetaError(t *testing.T) {
	conv := &testMockConverter{
		readMetaErr: errors.New("corrupt media metadata"),
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// Create preliminary entry with defaults
	req := EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "test.jpg",
	}
	plan := ProcessingPlan{
		WantsConversion: false,
		NeedsConversion: false,
		InitMimeType:    "image/jpeg",
		ResultMimeType:  "image/jpeg",
		InitFileName:    "test.jpg",
		FinalFileName:   "test.jpg",
	}

	entry, err := proc.createPreliminaryEntry(context.Background(), db, req, plan, repo.EntryStatusProcessing, true)
	if err != nil {
		t.Fatalf("failed to create preliminary entry: %v", err)
	}

	// Write temp file
	tempFile, err := os.CreateTemp("", "test-file-*.jpg")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tempPath := tempFile.Name()
	tempFile.Write([]byte("image content"))
	tempFile.Close()

	proc.runConversionAndFinalize(context.Background(), db, entry, tempPath, plan)

	finalEntry, err := r.GetEntry(context.Background(), db.ID, entry.ID)
	if err != nil {
		t.Fatalf("failed to get final entry: %v", err)
	}

	if finalEntry.Status != repo.EntryStatusReady {
		t.Fatalf("expected entry status Ready, got %v", finalEntry.Status)
	}

	// MediaFields should have default fields (e.g. width, height) rather than empty map
	if len(finalEntry.MediaFields) == 0 {
		t.Fatalf("expected default MediaFields to be preserved on extraction failure, but got empty map: %v", finalEntry.MediaFields)
	}
}

// Test 3.8: Unclosed Pipe Reader on Storage Failure does not hang
func TestGenerateAndStorePreview_StorageErrorDoesNotHang(t *testing.T) {
	conv := &testMockConverter{}
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

	tempDir := t.TempDir()
	baseStorage := &localstorage.LocalStorage{RootPath: tempDir}
	store := &failingPreviewStorage{LocalStorage: baseStorage}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	proc, _ := NewProcessor(r, store, conv, 2, 4, logger)

	db, _ := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "test_db",
		ContentType: "image",
	})

	doneChan := make(chan error, 1)
	go func() {
		inputSeeker := strings.NewReader("sample image data")
		_, err := proc.generateAndStorePreview(context.Background(), db, 123, inputSeeker, "image/jpeg")
		doneChan <- err
	}()

	select {
	case err := <-doneChan:
		if err == nil {
			t.Fatal("expected error from failing preview storage, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("generateAndStorePreview hung indefinitely due to unclosed pipe reader")
	}
}

func TestProcessor_ConvertStream_Success(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	input := strings.NewReader("original image data")
	opts := media.ConversionOptions{
		TargetMimeType: "image/webp",
		Width:          800,
		Height:         480,
		Fit:            "cut",
	}

	stream, size, err := proc.ConvertStream(context.Background(), input, "image/jpeg", opts)
	if err != nil {
		t.Fatalf("unexpected error from ConvertStream: %v", err)
	}
	defer stream.Close()

	outBytes, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read converted output: %v", err)
	}
	if string(outBytes) != "original image data" {
		t.Errorf("expected %q, got %q", "original image data", string(outBytes))
	}
	if size != int64(len("original image data")) {
		t.Errorf("expected size %d, got %d", len("original image data"), size)
	}
}

func TestProcessor_ConvertStream_NonSeekerInput(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// io.NopCloser hides the ReadSeeker interface from strings.NewReader
	plainReader := io.NopCloser(strings.NewReader("streamed data"))
	opts := media.ConversionOptions{TargetMimeType: "image/webp"}

	stream, size, err := proc.ConvertStream(context.Background(), plainReader, "image/jpeg", opts)
	if err != nil {
		t.Fatalf("unexpected error from ConvertStream with non-seeker: %v", err)
	}
	defer stream.Close()

	outBytes, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read converted output: %v", err)
	}
	if string(outBytes) != "streamed data" {
		t.Errorf("expected %q, got %q", "streamed data", string(outBytes))
	}
	if size != int64(len("streamed data")) {
		t.Errorf("expected size %d, got %d", len("streamed data"), size)
	}
}

func TestProcessor_ConvertStream_SlotExhaustion(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// Fill all sync slots up to NFfmpegTotal (setupTestProcessor sets NFfmpegTotal=4)
	proc.NFfmpegTotal = 1
	if !proc.tryReserveSyncSlot() {
		t.Fatal("failed to reserve initial slot")
	}

	input := strings.NewReader("image data")
	opts := media.ConversionOptions{TargetMimeType: "image/webp"}

	_, _, err := proc.ConvertStream(context.Background(), input, "image/jpeg", opts)
	if err == nil {
		t.Fatal("expected error due to slot exhaustion, got nil")
	}

	// Release the slot and verify subsequent ConvertStream succeeds
	proc.releaseSyncSlot()

	stream, size, err := proc.ConvertStream(context.Background(), strings.NewReader("image data"), "image/jpeg", opts)
	if err != nil {
		t.Fatalf("expected ConvertStream to succeed after slot release, got: %v", err)
	}
	defer stream.Close()

	if size != int64(len("image data")) {
		t.Errorf("expected size %d, got %d", len("image data"), size)
	}
}

