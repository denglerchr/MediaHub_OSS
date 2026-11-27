package processing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/storage"
)

type previewSpyConverter struct {
	*testMockConverter
	streamPreviewCalls atomic.Int32
	filePreviewCalls   atomic.Int32
}

func (c *previewSpyConverter) CreatePreviewFromStream(ctx context.Context, inputData io.ReadSeeker, outputWriter io.Writer, inputMimeType string) error {
	c.streamPreviewCalls.Add(1)
	return c.testMockConverter.CreatePreviewFromStream(ctx, inputData, outputWriter, inputMimeType)
}

func (c *previewSpyConverter) CreatePreviewFromFile(ctx context.Context, filepath string, outputWriter io.Writer, inputMimeType string) error {
	c.filePreviewCalls.Add(1)
	return c.testMockConverter.CreatePreviewFromFile(ctx, filepath, outputWriter, inputMimeType)
}

func assertStoredPreviewBytes(t *testing.T, proc *Processor, dbID repo.ULID, entryID int64, want []byte) {
	t.Helper()
	rc, err := proc.Storage.ReadPreview(context.Background(), dbID, entryID)
	if err != nil {
		t.Fatalf("failed to read stored preview: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read preview stream: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("stored preview mismatch: got %q, want %q", got, want)
	}
}

func TestCustomPreview_SyncWithCreatePreviewTrue(t *testing.T) {
	conv := &previewSpyConverter{
		testMockConverter: &testMockConverter{
			canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
		},
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	// Even with NFfmpegTotal=0, sync passthrough with custom preview needs no slot.
	proc.NFfmpegTotal = 0
	proc.NFfmpegAsync = 0

	db, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "sync_custom_preview",
		ContentType: "image",
		Config: repo.DatabaseConfig{
			AutoConversion: "",
			CreatePreview:  true,
		},
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	customPreview := []byte("RIFF\x16\x00\x00\x00WEBPVP8 \x0a\x00\x00\x00\x00\x00\x00\x9d\x01\x2a\x10\x00\x10\x00")
	payload := []byte("original-jpeg-payload")

	entry, synchronous, err := proc.ProcessIncomingEntry(
		context.Background(),
		db,
		EntryRequest{
			Timestamp:     time.Now().UnixMilli(),
			FileName:      "photo.jpg",
			CustomPreview: customPreview,
		},
		strings.NewReader(string(payload)),
		"image/jpeg",
		"photo.jpg",
	)
	if err != nil {
		t.Fatalf("ProcessIncomingEntry failed: %v", err)
	}
	if !synchronous {
		t.Fatal("expected synchronous=true")
	}
	if entry.Status != repo.EntryStatusReady {
		t.Fatalf("expected status ready, got %v", entry.Status)
	}
	if entry.PreviewSize != uint64(len(customPreview)) {
		t.Fatalf("expected PreviewSize=%d, got %d", len(customPreview), entry.PreviewSize)
	}
	if conv.streamPreviewCalls.Load() != 0 || conv.filePreviewCalls.Load() != 0 {
		t.Fatalf("expected 0 preview generator calls, got stream=%d file=%d",
			conv.streamPreviewCalls.Load(), conv.filePreviewCalls.Load())
	}

	assertStoredPreviewBytes(t, proc, db.ID, entry.ID, customPreview)

	stats, err := r.GetDatabaseStats(context.Background(), db.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	wantDiskSpace := uint64(len(payload) + len(customPreview))
	if stats.TotalDiskSpaceBytes != wantDiskSpace {
		t.Fatalf("expected TotalDiskSpaceBytes=%d, got %d", wantDiskSpace, stats.TotalDiskSpaceBytes)
	}
}

func TestCustomPreview_SyncOnFileDatabase(t *testing.T) {
	conv := &previewSpyConverter{
		testMockConverter: &testMockConverter{
			canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
		},
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	fileDB, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "files_with_custom_preview",
		ContentType: "file",
		Config: repo.DatabaseConfig{
			CreatePreview: false,
		},
	})
	if err != nil {
		t.Fatalf("failed to create file database: %v", err)
	}

	customPreview := []byte("custom-webp-preview-for-file")
	payload := []byte("plain document content")

	entry, synchronous, err := proc.ProcessIncomingEntry(
		context.Background(),
		fileDB,
		EntryRequest{
			Timestamp:     time.Now().UnixMilli(),
			FileName:      "notes.txt",
			CustomPreview: customPreview,
		},
		strings.NewReader(string(payload)),
		"text/plain",
		"notes.txt",
	)
	if err != nil {
		t.Fatalf("ProcessIncomingEntry failed: %v", err)
	}
	if !synchronous {
		t.Fatal("expected synchronous=true")
	}
	if entry.Status != repo.EntryStatusReady {
		t.Fatalf("expected status ready, got %v", entry.Status)
	}
	if entry.PreviewSize != uint64(len(customPreview)) {
		t.Fatalf("expected PreviewSize=%d, got %d", len(customPreview), entry.PreviewSize)
	}
	if conv.streamPreviewCalls.Load() != 0 || conv.filePreviewCalls.Load() != 0 {
		t.Fatalf("expected 0 preview generator calls, got stream=%d file=%d",
			conv.streamPreviewCalls.Load(), conv.filePreviewCalls.Load())
	}

	assertStoredPreviewBytes(t, proc, fileDB.ID, entry.ID, customPreview)

	stats, err := r.GetDatabaseStats(context.Background(), fileDB.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	wantDiskSpace := uint64(len(payload) + len(customPreview))
	if stats.TotalDiskSpaceBytes != wantDiskSpace {
		t.Fatalf("expected TotalDiskSpaceBytes=%d, got %d", wantDiskSpace, stats.TotalDiskSpaceBytes)
	}
}

func TestCustomPreview_AsyncDiskSpooledWithAutoConversionAndCreatePreview(t *testing.T) {
	conv := &previewSpyConverter{
		testMockConverter: &testMockConverter{
			canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
		},
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	db, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "async_custom_preview",
		ContentType: "image",
		Config: repo.DatabaseConfig{
			AutoConversion: "image/png",
			CreatePreview:  true,
		},
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	customPreview := []byte("custom-async-preview-bytes")
	payload := []byte("disk-spooled-jpeg-bytes")
	tempPath := writeTempFile(t, "mh-custom-preview-async-*.jpg", payload)
	file, err := os.Open(tempPath)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}

	entry, synchronous, err := proc.ProcessIncomingEntry(
		context.Background(),
		db,
		EntryRequest{
			Timestamp:     time.Now().UnixMilli(),
			FileName:      "photo.jpg",
			CustomPreview: customPreview,
		},
		file,
		"image/jpeg",
		"photo.jpg",
	)
	if err != nil {
		t.Fatalf("ProcessIncomingEntry failed: %v", err)
	}
	if synchronous {
		t.Fatal("expected synchronous=false for disk-spooled upload")
	}

	finalEntry := waitForStatus(t, r, db.ID, entry.ID, repo.EntryStatusReady)
	if finalEntry.PreviewSize != uint64(len(customPreview)) {
		t.Fatalf("expected final PreviewSize=%d, got %d", len(customPreview), finalEntry.PreviewSize)
	}
	if conv.streamPreviewCalls.Load() != 0 || conv.filePreviewCalls.Load() != 0 {
		t.Fatalf("expected 0 preview generator calls, got stream=%d file=%d",
			conv.streamPreviewCalls.Load(), conv.filePreviewCalls.Load())
	}

	assertStoredPreviewBytes(t, proc, db.ID, entry.ID, customPreview)
}

func TestCustomPreview_QueuedUploadAndWorkerFinalizationPreservesPreview(t *testing.T) {
	conv := &previewSpyConverter{
		testMockConverter: &testMockConverter{
			canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: true},
		},
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	db, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "queued_custom_preview",
		ContentType: "image",
		NMaxQueued:  5,
		Config: repo.DatabaseConfig{
			AutoConversion: "image/png",
			CreatePreview:  true,
		},
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Exhaust slots so the upload falls back to the queue.
	proc.NFfmpegTotal = 0
	proc.NFfmpegAsync = 0

	customPreview := []byte("custom-queued-preview-bytes")
	payload := []byte("queued-jpeg-bytes")

	queuedEntry, synchronous, err := proc.ProcessIncomingEntry(
		context.Background(),
		db,
		EntryRequest{
			Timestamp:     time.Now().UnixMilli(),
			FileName:      "queued.jpg",
			CustomPreview: customPreview,
		},
		strings.NewReader(string(payload)),
		"image/jpeg",
		"queued.jpg",
	)
	if err != nil {
		t.Fatalf("ProcessIncomingEntry failed: %v", err)
	}
	if synchronous {
		t.Fatal("expected synchronous=false for queued upload")
	}
	if queuedEntry.Status != repo.EntryStatusQueued {
		t.Fatalf("expected status queued, got %v", queuedEntry.Status)
	}
	if queuedEntry.PreviewSize != uint64(len(customPreview)) {
		t.Fatalf("expected queued PreviewSize=%d, got %d", len(customPreview), queuedEntry.PreviewSize)
	}
	assertStoredPreviewBytes(t, proc, db.ID, queuedEntry.ID, customPreview)

	// Re-verify persisted DB entry before worker runs.
	persisted, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("GetEntry failed: %v", err)
	}
	if persisted.PreviewSize != uint64(len(customPreview)) {
		t.Fatalf("expected persisted PreviewSize=%d, got %d", len(customPreview), persisted.PreviewSize)
	}

	// Simulate worker claiming and finalizing the queued entry.
	claimed, err := r.ClaimQueuedEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil || !claimed {
		t.Fatalf("ClaimQueuedEntry failed: claimed=%v err=%v", claimed, err)
	}

	plan := DeterminePlanForEntry(proc.MediaConverter, db, persisted)
	if plan.WantsPreview {
		t.Fatal("expected DeterminePlanForEntry to disable WantsPreview when entry.PreviewSize > 0")
	}

	tempPath := writeTempFile(t, "mh-queued-worker-*.jpg", payload)
	if err := proc.finalizeEntryFile(context.Background(), db, persisted, tempPath, plan); err != nil {
		t.Fatalf("finalizeEntryFile failed: %v", err)
	}

	finalEntry, err := r.GetEntry(context.Background(), db.ID, queuedEntry.ID)
	if err != nil {
		t.Fatalf("GetEntry after finalize failed: %v", err)
	}
	if finalEntry.Status != repo.EntryStatusReady {
		t.Fatalf("expected ready status, got %v", finalEntry.Status)
	}
	if finalEntry.PreviewSize != uint64(len(customPreview)) {
		t.Fatalf("expected final PreviewSize=%d, got %d", len(customPreview), finalEntry.PreviewSize)
	}
	if conv.streamPreviewCalls.Load() != 0 || conv.filePreviewCalls.Load() != 0 {
		t.Fatalf("expected 0 preview generator calls, got stream=%d file=%d",
			conv.streamPreviewCalls.Load(), conv.filePreviewCalls.Load())
	}

	assertStoredPreviewBytes(t, proc, db.ID, queuedEntry.ID, customPreview)
}

type shutdownDuringCreateRepo struct {
	repo.Repository
	proc *Processor
}

func (r *shutdownDuringCreateRepo) CreateEntry(ctx context.Context, db repo.Database, entry repo.Entry) (repo.Entry, error) {
	created, err := r.Repository.CreateEntry(ctx, db, entry)
	if err == nil && r.proc != nil {
		// Flip shuttingDown right after the preliminary entry (and custom preview) are created
		// so tryTrackWorker() fails in processAsynchronously.
		r.proc.shuttingDown.Store(true)
	}
	return created, err
}

func TestProcessIncomingEntry_AsyncWorkerTrackFailureCleansCustomPreview(t *testing.T) {
	conv := &previewSpyConverter{
		testMockConverter: &testMockConverter{
			canConvertCheck: media.ConversionCheck{CanConvert: true, NeedsConversion: false},
		},
	}
	proc, r, _ := setupTestProcessor(t, conv, nil)
	defer r.Close()

	db, err := r.CreateDatabase(context.Background(), repo.Database{
		Name:        "async_cleanup_db",
		ContentType: "image",
		Config: repo.DatabaseConfig{
			CreatePreview: true,
		},
	})
	if err != nil {
		t.Fatalf("CreateDatabase failed: %v", err)
	}

	proc.Repo = &shutdownDuringCreateRepo{Repository: r, proc: proc}

	tempPath := writeTempFile(t, "mh-async-cleanup-*.jpg", []byte("fake-jpeg-bytes"))
	f, err := os.Open(tempPath)
	if err != nil {
		t.Fatalf("Open temp file failed: %v", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tempPath)
	}()

	customPreview := []byte("RIFF\x1a\x00\x00\x00WEBPVP8L\x0e\x00\x00\x00\x2f\x00\x00\x00\x00")
	_, _, err = proc.ProcessIncomingEntry(context.Background(), db, EntryRequest{
		FileName:      "async.jpg",
		CustomPreview: customPreview,
	}, f, "image/jpeg", "async.jpg")
	if !errors.Is(err, customerrors.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable when worker tracking fails during shutdown, got %v", err)
	}

	var leaked []int64
	if err := proc.Storage.WalkPreview(context.Background(), db.ID, func(id int64, _ storage.FileInfo) error {
		leaked = append(leaked, id)
		return nil
	}); err != nil {
		t.Fatalf("WalkPreview failed: %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("expected 0 orphaned previews after async worker-track failure cleanup, found ids %v", leaked)
	}
}
