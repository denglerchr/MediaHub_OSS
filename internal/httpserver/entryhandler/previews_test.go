package entryhandler_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
	"mediahub_oss/internal/storage/localstorage"
)

type recordedAuditEvent struct {
	action   string
	actor    string
	resource string
	details  map[string]any
}

type recordingAuditor struct {
	events []recordedAuditEvent
}

func (a *recordingAuditor) Log(_ context.Context, action, actor, resource string, details map[string]any) {
	a.events = append(a.events, recordedAuditEvent{
		action:   action,
		actor:    actor,
		resource: resource,
		details:  details,
	})
}

func (a *recordingAuditor) last() (recordedAuditEvent, bool) {
	if len(a.events) == 0 {
		return recordedAuditEvent{}, false
	}
	return a.events[len(a.events)-1], true
}

type previewTestConverter struct {
	mockMediaConverter
	canCreatePreview  bool
	previewGenCalls   int
}

func (c *previewTestConverter) CanCreatePreview(_ string) bool {
	return c.canCreatePreview
}

func (c *previewTestConverter) CreatePreviewFromStream(_ context.Context, _ io.ReadSeeker, _ io.Writer, _ string) error {
	c.previewGenCalls++
	return nil
}

func (c *previewTestConverter) CreatePreviewFromFile(_ context.Context, _ string, _ io.Writer, _ string) error {
	c.previewGenCalls++
	return nil
}

type webpChunk struct {
	fourCC string
	data   []byte
}

func buildTestWebP(chunks ...webpChunk) []byte {
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, c := range chunks {
		body.WriteString(c.fourCC)
		var sz [4]byte
		binary.LittleEndian.PutUint32(sz[:], uint32(len(c.data)))
		body.Write(sz[:])
		body.Write(c.data)
		if len(c.data)%2 == 1 {
			body.WriteByte(0)
		}
	}

	var out bytes.Buffer
	out.WriteString("RIFF")
	var riffSize [4]byte
	binary.LittleEndian.PutUint32(riffSize[:], uint32(body.Len()))
	out.Write(riffSize[:])
	out.Write(body.Bytes())
	return out.Bytes()
}

func makeStaticVP8WebP(width, height, extraBytes int) []byte {
	payload := make([]byte, 10+extraBytes)
	payload[3], payload[4], payload[5] = 0x9D, 0x01, 0x2A
	binary.LittleEndian.PutUint16(payload[6:8], uint16(width&0x3FFF))
	binary.LittleEndian.PutUint16(payload[8:10], uint16(height&0x3FFF))
	return buildTestWebP(webpChunk{fourCC: "VP8 ", data: payload})
}

func makeStaticVP8LWebP(width, height, extraBytes int) []byte {
	payload := make([]byte, 5+extraBytes)
	payload[0] = 0x2F
	bits := uint32(width-1)&0x3FFF | (uint32(height-1)&0x3FFF)<<14
	binary.LittleEndian.PutUint32(payload[1:5], bits)
	return buildTestWebP(webpChunk{fourCC: "VP8L", data: payload})
}

func makeAnimatedVP8XWebP(width, height int) []byte {
	vp8x := make([]byte, 10)
	vp8x[0] = 0x02 // Animation flag
	w := uint32(width - 1)
	h := uint32(height - 1)
	vp8x[4] = byte(w)
	vp8x[5] = byte(w >> 8)
	vp8x[6] = byte(w >> 16)
	vp8x[7] = byte(h)
	vp8x[8] = byte(h >> 8)
	vp8x[9] = byte(h >> 16)

	return buildTestWebP(
		webpChunk{fourCC: "VP8X", data: vp8x},
		webpChunk{fourCC: "ANIM", data: make([]byte, 6)},
		webpChunk{fourCC: "ANMF", data: make([]byte, 24)},
	)
}

func setupPreviewTestEnv(t *testing.T, contentType string, createPreview bool, conv media.MediaConverter) (*sqlite.SQLiteRepository, *localstorage.LocalStorage, *entryhandler.EntryHandler, *recordingAuditor, repo.Database) {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	storageRoot := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		t.Fatalf("failed to create storage root: %v", err)
	}
	store := &localstorage.LocalStorage{RootPath: storageRoot}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "preview_db_" + contentType,
		ContentType: contentType,
		Config: repo.DatabaseConfig{
			CreatePreview: createPreview,
		},
	})
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	if conv == nil {
		conv = &mockMediaConverter{}
	}
	proc := processing.NewProcessor(r, store, conv, 2, 4, logger)
	t.Cleanup(func() { _ = proc.Shutdown(context.Background()) })

	auditor := &recordingAuditor{}
	handler := &entryhandler.EntryHandler{
		Repo:                   r,
		Storage:                store,
		MediaConverter:         conv,
		Processor:              proc,
		Auditor:                auditor,
		Logger:                 logger,
		MaxSyncUploadSizeBytes: 1024 * 1024,
	}

	return r, store, handler, auditor, db
}

func createTestEntry(t *testing.T, r *sqlite.SQLiteRepository, store *localstorage.LocalStorage, db repo.Database, status repo.EntryStatus, previewBytes []byte) repo.Entry {
	t.Helper()
	ctx := context.Background()

	fileBytes := []byte("entry-file-bytes")
	mimeType := "application/octet-stream"
	fileName := "test.bin"
	var mediaFields map[string]any
	if db.ContentType == "image" {
		mimeType = "image/jpeg"
		fileName = "test.jpg"
		mediaFields = map[string]any{"width": 100, "height": 100}
	}

	entry, err := r.CreateEntry(ctx, db, repo.Entry{
		Timestamp:   time.Now(),
		MimeType:    mimeType,
		FileName:    fileName,
		Size:        uint64(len(fileBytes)),
		PreviewSize: uint64(len(previewBytes)),
		Status:      status,
		MediaFields: mediaFields,
	})
	if err != nil {
		t.Fatalf("CreateEntry failed: %v", err)
	}
	if _, err := store.Write(ctx, db.ID, entry.ID, bytes.NewReader(fileBytes)); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	if len(previewBytes) > 0 {
		if _, err := store.WritePreview(ctx, db.ID, entry.ID, bytes.NewReader(previewBytes)); err != nil {
			t.Fatalf("store.WritePreview failed: %v", err)
		}
	}
	return entry
}

func newAuthedPreviewRequest(method, dbID, entryID string, contentType string, body []byte) *http.Request {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/database/"+dbID+"/entry/"+entryID+"/preview", bodyReader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.SetPathValue("database_id", dbID)
	req.SetPathValue("id", entryID)
	return req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "preview_user", IsAdmin: true}))
}

func TestGetEntryPreview_BinaryJSONAndNotFound(t *testing.T) {
	r, store, handler, _, db := setupPreviewTestEnv(t, "file", false, nil)
	ctx := context.Background()

	webpData := makeStaticVP8LWebP(64, 48, 0)
	entryWithPreview := createTestEntry(t, r, store, db, repo.EntryStatusReady, webpData)
	entryWithoutPreview := createTestEntry(t, r, store, db, repo.EntryStatusReady, nil)

	t.Run("binary GET returns image/webp and no-cache headers", func(t *testing.T) {
		req := newAuthedPreviewRequest(http.MethodGet, db.ID.String(), "1", "", nil)
		rec := httptest.NewRecorder()
		handler.GetEntryPreview(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
			t.Fatalf("expected Content-Type image/webp, got %q", ct)
		}
		if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
			t.Fatalf("expected X-Content-Type-Options nosniff, got %q", nosniff)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Fatalf("expected no-store Cache-Control, got %q", cc)
		}
		if !bytes.Equal(rec.Body.Bytes(), webpData) {
			t.Fatal("returned binary preview does not match stored WebP bytes")
		}
	})

	t.Run("Accept application/json returns Base64 FileJSONResponse", func(t *testing.T) {
		req := newAuthedPreviewRequest(http.MethodGet, db.ID.String(), "1", "", nil)
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		handler.GetEntryPreview(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ct)
		}

		var resp entryhandler.FileJSONResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode FileJSONResponse: %v", err)
		}
		if resp.Filename != "1_preview.webp" || resp.MimeType != "image/webp" {
			t.Fatalf("unexpected FileJSONResponse metadata: %+v", resp)
		}
		wantData := "data:image/webp;base64," + base64.StdEncoding.EncodeToString(webpData)
		if resp.Data != wantData {
			t.Fatalf("data URI mismatch")
		}
	})

	t.Run("missing preview returns 404", func(t *testing.T) {
		req := newAuthedPreviewRequest(http.MethodGet, db.ID.String(), "2", "", nil)
		rec := httptest.NewRecorder()
		handler.GetEntryPreview(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		_ = entryWithPreview
		_ = entryWithoutPreview
		_ = ctx
	})
}

func TestPostEntryPreview(t *testing.T) {
	t.Run("201 Created on file DB with static VP8L and animated VP8X WebP", func(t *testing.T) {
		r, store, handler, auditor, db := setupPreviewTestEnv(t, "file", false, nil)
		ctx := context.Background()

		e1 := createTestEntry(t, r, store, db, repo.EntryStatusReady, nil)
		e2 := createTestEntry(t, r, store, db, repo.EntryStatusReady, nil)

		cases := []struct {
			name    string
			entryID int64
			payload []byte
		}{
			{"static VP8L", e1.ID, makeStaticVP8LWebP(80, 60, 0)},
			{"animated VP8X", e2.ID, makeAnimatedVP8XWebP(120, 90)},
		}

		var expectedDisk uint64 = e1.Size + e2.Size
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				req := newAuthedPreviewRequest(http.MethodPost, db.ID.String(), formatInt64(tc.entryID), "image/webp", tc.payload)
				rec := httptest.NewRecorder()
				handler.PostEntryPreview(rec, req)

				if rec.Code != http.StatusCreated {
					t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
				}

				var resp entryhandler.PreviewUpdateResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if resp.DatabaseID != db.ID.String() || resp.EntryID != tc.entryID || resp.PreviewSize != uint64(len(tc.payload)) || resp.UpdatedAt == 0 {
					t.Fatalf("unexpected PreviewUpdateResponse: %+v", resp)
				}

				stored, err := store.ReadPreview(ctx, db.ID, tc.entryID)
				if err != nil {
					t.Fatalf("ReadPreview failed: %v", err)
				}
				gotBytes, _ := io.ReadAll(stored)
				_ = stored.Close()
				if !bytes.Equal(gotBytes, tc.payload) {
					t.Fatal("stored preview bytes mismatch")
				}

				expectedDisk += uint64(len(tc.payload))
				stats, err := r.GetDatabaseStats(ctx, db.ID)
				if err != nil {
					t.Fatalf("GetDatabaseStats failed: %v", err)
				}
				if stats.TotalDiskSpaceBytes != expectedDisk {
					t.Fatalf("total_disk_space_bytes = %d, want %d", stats.TotalDiskSpaceBytes, expectedDisk)
				}

				ev, ok := auditor.last()
				if !ok || ev.action != "entry.preview_create" {
					t.Fatalf("expected audit action entry.preview_create, got %+v", ev)
				}
				if ev.details["preview_filesize"] != uint64(len(tc.payload)) {
					t.Fatalf("unexpected audit details: %+v", ev.details)
				}
			})
		}
	})

	t.Run("409 Conflict when entry already has preview does not overwrite storage", func(t *testing.T) {
		r, store, handler, _, db := setupPreviewTestEnv(t, "image", false, nil)
		ctx := context.Background()

		existing := makeStaticVP8WebP(50, 50, 0)
		entry := createTestEntry(t, r, store, db, repo.EntryStatusReady, existing)
		replacement := makeStaticVP8LWebP(100, 100, 0)

		req := newAuthedPreviewRequest(http.MethodPost, db.ID.String(), formatInt64(entry.ID), "image/webp", replacement)
		rec := httptest.NewRecorder()
		handler.PostEntryPreview(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
		}

		stored, err := store.ReadPreview(ctx, db.ID, entry.ID)
		if err != nil {
			t.Fatalf("ReadPreview failed: %v", err)
		}
		gotBytes, _ := io.ReadAll(stored)
		_ = stored.Close()
		if !bytes.Equal(gotBytes, existing) {
			t.Fatal("existing preview was overwritten on 409 conflict")
		}
	})

	t.Run("409 Conflict on processing, queued, and deleting statuses", func(t *testing.T) {
		r, store, handler, _, db := setupPreviewTestEnv(t, "image", false, nil)
		valid := makeStaticVP8LWebP(64, 64, 0)

		for _, status := range []repo.EntryStatus{repo.EntryStatusProcessing, repo.EntryStatusQueued, repo.EntryStatusDeleting} {
			entry := createTestEntry(t, r, store, db, status, nil)
			req := newAuthedPreviewRequest(http.MethodPost, db.ID.String(), formatInt64(entry.ID), "image/webp", valid)
			rec := httptest.NewRecorder()
			handler.PostEntryPreview(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status=%v: expected 409, got %d: %s", status, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("400, 404, 413, 415 validation and lookup errors", func(t *testing.T) {
		r, store, handler, _, db := setupPreviewTestEnv(t, "image", false, nil)
		entry := createTestEntry(t, r, store, db, repo.EntryStatusReady, nil)
		idStr := formatInt64(entry.ID)
		valid := makeStaticVP8LWebP(64, 64, 0)
		oversized := make([]byte, media.MaxPreviewBytes+1)
		copy(oversized, valid)
		tooWide := makeStaticVP8WebP(201, 200, 0)
		pngBytes := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

		tests := []struct {
			name        string
			dbID        string
			entryID     string
			contentType string
			body        []byte
			wantCode    int
		}{
			{"invalid database_id", "not-a-ulid", idStr, "image/webp", valid, http.StatusBadRequest},
			{"invalid entry id", db.ID.String(), "abc", "image/webp", valid, http.StatusBadRequest},
			{"empty body", db.ID.String(), idStr, "image/webp", []byte{}, http.StatusBadRequest},
			{"malformed WebP", db.ID.String(), idStr, "image/webp", []byte("RIFF\x08\x00\x00\x00WEBPbad!"), http.StatusBadRequest},
			{"dimensions 201x200", db.ID.String(), idStr, "image/webp", tooWide, http.StatusBadRequest},
			{"oversized > 128 KiB", db.ID.String(), idStr, "image/webp", oversized, http.StatusRequestEntityTooLarge},
			{"Content-Type application/json", db.ID.String(), idStr, "application/json", valid, http.StatusUnsupportedMediaType},
			{"Content-Type multipart/form-data", db.ID.String(), idStr, "multipart/form-data; boundary=abc", valid, http.StatusUnsupportedMediaType},
			{"missing Content-Type", db.ID.String(), idStr, "", valid, http.StatusUnsupportedMediaType},
			{"PNG bytes with image/webp Content-Type", db.ID.String(), idStr, "image/webp", pngBytes, http.StatusUnsupportedMediaType},
			{"nonexistent database", ulid.New().String(), "1", "image/webp", valid, http.StatusNotFound},
			{"nonexistent entry", db.ID.String(), "99999", "image/webp", valid, http.StatusNotFound},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				req := newAuthedPreviewRequest(http.MethodPost, tc.dbID, tc.entryID, tc.contentType, tc.body)
				rec := httptest.NewRecorder()
				handler.PostEntryPreview(rec, req)

				if rec.Code != tc.wantCode {
					t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
				}
			})
		}
	})
}

func TestPutEntryPreview(t *testing.T) {
	t.Run("200 OK upsert creates from 0 and replaces existing updating disk stats", func(t *testing.T) {
		r, store, handler, auditor, db := setupPreviewTestEnv(t, "image", true, nil)
		ctx := context.Background()

		entry := createTestEntry(t, r, store, db, repo.EntryStatusReady, nil)
		idStr := formatInt64(entry.ID)

		firstPreview := makeStaticVP8WebP(100, 100, 40)
		req1 := newAuthedPreviewRequest(http.MethodPut, db.ID.String(), idStr, "image/webp; charset=binary", firstPreview)
		rec1 := httptest.NewRecorder()
		handler.PutEntryPreview(rec1, req1)

		if rec1.Code != http.StatusOK {
			t.Fatalf("expected 200 on initial PUT, got %d: %s", rec1.Code, rec1.Body.String())
		}
		var resp1 entryhandler.PreviewUpdateResponse
		if err := json.Unmarshal(rec1.Body.Bytes(), &resp1); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if resp1.PreviewSize != uint64(len(firstPreview)) {
			t.Fatalf("preview_filesize = %d, want %d", resp1.PreviewSize, len(firstPreview))
		}
		stats, _ := r.GetDatabaseStats(ctx, db.ID)
		if stats.TotalDiskSpaceBytes != entry.Size+uint64(len(firstPreview)) {
			t.Fatalf("disk space after 0->N = %d, want %d", stats.TotalDiskSpaceBytes, entry.Size+uint64(len(firstPreview)))
		}
		ev1, _ := auditor.last()
		if ev1.action != "entry.preview_update" || ev1.details["previous_preview_filesize"] != uint64(0) {
			t.Fatalf("unexpected audit event on initial PUT: %+v", ev1)
		}

		// Replace with a smaller preview
		secondPreview := makeStaticVP8LWebP(64, 64, 0)
		req2 := newAuthedPreviewRequest(http.MethodPut, db.ID.String(), idStr, "image/webp", secondPreview)
		rec2 := httptest.NewRecorder()
		handler.PutEntryPreview(rec2, req2)

		if rec2.Code != http.StatusOK {
			t.Fatalf("expected 200 on replace PUT, got %d: %s", rec2.Code, rec2.Body.String())
		}
		var resp2 entryhandler.PreviewUpdateResponse
		if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if resp2.PreviewSize != uint64(len(secondPreview)) {
			t.Fatalf("preview_filesize = %d, want %d", resp2.PreviewSize, len(secondPreview))
		}

		stats, _ = r.GetDatabaseStats(ctx, db.ID)
		if stats.TotalDiskSpaceBytes != entry.Size+uint64(len(secondPreview)) {
			t.Fatalf("disk space after N->M = %d, want %d", stats.TotalDiskSpaceBytes, entry.Size+uint64(len(secondPreview)))
		}

		stored, err := store.ReadPreview(ctx, db.ID, entry.ID)
		if err != nil {
			t.Fatalf("ReadPreview failed: %v", err)
		}
		gotBytes, _ := io.ReadAll(stored)
		_ = stored.Close()
		if !bytes.Equal(gotBytes, secondPreview) {
			t.Fatal("stored preview was not replaced by PUT")
		}

		ev2, _ := auditor.last()
		if ev2.action != "entry.preview_update" || ev2.details["previous_preview_filesize"] != uint64(len(firstPreview)) {
			t.Fatalf("unexpected audit event on replace PUT: %+v", ev2)
		}
	})

	t.Run("409 on blocked statuses and 400/404/413/415 validation errors", func(t *testing.T) {
		r, store, handler, _, db := setupPreviewTestEnv(t, "image", false, nil)
		valid := makeStaticVP8LWebP(64, 64, 0)

		for _, status := range []repo.EntryStatus{repo.EntryStatusProcessing, repo.EntryStatusQueued, repo.EntryStatusDeleting} {
			e := createTestEntry(t, r, store, db, status, nil)
			req := newAuthedPreviewRequest(http.MethodPut, db.ID.String(), formatInt64(e.ID), "image/webp", valid)
			rec := httptest.NewRecorder()
			handler.PutEntryPreview(rec, req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status=%v: expected 409, got %d", status, rec.Code)
			}
		}

		ready := createTestEntry(t, r, store, db, repo.EntryStatusReady, nil)
		idStr := formatInt64(ready.ID)
		oversized := make([]byte, media.MaxPreviewBytes+1)
		copy(oversized, valid)

		for _, tc := range []struct {
			dbID string
			id   string
			ct   string
			body []byte
			code int
		}{
			{db.ID.String(), "bad", "image/webp", valid, http.StatusBadRequest},
			{ulid.New().String(), "1", "image/webp", valid, http.StatusNotFound},
			{db.ID.String(), "999", "image/webp", valid, http.StatusNotFound},
			{db.ID.String(), idStr, "image/webp", oversized, http.StatusRequestEntityTooLarge},
			{db.ID.String(), idStr, "text/plain", valid, http.StatusUnsupportedMediaType},
		} {
			req := newAuthedPreviewRequest(http.MethodPut, tc.dbID, tc.id, tc.ct, tc.body)
			rec := httptest.NewRecorder()
			handler.PutEntryPreview(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("expected %d, got %d: %s", tc.code, rec.Code, rec.Body.String())
			}
		}
	})
}

func TestDeleteEntryPreview(t *testing.T) {
	t.Run("200 OK deletes storage, resets preview_filesize, adjusts disk stats, logs audit, subsequent GET 404", func(t *testing.T) {
		r, store, handler, auditor, db := setupPreviewTestEnv(t, "image", false, nil)
		ctx := context.Background()

		preview := makeStaticVP8LWebP(80, 80, 0)
		entry := createTestEntry(t, r, store, db, repo.EntryStatusReady, preview)
		idStr := formatInt64(entry.ID)

		req := newAuthedPreviewRequest(http.MethodDelete, db.ID.String(), idStr, "", nil)
		rec := httptest.NewRecorder()
		handler.DeleteEntryPreview(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var msg utils.MessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
			t.Fatalf("failed to decode MessageResponse: %v", err)
		}
		if !strings.Contains(msg.Message, idStr) || !strings.Contains(msg.Message, db.ID.String()) {
			t.Fatalf("unexpected delete message: %q", msg.Message)
		}

		updated, err := r.GetEntry(ctx, db.ID, entry.ID)
		if err != nil {
			t.Fatalf("GetEntry failed: %v", err)
		}
		if updated.PreviewSize != 0 {
			t.Fatalf("expected preview_filesize=0, got %d", updated.PreviewSize)
		}

		stats, err := r.GetDatabaseStats(ctx, db.ID)
		if err != nil {
			t.Fatalf("GetDatabaseStats failed: %v", err)
		}
		if stats.TotalDiskSpaceBytes != entry.Size {
			t.Fatalf("total_disk_space_bytes = %d, want %d", stats.TotalDiskSpaceBytes, entry.Size)
		}

		ev, ok := auditor.last()
		if !ok || ev.action != "entry.preview_delete" || ev.details["preview_filesize_freed"] != uint64(len(preview)) {
			t.Fatalf("unexpected audit event: %+v", ev)
		}

		getReq := newAuthedPreviewRequest(http.MethodGet, db.ID.String(), idStr, "", nil)
		getRec := httptest.NewRecorder()
		handler.GetEntryPreview(getRec, getReq)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("expected GET after DELETE to return 404, got %d", getRec.Code)
		}
	})

	t.Run("404 when no preview or missing DB/entry, 409 on blocked statuses", func(t *testing.T) {
		r, store, handler, _, db := setupPreviewTestEnv(t, "image", false, nil)
		noPreview := createTestEntry(t, r, store, db, repo.EntryStatusReady, nil)

		for _, tc := range []struct {
			dbID string
			id   string
			code int
		}{
			{db.ID.String(), formatInt64(noPreview.ID), http.StatusNotFound},
			{ulid.New().String(), "1", http.StatusNotFound},
			{db.ID.String(), "99999", http.StatusNotFound},
			{"bad-ulid", "1", http.StatusBadRequest},
		} {
			req := newAuthedPreviewRequest(http.MethodDelete, tc.dbID, tc.id, "", nil)
			rec := httptest.NewRecorder()
			handler.DeleteEntryPreview(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("db=%s id=%s: expected %d, got %d", tc.dbID, tc.id, tc.code, rec.Code)
			}
		}

		validPreview := makeStaticVP8LWebP(32, 32, 0)
		for _, status := range []repo.EntryStatus{repo.EntryStatusProcessing, repo.EntryStatusQueued, repo.EntryStatusDeleting} {
			e := createTestEntry(t, r, store, db, status, validPreview)
			req := newAuthedPreviewRequest(http.MethodDelete, db.ID.String(), formatInt64(e.ID), "", nil)
			rec := httptest.NewRecorder()
			handler.DeleteEntryPreview(rec, req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status=%v: expected 409, got %d", status, rec.Code)
			}
		}
	})
}

func buildEntryUploadRequest(t *testing.T, dbID string, fileMime, fileName string, fileBytes []byte, previewPart []byte, includePreview bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	fh := make(textproto.MIMEHeader)
	fh.Set("Content-Disposition", `form-data; name="file"; filename="`+fileName+`"`)
	fh.Set("Content-Type", fileMime)
	fpart, err := writer.CreatePart(fh)
	if err != nil {
		t.Fatalf("CreatePart file failed: %v", err)
	}
	if _, err := fpart.Write(fileBytes); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	if err := writer.WriteField("metadata", `{"filename":"`+fileName+`"}`); err != nil {
		t.Fatalf("WriteField metadata failed: %v", err)
	}

	if includePreview {
		ph := make(textproto.MIMEHeader)
		ph.Set("Content-Disposition", `form-data; name="preview"; filename="preview.webp"`)
		ph.Set("Content-Type", "image/webp")
		ppart, err := writer.CreatePart(ph)
		if err != nil {
			t.Fatalf("CreatePart preview failed: %v", err)
		}
		if _, err := ppart.Write(previewPart); err != nil {
			t.Fatalf("write preview failed: %v", err)
		}
	}
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/database/"+dbID+"/entry", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("database_id", dbID)
	return req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "uploader", IsAdmin: true}))
}

func TestPostEntry_OptionalPreviewPart(t *testing.T) {
	t.Run("valid WebP preview on create_preview=true DB returns 201 ready and skips FFmpeg preview generation", func(t *testing.T) {
		conv := &previewTestConverter{canCreatePreview: true}
		_, store, handler, _, db := setupPreviewTestEnv(t, "image", true, conv)

		customPreview := makeStaticVP8LWebP(96, 96, 0)
		req := buildEntryUploadRequest(t, db.ID.String(), "image/jpeg", "photo.jpg", []byte("jpeg-bytes"), customPreview, true)
		rec := httptest.NewRecorder()
		handler.PostEntry(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
		}
		if conv.previewGenCalls != 0 {
			t.Fatalf("expected FFmpeg preview generation to be skipped, got %d calls", conv.previewGenCalls)
		}

		var resp entryhandler.EntryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode EntryResponse failed: %v", err)
		}
		if resp.Status != "ready" || resp.PreviewSize != uint64(len(customPreview)) {
			t.Fatalf("unexpected EntryResponse: %+v", resp)
		}

		stored, err := store.ReadPreview(context.Background(), db.ID, resp.EntryID)
		if err != nil {
			t.Fatalf("ReadPreview failed: %v", err)
		}
		got, _ := io.ReadAll(stored)
		_ = stored.Close()
		if !bytes.Equal(got, customPreview) {
			t.Fatal("stored custom preview mismatch")
		}
	})

	t.Run("valid WebP preview on file DB returns 201 ready and stores preview", func(t *testing.T) {
		_, store, handler, _, db := setupPreviewTestEnv(t, "file", false, nil)

		customPreview := makeAnimatedVP8XWebP(100, 100)
		req := buildEntryUploadRequest(t, db.ID.String(), "application/pdf", "manual.pdf", []byte("%PDF-1.4"), customPreview, true)
		rec := httptest.NewRecorder()
		handler.PostEntry(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp entryhandler.EntryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode EntryResponse failed: %v", err)
		}
		if resp.Status != "ready" || resp.PreviewSize != uint64(len(customPreview)) {
			t.Fatalf("unexpected EntryResponse: %+v", resp)
		}

		stored, err := store.ReadPreview(context.Background(), db.ID, resp.EntryID)
		if err != nil {
			t.Fatalf("ReadPreview failed: %v", err)
		}
		got, _ := io.ReadAll(stored)
		_ = stored.Close()
		if !bytes.Equal(got, customPreview) {
			t.Fatal("stored custom preview mismatch")
		}
	})

	t.Run("invalid preview part returns 400/413/415 without creating entry", func(t *testing.T) {
		r, _, handler, _, db := setupPreviewTestEnv(t, "file", false, nil)
		ctx := context.Background()

		valid := makeStaticVP8LWebP(64, 64, 0)
		oversized := make([]byte, media.MaxPreviewBytes+1)
		copy(oversized, valid)

		cases := []struct {
			name     string
			preview  []byte
			wantCode int
		}{
			{"empty preview part", []byte{}, http.StatusBadRequest},
			{"oversized preview part", oversized, http.StatusRequestEntityTooLarge},
			{"non-WebP PNG preview part", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), http.StatusUnsupportedMediaType},
			{"201x200 preview part", makeStaticVP8WebP(201, 200, 0), http.StatusBadRequest},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				req := buildEntryUploadRequest(t, db.ID.String(), "application/pdf", "bad.pdf", []byte("%PDF-1.4"), tc.preview, true)
				rec := httptest.NewRecorder()
				handler.PostEntry(rec, req)

				if rec.Code != tc.wantCode {
					t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
				}
			})
		}

		entries, err := r.GetEntries(ctx, db.ID, repo.QueryOptions{})
		if err != nil {
			t.Fatalf("GetEntries failed: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("expected no entries to be created when preview validation fails, found %d", len(entries))
		}
	})
}

func formatInt64(v int64) string {
	return json.Number(string(appendInt64(nil, v))).String()
}

func appendInt64(dst []byte, v int64) []byte {
	if v == 0 {
		return append(dst, '0')
	}
	if v < 0 {
		dst = append(dst, '-')
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return append(dst, buf[i:]...)
}

type failingPreviewReadStorage struct {
	storage.StorageProvider
	readErr error
}

func (s *failingPreviewReadStorage) ReadPreview(_ context.Context, _ ulid.ULID, _ int64) (io.ReadCloser, error) {
	return nil, s.readErr
}

type failPreviewDBRepo struct {
	repo.Repository
	updateErr error
}

func (r *failPreviewDBRepo) UpdateEntryPreviewSize(_ context.Context, _ repo.ULID, _ int64, _ uint64, _ repo.PreviewUpdateMode) (repo.PreviewState, uint64, error) {
	return repo.PreviewState{}, 0, r.updateErr
}

func TestGetEntryPreview_StorageIOErrorReturns500(t *testing.T) {
	_, store, handler, _, db := setupPreviewTestEnv(t, "file", false, nil)
	handler.Storage = &failingPreviewReadStorage{
		StorageProvider: store,
		readErr:         errors.New("disk I/O failure"),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/database/"+db.ID.String()+"/entry/1/preview", nil)
	req.SetPathValue("database_id", db.ID.String())
	req.SetPathValue("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "tester"}))
	rec := httptest.NewRecorder()

	handler.GetEntryPreview(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on non-notfound storage error, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPutEntryPreview_RollbackRestoresPreviousPreviewOnDBFailure(t *testing.T) {
	r, store, handler, _, db := setupPreviewTestEnv(t, "file", false, nil)
	ctx := context.Background()

	origPreview := makeStaticVP8LWebP(64, 64, 1)
	written, err := store.WritePreview(ctx, db.ID, 1, bytes.NewReader(origPreview))
	if err != nil {
		t.Fatalf("WritePreview seed failed: %v", err)
	}
	entry, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "seeded.bin",
		MimeType:    "application/octet-stream",
		Size:        128,
		PreviewSize: uint64(written),
		Status:      repo.EntryStatusReady,
	})
	if err != nil {
		t.Fatalf("CreateEntry failed: %v", err)
	}

	handler.Repo = &failPreviewDBRepo{
		Repository: r,
		updateErr:  errors.New("simulated DB commit failure"),
	}

	newPreview := makeStaticVP8LWebP(100, 100, 2)
	req := httptest.NewRequest(http.MethodPut, "/api/database/"+db.ID.String()+"/entry/"+formatInt64(entry.ID)+"/preview", bytes.NewReader(newPreview))
	req.Header.Set("Content-Type", "image/webp")
	req.SetPathValue("database_id", db.ID.String())
	req.SetPathValue("id", formatInt64(entry.ID))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "editor"}))
	rec := httptest.NewRecorder()

	handler.PutEntryPreview(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	stored, err := store.ReadPreview(ctx, db.ID, entry.ID)
	if err != nil {
		t.Fatalf("expected previous preview to remain readable after failed PUT: %v", err)
	}
	got, _ := io.ReadAll(stored)
	_ = stored.Close()
	if !bytes.Equal(got, origPreview) {
		t.Fatalf("expected previous preview bytes to be restored after failed PUT")
	}
}

func TestPostEntryPreview_RollbackDeletesOrphanOnDBConflict(t *testing.T) {
	r, store, handler, _, db := setupPreviewTestEnv(t, "file", false, nil)
	ctx := context.Background()

	entry, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName: "empty.bin",
		MimeType: "application/octet-stream",
		Size:     128,
		Status:   repo.EntryStatusReady,
	})
	if err != nil {
		t.Fatalf("CreateEntry failed: %v", err)
	}

	handler.Repo = &failPreviewDBRepo{
		Repository: r,
		updateErr:  customerrors.ErrConflict,
	}

	preview := makeStaticVP8LWebP(64, 64, 3)
	req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entry/"+formatInt64(entry.ID)+"/preview", bytes.NewReader(preview))
	req.Header.Set("Content-Type", "image/webp")
	req.SetPathValue("database_id", db.ID.String())
	req.SetPathValue("id", formatInt64(entry.ID))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "creator"}))
	rec := httptest.NewRecorder()

	handler.PostEntryPreview(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, err := store.ReadPreview(ctx, db.ID, entry.ID); !errors.Is(err, customerrors.ErrNotFound) {
		t.Fatalf("expected orphaned preview to be deleted on ErrConflict when none existed beforehand, got err=%v", err)
	}
}

func TestPreviewConcurrency_StorageAndDBStayConsistent(t *testing.T) {
	r, store, handler, _, db := setupPreviewTestEnv(t, "file", false, nil)
	ctx := context.Background()

	entry, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName: "race.bin",
		MimeType: "application/octet-stream",
		Size:     200,
		Status:   repo.EntryStatusReady,
	})
	if err != nil {
		t.Fatalf("CreateEntry failed: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			payload := makeStaticVP8LWebP(40+i, 40+i, i+1)
			req := httptest.NewRequest(http.MethodPut, "/api/database/"+db.ID.String()+"/entry/"+formatInt64(entry.ID)+"/preview", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "image/webp")
			req.SetPathValue("database_id", db.ID.String())
			req.SetPathValue("id", formatInt64(entry.ID))
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "racer"}))
			rec := httptest.NewRecorder()
			handler.PutEntryPreview(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("concurrent PUT %d got status %d: %s", i, rec.Code, rec.Body.String())
			}
		}()
	}
	wg.Wait()

	state, err := r.GetEntryPreviewState(ctx, db.ID, entry.ID)
	if err != nil {
		t.Fatalf("GetEntryPreviewState failed: %v", err)
	}
	stored, err := store.ReadPreview(ctx, db.ID, entry.ID)
	if err != nil {
		t.Fatalf("ReadPreview failed: %v", err)
	}
	got, _ := io.ReadAll(stored)
	_ = stored.Close()

	if uint64(len(got)) != state.PreviewSize {
		t.Fatalf("storage/DB size mismatch after concurrent PUTs: storage=%d db=%d", len(got), state.PreviewSize)
	}
	stats, err := r.GetDatabaseStats(ctx, db.ID)
	if err != nil {
		t.Fatalf("GetDatabaseStats failed: %v", err)
	}
	if stats.TotalDiskSpaceBytes != 200+state.PreviewSize {
		t.Fatalf("expected total_disk_space_bytes=%d, got %d", 200+state.PreviewSize, stats.TotalDiskSpaceBytes)
	}
}

func TestPostEntry_AsyncAndQueuedWithCustomPreviewHTTP(t *testing.T) {
	t.Run("large async upload with custom preview stores custom preview", func(t *testing.T) {
		conv := &previewTestConverter{canCreatePreview: true}
		r, store, handler, _, db := setupPreviewTestEnv(t, "image", true, conv)
		handler.MaxSyncUploadSizeBytes = 1024

		customPreview := makeAnimatedVP8XWebP(120, 90)
		largePayload := bytes.Repeat([]byte("JPEGDATA"), 8192) // 64 KiB > 1 KiB sync limit
		req := buildEntryUploadRequest(t, db.ID.String(), "image/jpeg", "big.jpg", largePayload, customPreview, true)
		rec := httptest.NewRecorder()
		handler.PostEntry(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp entryhandler.PartialEntryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode PartialEntryResponse failed: %v", err)
		}

		// Wait for async worker to finish finalization.
		var final entryhandler.EntryResponse
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			metaReq := httptest.NewRequest(http.MethodGet, "/api/database/"+db.ID.String()+"/entry/"+formatInt64(resp.EntryID), nil)
			metaReq.SetPathValue("database_id", db.ID.String())
			metaReq.SetPathValue("id", formatInt64(resp.EntryID))
			metaReq = metaReq.WithContext(context.WithValue(metaReq.Context(), utils.UserKey, &repo.User{Username: "tester"}))
			metaRec := httptest.NewRecorder()
			handler.GetEntryMeta(metaRec, metaReq)
			if metaRec.Code == http.StatusOK && json.Unmarshal(metaRec.Body.Bytes(), &final) == nil && final.Status == "ready" {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if final.Status != "ready" {
			t.Fatalf("expected entry to become ready, got %+v", final)
		}
		if final.PreviewSize != uint64(len(customPreview)) {
			t.Fatalf("expected PreviewSize=%d, got %d", len(customPreview), final.PreviewSize)
		}
		if conv.previewGenCalls != 0 {
			t.Fatalf("expected 0 auto preview generator calls, got %d", conv.previewGenCalls)
		}
		stored, err := store.ReadPreview(context.Background(), db.ID, resp.EntryID)
		if err != nil {
			t.Fatalf("ReadPreview failed: %v", err)
		}
		got, _ := io.ReadAll(stored)
		_ = stored.Close()
		if !bytes.Equal(got, customPreview) {
			t.Fatal("stored preview does not match custom preview after async finalization")
		}
		_ = r
	})

	t.Run("queued upload with custom preview skips FFmpeg preview slot", func(t *testing.T) {
		ctx := context.Background()
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		r, err := sqlite.NewRepository(":memory:")
		if err != nil {
			t.Fatalf("failed to create repo: %v", err)
		}
		t.Cleanup(func() { _ = r.Close() })
		if err := r.MigrateUp(ctx); err != nil {
			t.Fatalf("failed to migrate: %v", err)
		}
		store := &localstorage.LocalStorage{RootPath: filepath.Join(t.TempDir(), "storage")}
		db, err := r.CreateDatabase(ctx, repo.Database{
			Name:        "queue_http_db",
			ContentType: "image",
			NMaxQueued:  5,
			Config: repo.DatabaseConfig{
				AutoConversion: "image/webp",
				CreatePreview:  true,
			},
		})
		if err != nil {
			t.Fatalf("CreateDatabase failed: %v", err)
		}
		conv := &previewTestConverter{canCreatePreview: true}
		// 0 processing capacity forces conversion uploads into queue immediately.
		proc := processing.NewProcessor(r, store, conv, 0, 0, logger)
		t.Cleanup(func() { _ = proc.Shutdown(context.Background()) })

		handler := &entryhandler.EntryHandler{
			Repo:                   r,
			Storage:                store,
			MediaConverter:         conv,
			Processor:              proc,
			Auditor:                &recordingAuditor{},
			Logger:                 logger,
			MaxSyncUploadSizeBytes: 8 << 20,
		}

		customPreview := makeStaticVP8WebP(88, 77, 5)
		req := buildEntryUploadRequest(t, db.ID.String(), "image/jpeg", "queued.jpg", []byte("jpeg-bytes"), customPreview, true)
		rec := httptest.NewRecorder()
		handler.PostEntry(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202 queued, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp entryhandler.PartialEntryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if resp.Status != "queued" {
			t.Fatalf("expected status queued, got %q", resp.Status)
		}
		persisted, err := r.GetEntry(ctx, db.ID, resp.EntryID)
		if err != nil {
			t.Fatalf("GetEntry failed: %v", err)
		}
		if persisted.PreviewSize != uint64(len(customPreview)) {
			t.Fatalf("expected persisted PreviewSize=%d, got %d", len(customPreview), persisted.PreviewSize)
		}
	})
}
