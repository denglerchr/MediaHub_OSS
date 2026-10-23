package processing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	_ "mediahub_oss/internal/repository/migrations/sqlite"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/storage"
	"mediahub_oss/internal/storage/localstorage"

	"github.com/pressly/goose/v3"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

type testMockConverter struct {
	canConvertCheck      media.ConversionCheck
	readMetaErr          error
	convertStreamErr     error
	convertFileErr       error
	previewErr           error
	readMetaStreamCalls  int
	readMetaFileCalls    int
	previewFromFileCalls int
	lastPreviewFileMime  string
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

// partialFailingConverter writes a few preview bytes and then fails, simulating
// an FFmpeg crash mid-generation.
type partialFailingConverter struct {
	*testMockConverter
}

func (m *partialFailingConverter) CreatePreviewFromStream(ctx context.Context, inputData io.ReadSeeker, outputWriter io.Writer, inputMimeType string) error {
	if _, err := outputWriter.Write([]byte("partial-preview-")); err != nil {
		return err
	}
	return errors.New("simulated ffmpeg failure mid-preview")
}

func (m *partialFailingConverter) CreatePreviewFromFile(ctx context.Context, filepath string, outputWriter io.Writer, inputMimeType string) error {
	return m.CreatePreviewFromStream(ctx, strings.NewReader(""), outputWriter, inputMimeType)
}

type failingWriteStorage struct {
	*localstorage.LocalStorage
}

func (f *failingWriteStorage) Write(ctx context.Context, dbID string, entryID int64, data io.Reader) (int64, error) {
	return 0, errors.New("simulated storage write failure")
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

// failingUpdateRepo fails the next n UpdateEntry calls before delegating to the
// embedded repository, simulating transient database failures.
type failingUpdateRepo struct {
	repo.Repository
	failNext int
}

func (r *failingUpdateRepo) UpdateEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) (repo.Entry, error) {
	if r.failNext > 0 {
		r.failNext--
		return repo.Entry{}, errors.New("simulated database failure on update")
	}
	return r.Repository.UpdateEntry(ctx, dbID, entry)
}

// ---------------------------------------------------------------------------
// Test setup
// ---------------------------------------------------------------------------

func setupTestProcessor(t *testing.T, conv media.MediaConverter, customStore storage.StorageProvider) (*Processor, repo.Repository, repo.Database) {
	t.Helper()

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

	storageProvider := storage.StorageProvider(&localstorage.LocalStorage{RootPath: t.TempDir()})
	if customStore != nil {
		storageProvider = customStore
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proc := NewProcessor(r, storageProvider, conv, 2, 4, logger)

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

func conversionPlan() ProcessingPlan {
	return ProcessingPlan{
		WantsConversion: true,
		NeedsConversion: true,
		CanConvert:      true,
		InitMimeType:    "image/jpeg",
		TargetMimeType:  "image/png",
		ResultMimeType:  "image/png",
		InitFileName:    "photo.jpg",
		FinalFileName:   "photo.png",
	}
}

func passthroughPlan() ProcessingPlan {
	return ProcessingPlan{
		WantsConversion: false,
		NeedsConversion: false,
		CanConvert:      true,
		InitMimeType:    "image/jpeg",
		ResultMimeType:  "image/jpeg",
		InitFileName:    "photo.jpg",
		FinalFileName:   "photo.jpg",
	}
}

func writeTempFile(t *testing.T, pattern string, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		t.Fatalf("failed to write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

// waitForStatus polls until the entry reaches the desired status or the timeout expires.
func waitForStatus(t *testing.T, r repo.Repository, dbID repo.ULID, entryID int64, status repo.EntryStatus) repo.Entry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entry, err := r.GetEntry(context.Background(), dbID, entryID)
		if err == nil && entry.Status == status {
			return entry
		}
		time.Sleep(10 * time.Millisecond)
	}
	entry, _ := r.GetEntry(context.Background(), dbID, entryID)
	t.Fatalf("entry %d did not reach status %v (current: %v)", entryID, status, entry.Status)
	return repo.Entry{}
}

// ---------------------------------------------------------------------------
// Incoming pipeline: synchronous path
// ---------------------------------------------------------------------------

func TestProcessSynchronously_CannotConvertMarksEntryError(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: false, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	plan := conversionPlan()
	plan.CanConvert = false

	_, err := proc.processSynchronously(context.Background(), strings.NewReader("sample image data"), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"}, plan)
	if err == nil {
		t.Fatal("expected error from processSynchronously, got nil")
	}

	// Unconvertible plans must fail fast without creating any entry in the database.
	entries, err := r.GetEntries(context.Background(), db.ID, repo.QueryOptions{})
	if err != nil {
		t.Fatalf("failed to fetch entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries in DB after fast failure, found %d", len(entries))
	}
}

func TestProcessIncomingEntry_CannotConvertFailsFastWithoutDBEntryOrQueue(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: false, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	db.NMaxQueued = 10

	_, _, err := proc.ProcessIncomingEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"},
		strings.NewReader("sample image data"), "image/jpeg", "photo.jpg")
	if err == nil {
		t.Fatal("expected error from ProcessIncomingEntry when conversion is unsupported, got nil")
	}

	entries, err := r.GetEntries(context.Background(), db.ID, repo.QueryOptions{})
	if err != nil {
		t.Fatalf("failed to fetch entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries in DB, found %d", len(entries))
	}
}

func TestProcessSynchronously_ConvertedFileUsesFileProbeAndPreview(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	plan := conversionPlan()
	plan.WantsPreview = true
	plan.CanGenPreview = true

	entry, err := proc.processSynchronously(context.Background(), strings.NewReader("image data"), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"}, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Metadata must be extracted from the converted temp file (width=300), not from the original stream (width=100).
	if conv.readMetaFileCalls != 1 || conv.readMetaStreamCalls != 0 {
		t.Errorf("expected 1 ReadMediaFieldsFromFile call and 0 stream calls, got file=%d stream=%d", conv.readMetaFileCalls, conv.readMetaStreamCalls)
	}
	if entry.MediaFields["width"] != 300 {
		t.Errorf("expected converted width 300, got %v (%T)", entry.MediaFields["width"], entry.MediaFields["width"])
	}

	// Wait for the background preview to finalize the entry.
	waitForStatus(t, r, db.ID, entry.ID, repo.EntryStatusReady)

	if conv.previewFromFileCalls != 1 {
		t.Errorf("expected CreatePreviewFromFile to be called once, got %d", conv.previewFromFileCalls)
	}
	if conv.lastPreviewFileMime != "image/png" {
		t.Errorf("expected preview MIME 'image/png', got %q", conv.lastPreviewFileMime)
	}
}

func TestProcessIncomingEntry_SmallFileSyncSuccess(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	entry, synchronous, err := proc.ProcessIncomingEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"},
		strings.NewReader("jpeg bytes"), "image/jpeg", "photo.jpg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !synchronous {
		t.Error("expected synchronous=true for an in-memory upload")
	}
	if entry.Status != repo.EntryStatusReady {
		t.Errorf("expected status ready (no preview configured), got %v", entry.Status)
	}
	if entry.MimeType != "image/png" || entry.FileName != "photo.png" {
		t.Errorf("expected converted mime/name image/png/photo.png, got %s/%s", entry.MimeType, entry.FileName)
	}

	// The stored file must contain the (mock-)converted bytes.
	stream, err := proc.Storage.Read(context.Background(), db.ID.String(), entry.ID, 0, -1)
	if err != nil {
		t.Fatalf("failed to read stored file: %v", err)
	}
	defer stream.Close()
	data, _ := io.ReadAll(stream)
	if string(data) != "jpeg bytes" {
		t.Errorf("expected stored content %q, got %q", "jpeg bytes", string(data))
	}
}

func TestProcessIncomingEntry_NoConversionBypassesFfmpegSlots(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	imageDB, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "passthrough_images",
		ContentType: "image",
		NMaxQueued:  0,
		Config:      repo.DatabaseConfig{AutoConversion: "", CreatePreview: false},
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

	// Exhaust all conversion slots.
	proc.NFfmpegTotal = 1
	proc.NFfmpegAsync = 1
	if !proc.tryReserveAsyncSlot() {
		t.Fatal("failed to reserve slot")
	}
	defer proc.releaseAsyncSlot()

	// 1. Image upload requiring no conversion bypasses the exhausted slots.
	imgEntry, wasSync, err := proc.ProcessIncomingEntry(context.Background(), imageDB,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"},
		strings.NewReader("jpeg bytes"), "image/jpeg", "photo.jpg")
	if err != nil {
		t.Fatalf("expected unconverted image upload to bypass exhausted slots, got err: %v", err)
	}
	if !wasSync || imgEntry.Status != repo.EntryStatusReady {
		t.Errorf("expected sync ready image entry, got wasSync=%v status=%v", wasSync, imgEntry.Status)
	}
	if conv.readMetaStreamCalls != 1 {
		t.Errorf("expected 1 metadata probe call for image DB, got %d", conv.readMetaStreamCalls)
	}

	// 2. File database upload bypasses the slots and skips metadata probing.
	conv.readMetaStreamCalls = 0
	fileEntry, wasSync, err := proc.ProcessIncomingEntry(context.Background(), fileDB,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "notes.txt"},
		strings.NewReader("hello world"), "text/plain", "notes.txt")
	if err != nil {
		t.Fatalf("expected file database upload to bypass exhausted slots, got err: %v", err)
	}
	if !wasSync || fileEntry.Status != repo.EntryStatusReady {
		t.Errorf("expected sync ready file entry, got wasSync=%v status=%v", wasSync, fileEntry.Status)
	}
	if conv.readMetaStreamCalls != 0 || conv.readMetaFileCalls != 0 {
		t.Errorf("expected 0 metadata probe calls for 'file' database, got stream=%d file=%d", conv.readMetaStreamCalls, conv.readMetaFileCalls)
	}
}

func TestProcessIncomingEntry_BadMimeTypeRejected(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	_, _, err := proc.ProcessIncomingEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "evil.exe"},
		strings.NewReader("binary"), "application/x-msdownload", "evil.exe")
	if !errors.Is(err, customerrors.ErrBadMimeType) {
		t.Fatalf("expected ErrBadMimeType, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Incoming pipeline: queueing
// ---------------------------------------------------------------------------

func TestProcessIncomingEntry_QueuesWhenSlotsExhausted(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	db.NMaxQueued = 10

	// Occupy the only sync slot so the upload cannot be processed immediately.
	proc.NFfmpegTotal = 1
	if !proc.tryReserveSyncSlot() {
		t.Fatal("failed to reserve slot")
	}
	defer proc.releaseSyncSlot()

	entry, synchronous, err := proc.ProcessIncomingEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"},
		strings.NewReader("jpeg bytes"), "image/jpeg", "photo.jpg")
	if err != nil {
		t.Fatalf("expected upload to be queued, got err: %v", err)
	}
	if synchronous {
		t.Error("expected synchronous=false for a queued upload")
	}
	if entry.Status != repo.EntryStatusQueued {
		t.Errorf("expected status queued, got %v", entry.Status)
	}

	stats, err := r.GetDatabaseStats(context.Background(), db.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.QueuedCount != 1 {
		t.Errorf("expected QueuedCount == 1, got %d", stats.QueuedCount)
	}

	// The original file must be stored so a queue worker can pick it up.
	if _, err := proc.Storage.Stat(context.Background(), db.ID.String(), entry.ID); err != nil {
		t.Errorf("expected queued file in storage, got err: %v", err)
	}
}

func TestProcessIncomingEntry_QueueFullReturnsUnavailable(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// No queue capacity in this database (NMaxQueued defaults to 0).

	// Occupy the only sync slot.
	proc.NFfmpegTotal = 1
	if !proc.tryReserveSyncSlot() {
		t.Fatal("failed to reserve slot")
	}
	defer proc.releaseSyncSlot()

	_, _, err := proc.ProcessIncomingEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"},
		strings.NewReader("jpeg bytes"), "image/jpeg", "photo.jpg")
	if !errors.Is(err, customerrors.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestQueueIncomingFile_StorageFailureCleansUpDBEntry(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	tempDir := t.TempDir()
	store := &failingWriteStorage{LocalStorage: &localstorage.LocalStorage{RootPath: tempDir}}

	proc, r, db := setupTestProcessor(t, conv, store)
	defer r.Close()

	db.NMaxQueued = 10

	_, err := proc.queueIncomingFile(context.Background(), strings.NewReader("image bytes"), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "queued.jpg"}, conversionPlan())
	if err == nil {
		t.Fatal("expected error from queueIncomingFile when storage write fails, got nil")
	}

	queuedCount, err := r.CountEntriesByStatus(context.Background(), db.ID, repo.EntryStatusQueued)
	if err != nil {
		t.Fatalf("failed to count queued entries: %v", err)
	}
	if queuedCount != 0 {
		t.Fatalf("expected 0 orphaned queued entries after storage write failure, got %d", queuedCount)
	}

	// The entry must be completely gone (or at worst marked as error), never queued.
	entries, err := r.GetEntries(context.Background(), db.ID, repo.QueryOptions{})
	if err != nil {
		t.Fatalf("failed to fetch entries: %v", err)
	}
	for _, e := range entries {
		if e.Status == repo.EntryStatusQueued || e.Status == repo.EntryStatusProcessing {
			t.Errorf("found orphaned entry with status %v", e.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// Incoming pipeline: asynchronous path
// ---------------------------------------------------------------------------

func TestProcessIncomingEntry_LargeFileAsyncSuccess(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	payload := []byte("large image bytes")
	tempPath := writeTempFile(t, "mh-upload-*.jpg", payload)
	file, err := os.Open(tempPath)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}

	entry, synchronous, err := proc.ProcessIncomingEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"},
		file, "image/jpeg", "photo.jpg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if synchronous {
		t.Error("expected synchronous=false for a disk-spooled upload")
	}
	if entry.Status != repo.EntryStatusProcessing {
		t.Errorf("expected status processing, got %v", entry.Status)
	}

	finalEntry := waitForStatus(t, r, db.ID, entry.ID, repo.EntryStatusReady)
	if finalEntry.MimeType != "image/png" || finalEntry.FileName != "photo.png" {
		t.Errorf("expected converted mime/name image/png/photo.png, got %s/%s", finalEntry.MimeType, finalEntry.FileName)
	}

	// The original upload temp file must have been claimed (moved) by the worker.
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("expected original temp file to be moved away, stat err: %v", err)
	}
}

func TestProcessIncomingEntry_LargeFilePassthroughReservesNoSlot(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// Exhaust all async capacity; passthrough must still work.
	proc.NFfmpegAsync = 0

	payload := []byte("large unconverted bytes")
	tempPath := writeTempFile(t, "mh-upload-*.jpg", payload)
	file, err := os.Open(tempPath)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}

	entry, synchronous, err := proc.ProcessIncomingEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"},
		file, "image/jpeg", "photo.jpg")
	if err != nil {
		t.Fatalf("expected large passthrough to succeed without slots, got err: %v", err)
	}
	if synchronous {
		t.Error("expected synchronous=false for a disk-spooled upload")
	}

	finalEntry := waitForStatus(t, r, db.ID, entry.ID, repo.EntryStatusReady)
	if finalEntry.MimeType != "image/jpeg" {
		t.Errorf("expected unconverted mime image/jpeg, got %s", finalEntry.MimeType)
	}
}

// ---------------------------------------------------------------------------
// Outgoing pipeline
// ---------------------------------------------------------------------------

func TestProcessOutgoingEntry_Success(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	input := strings.NewReader("original image data")
	opts := media.ConversionOptions{TargetMimeType: "image/webp", Width: 800, Height: 480, Fit: "cut"}

	stream, size, err := proc.ProcessOutgoingEntry(context.Background(), input, "image/jpeg", opts)
	if err != nil {
		t.Fatalf("unexpected error from ProcessOutgoingEntry: %v", err)
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

func TestProcessOutgoingEntry_NonSeekerInput(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// io.NopCloser hides the ReadSeeker interface from strings.NewReader.
	plainReader := io.NopCloser(strings.NewReader("streamed data"))
	opts := media.ConversionOptions{TargetMimeType: "image/webp"}

	stream, size, err := proc.ProcessOutgoingEntry(context.Background(), plainReader, "image/jpeg", opts)
	if err != nil {
		t.Fatalf("unexpected error from ProcessOutgoingEntry with non-seeker: %v", err)
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

func TestProcessOutgoingEntry_SlotExhaustion(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	proc.NFfmpegTotal = 1
	if !proc.tryReserveSyncSlot() {
		t.Fatal("failed to reserve initial slot")
	}

	_, _, err := proc.ProcessOutgoingEntry(context.Background(), strings.NewReader("image data"), "image/jpeg",
		media.ConversionOptions{TargetMimeType: "image/webp"})
	if !errors.Is(err, customerrors.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable due to slot exhaustion, got %v", err)
	}

	// Release the slot and verify a subsequent call succeeds.
	proc.releaseSyncSlot()

	stream, size, err := proc.ProcessOutgoingEntry(context.Background(), strings.NewReader("image data"), "image/jpeg",
		media.ConversionOptions{TargetMimeType: "image/webp"})
	if err != nil {
		t.Fatalf("expected ProcessOutgoingEntry to succeed after slot release, got: %v", err)
	}
	defer stream.Close()

	if size != int64(len("image data")) {
		t.Errorf("expected size %d, got %d", len("image data"), size)
	}
}

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

	store := &failingPreviewStorage{LocalStorage: &localstorage.LocalStorage{RootPath: t.TempDir()}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proc := NewProcessor(r, store, conv, 2, 4, logger)

	db, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "test_db",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	doneChan := make(chan error, 1)
	go func() {
		_, err := proc.generateAndStorePreview(context.Background(), db, 123, "image/jpeg",
			func(ctx context.Context, w io.Writer) error {
				return conv.CreatePreviewFromStream(ctx, strings.NewReader("sample image data"), w, "image/jpeg")
			})
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

func TestGenerateAndStorePreview_PartialPreviewIsDiscardedOnGeneratorError(t *testing.T) {
	conv := &partialFailingConverter{&testMockConverter{}}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	const entryID = int64(42)

	_, err := proc.generateAndStorePreview(context.Background(), db, entryID, "image/jpeg",
		func(ctx context.Context, w io.Writer) error {
			return conv.CreatePreviewFromStream(ctx, strings.NewReader("image data"), w, "image/jpeg")
		})
	if err == nil {
		t.Fatal("expected error when preview generation fails mid-stream, got nil")
	}

	// The partially generated preview must not be left behind in storage:
	// otherwise it would be served to clients despite PreviewSize == 0.
	if _, err := proc.Storage.StatPreview(context.Background(), db.ID.String(), entryID); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected no preview file in storage after generator failure, got err: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Background finalization pipeline
// ---------------------------------------------------------------------------

func TestFinalizeEntryFile_PreservesDefaultMediaFieldsOnMetaError(t *testing.T) {
	conv := &testMockConverter{
		readMetaErr: errors.New("corrupt media metadata"),
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	plan := passthroughPlan()
	entry, err := proc.createPreliminaryEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"}, plan, true)
	if err != nil {
		t.Fatalf("failed to create preliminary entry: %v", err)
	}

	tempPath := writeTempFile(t, "test-file-*.jpg", []byte("image content"))
	proc.finalizeEntryFile(context.Background(), db, entry, tempPath, plan)

	finalEntry, err := r.GetEntry(context.Background(), db.ID, entry.ID)
	if err != nil {
		t.Fatalf("failed to get final entry: %v", err)
	}
	if finalEntry.Status != repo.EntryStatusReady {
		t.Fatalf("expected entry status ready, got %v", finalEntry.Status)
	}
	if len(finalEntry.MediaFields) == 0 {
		t.Fatalf("expected default MediaFields to be preserved on extraction failure, got empty map: %v", finalEntry.MediaFields)
	}
}

func TestFinalizeEntryFile_PassesResultMimeTypeToPreview(t *testing.T) {
	conv := &testMockConverter{}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// WantsConversion with CanConvert=false and NeedsConversion=false: the original
	// file is stored, so the preview must be generated from the result (= input) MIME.
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

	entry, err := proc.createPreliminaryEntry(context.Background(), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"}, plan, false)
	if err != nil {
		t.Fatalf("failed to create preliminary entry: %v", err)
	}

	tempPath := writeTempFile(t, "test-preview-mime-*.jpg", []byte("jpeg content"))
	proc.finalizeEntryFile(context.Background(), db, entry, tempPath, plan)

	if conv.lastPreviewFileMime != "image/jpeg" {
		t.Errorf("expected CreatePreviewFromFile to receive ResultMimeType 'image/jpeg', got %q", conv.lastPreviewFileMime)
	}
}

func TestFinalizeEntryFile_FailurePreservesStorage(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
		convertFileErr:  errors.New("simulated ffmpeg conversion error"),
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	db.NMaxQueued = 10

	// Queue the entry so the raw original file is written to storage.
	queuedEntry, err := proc.queueIncomingFile(context.Background(), strings.NewReader("raw queued bytes"), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "queued.jpg"}, conversionPlan())
	if err != nil {
		t.Fatalf("failed to queue file: %v", err)
	}

	if _, err := proc.Storage.Stat(context.Background(), db.ID.String(), queuedEntry.ID); err != nil {
		t.Fatalf("expected queued file in storage before worker failure, got err: %v", err)
	}

	proc.runClaimedEntry(context.Background(), db.ID, queuedEntry)

	// The entry must be marked as erroneous, but the original file in storage must be preserved.
	updated, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("failed to fetch entry: %v", err)
	}
	if updated.Status != repo.EntryStatusError {
		t.Fatalf("expected EntryStatusError, got %v", updated.Status)
	}
	if _, err := proc.Storage.Stat(context.Background(), db.ID.String(), queuedEntry.ID); err != nil {
		t.Fatalf("expected raw file in storage to be preserved after worker conversion failure, got err: %v", err)
	}
	if updated.Size == 0 {
		t.Fatalf("expected non-zero size for preserved raw file, got %d", updated.Size)
	}
}

func TestFinalizeEntryFile_FinalUpdateFailurePreservesStorage(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	db.NMaxQueued = 10

	// Queue the entry so the raw original file is written to storage.
	queuedEntry, err := proc.queueIncomingFile(context.Background(), strings.NewReader("raw queued bytes"), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "queued.jpg"}, conversionPlan())
	if err != nil {
		t.Fatalf("failed to queue file: %v", err)
	}

	claimed, err := r.ClaimQueuedEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil || !claimed {
		t.Fatalf("failed to claim entry: %v", err)
	}
	claimedEntry, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("failed to get claimed entry: %v", err)
	}

	// Let the final metadata update fail; the follow-up status update from
	// failProcessedEntry must succeed.
	failingRepo := &failingUpdateRepo{Repository: r, failNext: 1}
	proc.Repo = failingRepo

	proc.runClaimedEntry(context.Background(), db.ID, claimedEntry)

	if failingRepo.failNext != 0 {
		t.Fatal("expected the final UpdateEntry call to fail exactly once")
	}

	// The entry must be marked as erroneous, but the stored file must be preserved:
	// a failing final database update must never delete user data.
	updated, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("failed to fetch entry: %v", err)
	}
	if updated.Status != repo.EntryStatusError {
		t.Fatalf("expected EntryStatusError, got %v", updated.Status)
	}
	if _, err := proc.Storage.Stat(context.Background(), db.ID.String(), queuedEntry.ID); err != nil {
		t.Fatalf("expected stored file to be preserved after final update failure, got err: %v", err)
	}
	if updated.Size == 0 {
		t.Fatalf("expected non-zero size for preserved file, got %d", updated.Size)
	}
}

func TestRunClaimedEntry_TransientReadErrorRequeues(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	store := &failingReadStorage{LocalStorage: &localstorage.LocalStorage{RootPath: t.TempDir()}, failRead: true}

	proc, r, db := setupTestProcessor(t, conv, store)
	defer r.Close()

	// Disable async capacity so queueing does not immediately auto-dispatch.
	proc.NFfmpegAsync = 0
	db.NMaxQueued = 10

	queuedEntry, err := proc.queueIncomingFile(context.Background(), strings.NewReader("queued payload"), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "photo.jpg"}, passthroughPlan())
	if err != nil {
		t.Fatalf("failed to queue file: %v", err)
	}

	// Simulate claiming the entry.
	claimed, err := r.ClaimQueuedEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil || !claimed {
		t.Fatalf("failed to claim entry: %v", err)
	}

	claimedEntry, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("failed to get claimed entry: %v", err)
	}

	// The worker encounters a transient read error and must requeue the entry.
	proc.runClaimedEntry(context.Background(), db.ID, claimedEntry)

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

	stats, err := r.GetDatabaseStats(context.Background(), db.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.QueuedCount != 1 {
		t.Errorf("expected QueuedCount == 1 after requeue, got %d", stats.QueuedCount)
	}
}

// ---------------------------------------------------------------------------
// Queue dispatching
// ---------------------------------------------------------------------------

func TestDispatchQueuedWorkers_ProcessesQueuedEntries(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	ctx := context.Background()
	db.NMaxQueued = 10

	// Disable async capacity so queueing does not immediately auto-dispatch.
	proc.NFfmpegAsync = 0

	e1, err := proc.queueIncomingFile(ctx, strings.NewReader("fake-image-1"), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e1.jpg"}, passthroughPlan())
	if err != nil {
		t.Fatalf("failed to queue e1: %v", err)
	}
	e2, err := proc.queueIncomingFile(ctx, strings.NewReader("fake-image-2"), db,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e2.jpg"}, passthroughPlan())
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

	// Restore capacity and dispatch.
	proc.NFfmpegAsync = 2
	proc.dispatchQueuedWorkers(ctx)

	waitForStatus(t, r, db.ID, e1.ID, repo.EntryStatusReady)
	waitForStatus(t, r, db.ID, e2.ID, repo.EntryStatusReady)

	stats, _ = r.GetDatabaseStats(ctx, db.ID)
	if stats.QueuedCount != 0 {
		t.Errorf("expected QueuedCount == 0, got %d", stats.QueuedCount)
	}
}

func TestDispatchQueuedWorkers_ParallelAcrossDatabases(t *testing.T) {
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
	db1.NMaxQueued = 10
	db2.NMaxQueued = 10

	e1, err := proc.queueIncomingFile(ctx, strings.NewReader("fake-image-1"), db1,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e1.jpg"}, passthroughPlan())
	if err != nil {
		t.Fatalf("failed to queue e1: %v", err)
	}
	e2, err := proc.queueIncomingFile(ctx, strings.NewReader("fake-image-2"), db2,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e2.jpg"}, passthroughPlan())
	if err != nil {
		t.Fatalf("failed to queue e2: %v", err)
	}

	proc.dispatchQueuedWorkers(ctx)

	waitForStatus(t, r, db1.ID, e1.ID, repo.EntryStatusReady)
	waitForStatus(t, r, db2.ID, e2.ID, repo.EntryStatusReady)

	stats1, _ := r.GetDatabaseStats(ctx, db1.ID)
	stats2, _ := r.GetDatabaseStats(ctx, db2.ID)
	if stats1.QueuedCount != 0 || stats2.QueuedCount != 0 {
		t.Errorf("expected QueuedCount == 0, got db1=%d db2=%d", stats1.QueuedCount, stats2.QueuedCount)
	}
}

func TestDispatchQueuedWorkers_RespectsAsyncLimit(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	ctx := context.Background()
	db.NMaxQueued = 10

	// Queue 3 entries without auto-dispatch.
	proc.NFfmpegAsync = 0
	var ids []int64
	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		e, err := proc.queueIncomingFile(ctx, strings.NewReader("img-"+name), db,
			EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: name}, passthroughPlan())
		if err != nil {
			t.Fatalf("failed to queue %s: %v", name, err)
		}
		ids = append(ids, e.ID)
	}

	// Allow only a single async worker at a time and occupy that slot:
	// the dispatch pass must not spawn anything while the slot is held.
	proc.NFfmpegAsync = 1
	if !proc.tryReserveAsyncSlot() {
		t.Fatal("failed to reserve slot")
	}
	proc.dispatchQueuedWorkers(ctx)

	for _, id := range ids {
		entry, err := r.GetEntry(ctx, db.ID, id)
		if err != nil {
			t.Fatalf("failed to fetch entry: %v", err)
		}
		if entry.Status != repo.EntryStatusQueued {
			t.Errorf("expected entry %d to remain queued while slot is held, got %v", id, entry.Status)
		}
	}

	// Freeing the slot triggers a dispatch pass which drains the queue.
	proc.releaseAsyncSlot()
	for _, id := range ids {
		waitForStatus(t, r, db.ID, id, repo.EntryStatusReady)
	}
}

func TestDispatchQueuedWorkers_ConcurrentTriggersDrainQueue(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	ctx := context.Background()
	db.NMaxQueued = 20

	// Queue 12 entries without auto-dispatch.
	proc.NFfmpegAsync = 0
	var ids []int64
	for i := 0; i < 12; i++ {
		e, err := proc.queueIncomingFile(ctx, strings.NewReader("queued image"), db,
			EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "e.jpg"}, passthroughPlan())
		if err != nil {
			t.Fatalf("failed to queue entry %d: %v", i, err)
		}
		ids = append(ids, e.ID)
	}

	// Restore capacity and hammer the dispatcher from many goroutines at once,
	// like concurrent slot releases and queue inserts would. No trigger may be
	// lost, regardless of how the passes interleave.
	proc.NFfmpegAsync = 4
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			proc.dispatchQueuedWorkers(ctx)
		}()
	}
	wg.Wait()

	for _, id := range ids {
		waitForStatus(t, r, db.ID, id, repo.EntryStatusReady)
	}

	stats, err := r.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.QueuedCount != 0 {
		t.Errorf("expected QueuedCount == 0, got %d", stats.QueuedCount)
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

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// StartQueueMonitor exited cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("StartQueueMonitor did not terminate after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// Issue 1.1: RetryRegistry composite key (dbID, entryID)
// ---------------------------------------------------------------------------

func TestRetryRegistry_ScopedByDatabaseAndEntryID(t *testing.T) {
	rr := newRetryRegistry()
	db1 := repo.ULID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	db2 := repo.ULID("01ARZ3NDEKTSV4RRFFQ69G5FAW")

	rr.noteClaimFailure(db1, 1)

	if !rr.claimDeferred(db1, 1) {
		t.Fatal("expected (db1, 1) to be deferred after noteClaimFailure")
	}
	if rr.claimDeferred(db2, 1) {
		t.Fatal("expected (db2, 1) NOT to be deferred when only (db1, 1) failed")
	}

	// Clearing (db2, 1) must not clear (db1, 1).
	rr.clearClaimRetry(db2, 1)
	if !rr.claimDeferred(db1, 1) {
		t.Fatal("expected (db1, 1) to remain deferred after clearClaimRetry(db2, 1)")
	}

	rr.clearClaimRetry(db1, 1)
	if rr.claimDeferred(db1, 1) {
		t.Fatal("expected (db1, 1) to be cleared after clearClaimRetry(db1, 1)")
	}
}

// ---------------------------------------------------------------------------
// Issue 1.4: Concurrent queueIncomingFile respects NMaxQueued without TOCTOU
// ---------------------------------------------------------------------------

func TestQueueIncomingFile_ConcurrentRespectsMaxQueued(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
	}
	proc, r, db := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// Disable background workers so queued entries stay queued during the test.
	proc.NFfmpegAsync = 0
	proc.NFfmpegTotal = 0

	const maxQueued = 5
	const totalCallers = 20
	db.NMaxQueued = maxQueued

	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	var succeeded []repo.Entry
	var unavailableCount int
	var unexpectedErrs []error

	for i := 0; i < totalCallers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			entry, err := proc.queueIncomingFile(
				context.Background(),
				strings.NewReader("concurrent payload"),
				db,
				EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "concurrent.jpg"},
				conversionPlan(),
			)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				succeeded = append(succeeded, entry)
			} else if errors.Is(err, customerrors.ErrUnavailable) {
				unavailableCount++
			} else {
				unexpectedErrs = append(unexpectedErrs, err)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if len(unexpectedErrs) > 0 {
		t.Fatalf("unexpected errors during concurrent queueing: %v", unexpectedErrs)
	}
	if len(succeeded) != maxQueued {
		t.Errorf("expected exactly %d queued entries, got %d", maxQueued, len(succeeded))
	}
	if unavailableCount != totalCallers-maxQueued {
		t.Errorf("expected %d ErrUnavailable responses, got %d", totalCallers-maxQueued, unavailableCount)
	}

	stats, err := r.GetDatabaseStats(context.Background(), db.ID)
	if err != nil {
		t.Fatalf("failed to get database stats: %v", err)
	}
	if stats.QueuedCount != maxQueued {
		t.Errorf("expected stats.QueuedCount == %d, got %d", maxQueued, stats.QueuedCount)
	}
	if stats.EntryCount != maxQueued {
		t.Errorf("expected stats.EntryCount == %d (no orphaned entries), got %d", maxQueued, stats.EntryCount)
	}
}

// ---------------------------------------------------------------------------
// Issue 1.5: Preview generation slot reservation & streaming from storage
// ---------------------------------------------------------------------------

type blockingPreviewConverter struct {
	*testMockConverter
	previewStarted chan struct{}
	unblockPreview chan struct{}
}

func (b *blockingPreviewConverter) CreatePreviewFromStream(ctx context.Context, inputData io.ReadSeeker, outputWriter io.Writer, inputMimeType string) error {
	select {
	case b.previewStarted <- struct{}{}:
	default:
	}
	<-b.unblockPreview
	data, err := io.ReadAll(inputData)
	if err != nil {
		return err
	}
	_, err = outputWriter.Write([]byte("preview:" + string(data)))
	return err
}

func TestProcessIncomingEntry_SyncPassthroughWithPreviewHoldsSlotAndStreamsFromStorage(t *testing.T) {
	baseConv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	conv := &blockingPreviewConverter{
		testMockConverter: baseConv,
		previewStarted:    make(chan struct{}, 1),
		unblockPreview:    make(chan struct{}),
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// Allow only 1 total slot and 0 async slots so a second upload while the
	// preview is running must fall back to queueing.
	proc.NFfmpegTotal = 1
	proc.NFfmpegAsync = 0

	previewDB, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "sync_preview_passthrough",
		ContentType: "image",
		NMaxQueued:  5,
		Config: repo.DatabaseConfig{
			AutoConversion: "",
			CreatePreview:  true,
		},
	})
	if err != nil {
		t.Fatalf("failed to create preview database: %v", err)
	}

	// First upload: passthrough with preview. It reserves the single sync slot and
	// hands it off to generatePreviewInBackground.
	entry1, wasSync1, err := proc.ProcessIncomingEntry(context.Background(), previewDB,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "first.jpg"},
		strings.NewReader("first-image-bytes"), "image/jpeg", "first.jpg")
	if err != nil {
		t.Fatalf("unexpected error on first upload: %v", err)
	}
	if !wasSync1 {
		t.Error("expected wasSync=true for first upload")
	}

	// Wait until generatePreviewInBackground is actively running.
	select {
	case <-conv.previewStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for background preview to start")
	}

	// While the background preview holds the sync slot, a second upload with
	// preview must see the slot occupied and fall back to queueing.
	entry2, wasSync2, err := proc.ProcessIncomingEntry(context.Background(), previewDB,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "second.jpg"},
		strings.NewReader("second-image-bytes"), "image/jpeg", "second.jpg")
	if err != nil {
		t.Fatalf("expected second upload to be queued while preview holds slot, got err: %v", err)
	}
	if wasSync2 || entry2.Status != repo.EntryStatusQueued {
		t.Errorf("expected second upload to be queued (wasSync=false, status=queued), got wasSync=%v status=%v", wasSync2, entry2.Status)
	}

	// Unblock preview generation for the first upload and wait for it to become ready.
	close(conv.unblockPreview)
	ready1 := waitForStatus(t, r, previewDB.ID, entry1.ID, repo.EntryStatusReady)
	if ready1.PreviewSize == 0 {
		t.Errorf("expected non-zero PreviewSize after background preview streamed from storage")
	}

	// Verify the preview content was streamed from the stored file.
	prevStream, err := proc.Storage.ReadPreview(context.Background(), previewDB.ID.String(), entry1.ID)
	if err != nil {
		t.Fatalf("failed to read preview from storage: %v", err)
	}
	defer prevStream.Close()
	prevBytes, _ := io.ReadAll(prevStream)
	if string(prevBytes) != "preview:first-image-bytes" {
		t.Errorf("expected preview %q, got %q", "preview:first-image-bytes", string(prevBytes))
	}
}

func TestProcessIncomingEntry_AsyncPassthroughWithPreviewReservesSlot(t *testing.T) {
	conv := &testMockConverter{
		canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// Exhaust async slots so an async upload requiring a preview must fall back to queueing.
	proc.NFfmpegAsync = 0
	proc.NFfmpegTotal = 2

	previewDB, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "async_preview_passthrough",
		ContentType: "image",
		NMaxQueued:  5,
		Config: repo.DatabaseConfig{
			AutoConversion: "",
			CreatePreview:  true,
		},
	})
	if err != nil {
		t.Fatalf("failed to create preview database: %v", err)
	}

	tempPath := writeTempFile(t, "mh-upload-preview-*.jpg", []byte("large image with preview"))
	file, err := os.Open(tempPath)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}
	defer func() {
		file.Close()
		os.Remove(tempPath)
	}()

	entry, synchronous, err := proc.ProcessIncomingEntry(context.Background(), previewDB,
		EntryRequest{Timestamp: time.Now().UnixMilli(), FileName: "large.jpg"},
		file, "image/jpeg", "large.jpg")
	if err != nil {
		t.Fatalf("expected large passthrough with preview to fall back to queue when async slots exhausted, got err: %v", err)
	}
	if synchronous {
		t.Error("expected synchronous=false")
	}
	if entry.Status != repo.EntryStatusQueued {
		t.Errorf("expected status queued when async slots are exhausted for preview-enabled passthrough, got %v", entry.Status)
	}
}

