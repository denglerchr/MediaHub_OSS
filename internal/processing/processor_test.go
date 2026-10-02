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
	canConvertCheck        media.ConversionCheck
	readMetaErr            error
	convertStreamErr       error
	convertFileErr         error
	previewErr             error
	readMetaStreamCalls    int
	readMetaFileCalls      int
	previewFromFileCalls   int
	lastPreviewFileMime    string
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
	m.readMetaStreamCalls++
	if m.readMetaErr != nil {
		return nil, m.readMetaErr
	}
	return map[string]any{"width": 100, "height": 200}, nil
}
func (m *testMockConverter) ReadMediaFieldsFromFile(ctx context.Context, filepath string, contentType string) (map[string]any, error) {
	m.readMetaFileCalls++
	if m.readMetaErr != nil {
		return nil, m.readMetaErr
	}
	return map[string]any{"width": 300, "height": 400}, nil
}
func (m *testMockConverter) CreatePreviewFromStream(ctx context.Context, inputData io.ReadSeeker, outputWriter io.Writer, inputMimeType string) error {
	if m.previewErr != nil {
		return m.previewErr
	}
	_, err := outputWriter.Write([]byte("preview-data"))
	return err
}
func (m *testMockConverter) CreatePreviewFromFile(ctx context.Context, filepath string, outputWriter io.Writer, inputMimeType string) error {
	m.previewFromFileCalls++
	m.lastPreviewFileMime = inputMimeType
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

type failingWriteStorage struct {
	*localstorage.LocalStorage
}

func (f *failingWriteStorage) Write(ctx context.Context, dbID string, entryID int64, data io.Reader) (int64, error) {
	return 0, errors.New("simulated storage write failure")
}

func TestQueueFile_StorageFailureCleansUpDBEntry(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	tempDir := t.TempDir()
	baseStorage := &localstorage.LocalStorage{RootPath: tempDir}
	store := &failingWriteStorage{LocalStorage: baseStorage}

	proc, r, db := setupTestProcessor(t, conv, store)
	defer r.Close()

	req := EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "queued.jpg",
	}
	plan := ProcessingPlan{
		WantsConversion: true,
		NeedsConversion: true,
		CanConvert:      true,
		InitMimeType:    "image/jpeg",
		TargetMimeType:  "image/png",
		ResultMimeType:  "image/png",
		InitFileName:    "queued.jpg",
		FinalFileName:   "queued.png",
	}

	_, err := proc.queueFile(context.Background(), strings.NewReader("image bytes"), db, req, plan)
	if err == nil {
		t.Fatal("expected error from queueFile when storage write fails, got nil")
	}

	queuedCount, err := r.CountEntriesByStatus(context.Background(), db.ID, repo.EntryStatusQueued)
	if err != nil {
		t.Fatalf("failed to count queued entries: %v", err)
	}
	if queuedCount != 0 {
		t.Fatalf("expected 0 orphaned queued entries after storage write failure, got %d", queuedCount)
	}
}

func TestRunConversionAndFinalize_FailureCleansUpStorage(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
		convertFileErr:  errors.New("simulated ffmpeg conversion error"),
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	req := EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "queued.jpg",
	}
	plan := ProcessingPlan{
		WantsConversion: true,
		NeedsConversion: true,
		CanConvert:      true,
		InitMimeType:    "image/jpeg",
		TargetMimeType:  "image/png",
		ResultMimeType:  "image/png",
		InitFileName:    "queued.jpg",
		FinalFileName:   "queued.png",
	}

	// Queue the entry so raw file is written to storage
	queuedEntry, err := proc.queueFile(context.Background(), strings.NewReader("raw queued bytes"), db, req, plan)
	if err != nil {
		t.Fatalf("failed to queue file: %v", err)
	}

	// Verify file exists in storage before worker runs
	if _, err := proc.Storage.Stat(context.Background(), db.ID.String(), queuedEntry.ID); err != nil {
		t.Fatalf("expected queued file in storage before worker failure, got err: %v", err)
	}

	proc.runWorkerForClaimedEntry(context.Background(), db.ID, queuedEntry)

	// Entry should be marked Error and storage file should be deleted
	updated, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("failed to fetch entry: %v", err)
	}
	if updated.Status != repo.EntryStatusError {
		t.Fatalf("expected EntryStatusError, got %v", updated.Status)
	}
	if _, err := proc.Storage.Stat(context.Background(), db.ID.String(), queuedEntry.ID); err == nil {
		t.Fatal("expected raw file in storage to be deleted after worker conversion failure")
	}
}

type failingReadStorage struct {
	*localstorage.LocalStorage
	failRead bool
}

func (f *failingReadStorage) Read(ctx context.Context, dbID string, entryID int64, offset int64, length int64) (io.ReadCloser, error) {
	if f.failRead {
		return nil, errors.New("simulated transient storage read failure")
	}
	return f.LocalStorage.Read(ctx, dbID, entryID, offset, length)
}

func TestRunWorkerForClaimedEntry_TransientReadErrorRequeues(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	tempDir := t.TempDir()
	baseStorage := &localstorage.LocalStorage{RootPath: tempDir}
	store := &failingReadStorage{LocalStorage: baseStorage, failRead: true}

	proc, r, db := setupTestProcessor(t, conv, store)
	defer r.Close()

	req := EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "test.jpg",
	}
	plan := ProcessingPlan{
		WantsConversion: false,
		NeedsConversion: false,
		CanConvert:      true,
		InitMimeType:    "image/jpeg",
		ResultMimeType:  "image/jpeg",
		InitFileName:    "test.jpg",
		FinalFileName:   "test.jpg",
	}

	// Queue entry
	queuedEntry, err := proc.queueFile(context.Background(), strings.NewReader("queued payload"), db, req, plan)
	if err != nil {
		t.Fatalf("failed to queue file: %v", err)
	}

	// Simulate claiming the entry
	claimed, err := r.ClaimQueuedEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil || !claimed {
		t.Fatalf("failed to claim entry: %v", err)
	}

	claimedEntry, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("failed to get claimed entry: %v", err)
	}

	// Worker encounters transient read error
	proc.runWorkerForClaimedEntry(context.Background(), db.ID, claimedEntry)

	// Entry must be requeued (EntryStatusQueued), NOT marked Error, and file must NOT be deleted from storage
	updated, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("failed to fetch entry: %v", err)
	}
	if updated.Status != repo.EntryStatusQueued {
		t.Fatalf("expected EntryStatusQueued after transient error, got %v", updated.Status)
	}

	store.failRead = false
	if _, err := proc.Storage.Stat(context.Background(), db.ID.String(), queuedEntry.ID); err != nil {
		t.Fatalf("expected queued file to be preserved in storage, got err: %v", err)
	}
}

func TestProcessEntry_NoConversionBypassesFfmpegSlots(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	imageDB, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "passthrough_images",
		ContentType: "image",
		NMaxQueued:  0,
		Config: repo.DatabaseConfig{
			AutoConversion: "",
			CreatePreview:  false,
		},
	})
	if err != nil {
		t.Fatalf("failed to create image database: %v", err)
	}

	fileDB, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "raw_files",
		ContentType: "file",
		NMaxQueued:  0,
	})
	if err != nil {
		t.Fatalf("failed to create file database: %v", err)
	}

	// Exhaust all FFmpeg sync & async conversion slots
	proc.NFfmpegTotal = 1
	proc.NFfmpegAsync = 1
	if !proc.tryReserveAsyncSlot() {
		t.Fatal("failed to reserve slot")
	}
	defer proc.releaseAsyncSlot()

	// 1. Image upload requiring no conversion bypasses exhausted conversion slots
	imgEntry, wasSync, err := proc.ProcessEntry(context.Background(), imageDB, EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "photo.jpg",
	}, strings.NewReader("jpeg bytes"), "image/jpeg", "photo.jpg")
	if err != nil {
		t.Fatalf("expected unconverted image upload to bypass exhausted conversion slots, got err: %v", err)
	}
	if !wasSync || imgEntry.Status != repo.EntryStatusReady {
		t.Errorf("expected sync Ready image entry, got wasSync=%v status=%v", wasSync, imgEntry.Status)
	}
	if conv.readMetaStreamCalls != 1 {
		t.Errorf("expected 1 metadata probe call for image DB, got %d", conv.readMetaStreamCalls)
	}

	// 2. File database upload bypasses exhausted conversion slots and skips metadata probing
	conv.readMetaStreamCalls = 0
	fileEntry, wasSync, err := proc.ProcessEntry(context.Background(), fileDB, EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "notes.txt",
	}, strings.NewReader("hello world"), "text/plain", "notes.txt")
	if err != nil {
		t.Fatalf("expected file database upload to bypass exhausted FFmpeg slots, got err: %v", err)
	}
	if !wasSync || fileEntry.Status != repo.EntryStatusReady {
		t.Errorf("expected sync Ready file entry, got wasSync=%v status=%v", wasSync, fileEntry.Status)
	}
	if conv.readMetaStreamCalls != 0 || conv.readMetaFileCalls != 0 {
		t.Errorf("expected 0 metadata probe calls for 'file' database, got stream=%d file=%d", conv.readMetaStreamCalls, conv.readMetaFileCalls)
	}
}

func TestHandleSmallFileSync_ConvertedFileUsesFileProbeAndPreview(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	req := EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "photo.jpg",
	}
	plan := ProcessingPlan{
		WantsConversion: true,
		NeedsConversion: true,
		CanConvert:      true,
		WantsPreview:    true,
		CanGenPreview:   true,
		InitMimeType:    "image/jpeg",
		TargetMimeType:  "image/png",
		ResultMimeType:  "image/png",
		InitFileName:    "photo.jpg",
		FinalFileName:   "photo.png",
	}

	entry, err := proc.handleSmallFileSync(context.Background(), strings.NewReader("image data"), db, req, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Metadata must be extracted from converted file (width=300, height=400), not stream (width=100, height=200)
	if conv.readMetaFileCalls != 1 || conv.readMetaStreamCalls != 0 {
		t.Errorf("expected 1 ReadMediaFieldsFromFile call and 0 stream calls, got file=%d stream=%d", conv.readMetaFileCalls, conv.readMetaStreamCalls)
	}
	if entry.MediaFields["width"] != 300 && entry.MediaFields["width"] != uint64(300) && entry.MediaFields["width"] != int64(300) {
		t.Errorf("expected converted width 300, got %v (%T)", entry.MediaFields["width"], entry.MediaFields["width"])
	}

	// Wait for background preview from temp file to finish
	for i := 0; i < 50; i++ {
		updated, err := r.GetEntry(context.Background(), db.ID, entry.ID)
		if err == nil && updated.Status == repo.EntryStatusReady {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if conv.previewFromFileCalls != 1 {
		t.Errorf("expected CreatePreviewFromFile to be called once using converted tempStream, got %d", conv.previewFromFileCalls)
	}
	if conv.lastPreviewFileMime != "image/png" {
		t.Errorf("expected preview MIME 'image/png', got %q", conv.lastPreviewFileMime)
	}
}

func TestRunConversionAndFinalize_PassesResultMimeTypeToPreview(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	req := EntryRequest{
		Timestamp: time.Now().UnixMilli(),
		FileName:  "photo.jpg",
	}
	// Simulate case where WantsConversion was true (TargetMimeType="image/webp") but conversion was not performed (ResultMimeType="image/jpeg")
	plan := ProcessingPlan{
		WantsConversion: true,
		NeedsConversion: false,
		CanConvert:      false,
		WantsPreview:    true,
		CanGenPreview:   true,
		InitMimeType:    "image/jpeg",
		TargetMimeType:  "image/webp",
		ResultMimeType:  "image/jpeg",
		InitFileName:    "photo.jpg",
		FinalFileName:   "photo.jpg",
	}

	entry, err := proc.createPreliminaryEntry(context.Background(), db, req, plan, repo.EntryStatusProcessing, false)
	if err != nil {
		t.Fatalf("failed to create preliminary entry: %v", err)
	}

	tempFile, err := os.CreateTemp("", "test-preview-mime-*.jpg")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tempPath := tempFile.Name()
	tempFile.Write([]byte("jpeg content"))
	tempFile.Close()

	proc.runConversionAndFinalize(context.Background(), db, entry, tempPath, plan)

	if conv.lastPreviewFileMime != "image/jpeg" {
		t.Errorf("expected CreatePreviewFromFile to receive ResultMimeType 'image/jpeg', got %q", conv.lastPreviewFileMime)
	}
}

func TestQueueMonitor_TriggersAndProcessesQueuedEntries(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	ctx := context.Background()

	plan := ProcessingPlan{
		WantsConversion: false,
		NeedsConversion: false,
		CanConvert:      true,
		InitMimeType:    "image/jpeg",
		ResultMimeType:  "image/jpeg",
		InitFileName:    "test.jpg",
		FinalFileName:   "test.jpg",
	}

	// Temporarily disable async slot capacity so queueing does not immediately auto-dispatch
	proc.mu.Lock()
	proc.NFfmpegAsync = 0
	proc.mu.Unlock()

	// Queue 2 entries
	e1, err := proc.queueFile(ctx, strings.NewReader("fake-image-1"), db, EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e1.jpg"}, plan)
	if err != nil {
		t.Fatalf("failed to queue e1: %v", err)
	}
	e2, err := proc.queueFile(ctx, strings.NewReader("fake-image-2"), db, EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e2.jpg"}, plan)
	if err != nil {
		t.Fatalf("failed to queue e2: %v", err)
	}

	stats, err := r.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.QueuedCount != 2 {
		t.Fatalf("expected QueuedCount == 2, got %d", stats.QueuedCount)
	}

	// Restore capacity and trigger queue check
	proc.mu.Lock()
	proc.NFfmpegAsync = 2
	proc.mu.Unlock()

	proc.TriggerQueueWorkersIfPossible(ctx)

	// Wait for workers to process both entries
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ent1, err1 := r.GetEntry(ctx, db.ID, e1.ID)
		ent2, err2 := r.GetEntry(ctx, db.ID, e2.ID)
		if err1 == nil && err2 == nil && ent1.Status == repo.EntryStatusReady && ent2.Status == repo.EntryStatusReady {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	ent1, _ := r.GetEntry(ctx, db.ID, e1.ID)
	ent2, _ := r.GetEntry(ctx, db.ID, e2.ID)
	if ent1.Status != repo.EntryStatusReady || ent2.Status != repo.EntryStatusReady {
		t.Fatalf("entries not ready: e1=%v, e2=%v", ent1.Status, ent2.Status)
	}

	stats, _ = r.GetDatabaseStats(ctx, db.ID)
	if stats.QueuedCount != 0 {
		t.Errorf("expected QueuedCount == 0, got %d", stats.QueuedCount)
	}
}

func TestParallelQueueWorkerDispatch(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, db1 := setupTestProcessor(t, conv, nil)
	defer r.Close()

	ctx := context.Background()

	db2, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "test_db_2",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create db2: %v", err)
	}

	plan := ProcessingPlan{
		WantsConversion: false,
		NeedsConversion: false,
		CanConvert:      true,
		InitMimeType:    "image/jpeg",
		ResultMimeType:  "image/jpeg",
		InitFileName:    "test.jpg",
		FinalFileName:   "test.jpg",
	}

	// Queue entries in both databases
	e1, err := proc.queueFile(ctx, strings.NewReader("fake-image-1"), db1, EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e1.jpg"}, plan)
	if err != nil {
		t.Fatalf("failed to queue e1: %v", err)
	}
	e2, err := proc.queueFile(ctx, strings.NewReader("fake-image-2"), db2, EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e2.jpg"}, plan)
	if err != nil {
		t.Fatalf("failed to queue e2: %v", err)
	}

	// Trigger queue workers
	proc.TriggerQueueWorkersIfPossible(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ent1, _ := r.GetEntry(ctx, db1.ID, e1.ID)
		ent2, _ := r.GetEntry(ctx, db2.ID, e2.ID)
		if ent1.Status == repo.EntryStatusReady && ent2.Status == repo.EntryStatusReady {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	ent1, _ := r.GetEntry(ctx, db1.ID, e1.ID)
	ent2, _ := r.GetEntry(ctx, db2.ID, e2.ID)
	if ent1.Status != repo.EntryStatusReady || ent2.Status != repo.EntryStatusReady {
		t.Fatalf("entries not ready: e1=%v, e2=%v", ent1.Status, ent2.Status)
	}

	stats1, _ := r.GetDatabaseStats(ctx, db1.ID)
	stats2, _ := r.GetDatabaseStats(ctx, db2.ID)
	if stats1.QueuedCount != 0 || stats2.QueuedCount != 0 {
		t.Errorf("expected QueuedCount == 0, got db1=%d db2=%d", stats1.QueuedCount, stats2.QueuedCount)
	}
}

func TestStartQueueMonitor_ContextCancel(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		proc.StartQueueMonitor(ctx)
		close(done)
	}()

	// Cancel context after short duration
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Succeeded: StartQueueMonitor exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("StartQueueMonitor did not terminate after context cancellation")
	}
}




