package entryhandler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mediahub_oss/internal/httpserver/entryhandler"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/media"
	"mediahub_oss/internal/processing"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/storage/localstorage"
)

func setupFileTestEnvironment(t *testing.T, contentType string, conv *mockMediaConverter) (*sqlite.SQLiteRepository, *localstorage.LocalStorage, *entryhandler.EntryHandler, repo.Database, repo.Entry) {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}

	if err := r.MigrateUp(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	tempDir := t.TempDir()
	storageRoot := filepath.Join(tempDir, "storage")
	_ = os.MkdirAll(storageRoot, 0755)
	store := &localstorage.LocalStorage{RootPath: storageRoot}

	db, err := r.CreateDatabase(ctx, repo.Database{
		Name:        "test_" + contentType,
		ContentType: contentType,
	})
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	if conv == nil {
		conv = &mockMediaConverter{}
	}
	proc := processing.NewProcessor(r, store, conv, 2, 4, logger)

	handler := &entryhandler.EntryHandler{
		Repo:                   r,
		Storage:                store,
		MediaConverter:         conv,
		Processor:              proc,
		Auditor:                &mockAuditor{},
		Logger:                 logger,
		MaxSyncUploadSizeBytes: 1024,
	}

	mimeType := "image/jpeg"
	filename := "photo.jpg"
	if contentType == "audio" {
		mimeType = "audio/wav"
		filename = "track.wav"
	} else if contentType == "video" {
		mimeType = "video/mp4"
		filename = "movie.mp4"
	} else if contentType == "file" {
		mimeType = "application/pdf"
		filename = "doc.pdf"
	}

	var mediaFields map[string]any
	if contentType == "image" {
		mediaFields = map[string]any{"width": 800, "height": 600}
	} else if contentType == "audio" {
		mediaFields = map[string]any{"duration": 120.0, "channels": 2}
	} else if contentType == "video" {
		mediaFields = map[string]any{"width": 1920, "height": 1080, "duration": 60.0}
	}

	entryData := []byte("original-file-payload-content")
	entry, err := r.CreateEntry(ctx, db, repo.Entry{
		Timestamp:   time.Now(),
		MimeType:    mimeType,
		FileName:    filename,
		Size:        uint64(len(entryData)),
		Status:      repo.EntryStatusReady,
		MediaFields: mediaFields,
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	_, err = store.Write(ctx, db.ID, entry.ID, bytes.NewReader(entryData))
	if err != nil {
		t.Fatalf("failed to write test entry data: %v", err)
	}

	return r, store, handler, db, entry
}

func TestGetEntryFile_Normal(t *testing.T) {
	r, _, handler, db, entry := setupFileTestEnvironment(t, "image", nil)
	defer r.Close()

	t.Run("Standard binary download", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d (body: %s)", w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Type") != "image/jpeg" {
			t.Errorf("expected Content-Type image/jpeg, got %s", w.Header().Get("Content-Type"))
		}
		if w.Body.String() != "original-file-payload-content" {
			t.Errorf("unexpected body content: %s", w.Body.String())
		}
	})

	t.Run("JSON Base64 content negotiation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req.Header.Set("Accept", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}
		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", w.Header().Get("Content-Type"))
		}

		var jsonResp entryhandler.FileJSONResponse
		if err := json.Unmarshal(w.Body.Bytes(), &jsonResp); err != nil {
			t.Fatalf("failed to decode JSON response: %v", err)
		}
		if jsonResp.Filename != "photo.jpg" {
			t.Errorf("expected filename photo.jpg, got %s", jsonResp.Filename)
		}
		if jsonResp.MimeType != "image/jpeg" {
			t.Errorf("expected mime_type image/jpeg, got %s", jsonResp.MimeType)
		}
		if !strings.HasPrefix(jsonResp.Data, "data:image/jpeg;base64,") {
			t.Errorf("unexpected data prefix: %s", jsonResp.Data)
		}
	})
}

func TestGetEntryFile_ImageTransformations(t *testing.T) {
	var capturedOpts media.ConversionOptions
	conv := &mockMediaConverter{
		convertStreamToFileFunc: func(ctx context.Context, inputData io.ReadSeeker, inputMimeType string, opts media.ConversionOptions) (*os.File, error) {
			capturedOpts = opts
			tmp, err := os.CreateTemp("", "trans-test-*.tmp")
			if err != nil {
				return nil, err
			}
			_, _ = tmp.WriteString("transformed-image-bytes")
			_, _ = tmp.Seek(0, io.SeekStart)
			return tmp, nil
		},
	}

	r, _, handler, db, entry := setupFileTestEnvironment(t, "image", conv)
	defer r.Close()

	t.Run("Format conversion to webp", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?format=webp", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Type") != "image/webp" {
			t.Errorf("expected Content-Type image/webp, got %s", w.Header().Get("Content-Type"))
		}
		if !strings.Contains(w.Header().Get("Content-Disposition"), "photo.webp") {
			t.Errorf("expected filename photo.webp in disposition, got %s", w.Header().Get("Content-Disposition"))
		}
		if w.Body.String() != "transformed-image-bytes" {
			t.Errorf("unexpected body content: %s", w.Body.String())
		}
		if capturedOpts.TargetMimeType != "image/webp" {
			t.Errorf("expected TargetMimeType image/webp, got %s", capturedOpts.TargetMimeType)
		}
	})

	t.Run("Resize with cut mode", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?width=800&height=480&fit=cut", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		if capturedOpts.Width != 800 || capturedOpts.Height != 480 || capturedOpts.Fit != "cut" {
			t.Errorf("unexpected options: %+v", capturedOpts)
		}
	})

	t.Run("Single dimension resize", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?width=640", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		if capturedOpts.Width != 640 || capturedOpts.Height != 0 {
			t.Errorf("unexpected options: %+v", capturedOpts)
		}
	})

	t.Run("Transformed output as JSON Base64", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?format=webp&width=400", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req.Header.Set("Accept", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", w.Header().Get("Content-Type"))
		}

		var jsonResp entryhandler.FileJSONResponse
		if err := json.Unmarshal(w.Body.Bytes(), &jsonResp); err != nil {
			t.Fatalf("failed to decode JSON response: %v", err)
		}
		if jsonResp.Filename != "photo.webp" {
			t.Errorf("expected filename photo.webp, got %s", jsonResp.Filename)
		}
		if jsonResp.MimeType != "image/webp" {
			t.Errorf("expected mime_type image/webp, got %s", jsonResp.MimeType)
		}
		if !strings.HasPrefix(jsonResp.Data, "data:image/webp;base64,") {
			t.Errorf("unexpected data prefix: %s", jsonResp.Data)
		}
	})
}

func TestGetEntryFile_AudioTransformations(t *testing.T) {
	conv := &mockMediaConverter{
		convertStreamToFileFunc: func(ctx context.Context, inputData io.ReadSeeker, inputMimeType string, opts media.ConversionOptions) (*os.File, error) {
			tmp, err := os.CreateTemp("", "audio-trans-*.tmp")
			if err != nil {
				return nil, err
			}
			_, _ = tmp.WriteString("transcoded-audio-bytes")
			_, _ = tmp.Seek(0, io.SeekStart)
			return tmp, nil
		},
	}

	r, _, handler, db, entry := setupFileTestEnvironment(t, "audio", conv)
	defer r.Close()

	t.Run("Valid audio format conversion", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?format=opus", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Type") != "audio/opus" {
			t.Errorf("expected Content-Type audio/opus, got %s", w.Header().Get("Content-Type"))
		}
	})

	t.Run("Disallowing resolution on audio returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?width=800", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", w.Code)
		}
	})

	t.Run("Disallowing fit on audio returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?fit=cut", db.ID, entry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", w.Code)
		}
	})
}

func TestGetEntryFile_VideoReturns501(t *testing.T) {
	r, _, handler, db, entry := setupFileTestEnvironment(t, "video", nil)
	defer r.Close()

	testUrls := []string{
		fmt.Sprintf("/api/database/%s/entry/%d/file?format=webm", db.ID, entry.ID),
		fmt.Sprintf("/api/database/%s/entry/%d/file?width=1280", db.ID, entry.ID),
		fmt.Sprintf("/api/database/%s/entry/%d/file?fit=cut", db.ID, entry.ID),
	}

	for _, url := range testUrls {
		t.Run(url, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.SetPathValue("database_id", db.ID.String())
			req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
			w := httptest.NewRecorder()

			handler.GetEntryFile(w, req)

			if w.Code != http.StatusNotImplemented {
				t.Errorf("expected 501 Not Implemented, got %d", w.Code)
			}
		})
	}
}

func TestGetEntryFile_FileReturns400(t *testing.T) {
	r, _, handler, db, entry := setupFileTestEnvironment(t, "file", nil)
	defer r.Close()

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?format=pdf", db.ID, entry.ID), nil)
	req.SetPathValue("database_id", db.ID.String())
	req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	w := httptest.NewRecorder()

	handler.GetEntryFile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w.Code)
	}
}

func TestGetEntryFile_ValidationErrors(t *testing.T) {
	r, _, handler, db, entry := setupFileTestEnvironment(t, "image", nil)
	defer r.Close()

	testCases := []struct {
		name string
		url  string
	}{
		{"negative width", fmt.Sprintf("/api/database/%s/entry/%d/file?width=-10", db.ID, entry.ID)},
		{"zero width", fmt.Sprintf("/api/database/%s/entry/%d/file?width=0", db.ID, entry.ID)},
		{"non-integer width", fmt.Sprintf("/api/database/%s/entry/%d/file?width=abc", db.ID, entry.ID)},
		{"negative height", fmt.Sprintf("/api/database/%s/entry/%d/file?height=-10", db.ID, entry.ID)},
		{"non-integer height", fmt.Sprintf("/api/database/%s/entry/%d/file?height=def", db.ID, entry.ID)},
		{"invalid fit option", fmt.Sprintf("/api/database/%s/entry/%d/file?fit=distort", db.ID, entry.ID)},
		{"unsupported target format", fmt.Sprintf("/api/database/%s/entry/%d/file?format=unsupported", db.ID, entry.ID)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			req.SetPathValue("database_id", db.ID.String())
			req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
			w := httptest.NewRecorder()

			handler.GetEntryFile(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request for %s, got %d (body: %s)", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestGetEntryFile_CapacityExhausted(t *testing.T) {
	conv := &mockMediaConverter{
		convertStreamToFileFunc: func(ctx context.Context, inputData io.ReadSeeker, inputMimeType string, opts media.ConversionOptions) (*os.File, error) {
			tmp, _ := os.CreateTemp("", "dummy-*.tmp")
			return tmp, nil
		},
	}

	r, _, handler, db, entry := setupFileTestEnvironment(t, "image", conv)
	defer r.Close()

	// Fill all sync slots by holding an active converted stream open
	handler.Processor.NFfmpegTotal = 1
	heldStream, _, err := handler.Processor.ProcessOutgoingEntry(context.Background(), bytes.NewReader([]byte("dummy")), "image/jpeg", media.ConversionOptions{TargetMimeType: "image/webp"})
	if err != nil {
		t.Fatalf("failed to reserve slot via ProcessOutgoingEntry: %v", err)
	}
	defer heldStream.Close()

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?format=webp", db.ID, entry.ID), nil)
	req.SetPathValue("database_id", db.ID.String())
	req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	w := httptest.NewRecorder()

	handler.GetEntryFile(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 Service Unavailable when capacity full, got %d", w.Code)
	}
}

func TestGetEntryFile_XSSHardeningAndContentDisposition(t *testing.T) {
	ctx := context.Background()
	r, store, handler, db, _ := setupFileTestEnvironment(t, "file", nil)
	defer r.Close()

	createAndStore := func(filename, mimeType, payload string) repo.Entry {
		entry, err := r.CreateEntry(ctx, db, repo.Entry{
			Timestamp: time.Now(),
			MimeType:  mimeType,
			FileName:  filename,
			Size:      uint64(len(payload)),
			Status:    repo.EntryStatusReady,
		})
		if err != nil {
			t.Fatalf("CreateEntry failed: %v", err)
		}
		if _, err := store.Write(ctx, db.ID, entry.ID, strings.NewReader(payload)); err != nil {
			t.Fatalf("store.Write failed: %v", err)
		}
		return entry
	}

	t.Run("Range requests force attachment on dangerous MIME types and allow inline on safe media", func(t *testing.T) {
		cases := []struct {
			name            string
			filename        string
			mimeType        string
			wantDisposition string
		}{
			{"text/html forced attachment", "page.html", "text/html", "attachment"},
			{"image/svg+xml forced attachment", "icon.svg", "image/svg+xml", "attachment"},
			{"application/javascript forced attachment", "app.js", "application/javascript", "attachment"},
			{"application/pdf forced attachment", "doc.pdf", "application/pdf", "attachment"},
			{"video/mp4 allowed inline", "clip.mp4", "video/mp4", "inline"},
			{"audio/mpeg allowed inline", "song.mp3", "audio/mpeg", "inline"},
			{"image/jpeg allowed inline", "pic.jpg", "image/jpeg", "inline"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				entry := createAndStore(tc.filename, tc.mimeType, "0123456789abcdef")

				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file", db.ID, entry.ID), nil)
				req.Header.Set("Range", "bytes=0-3")
				req.SetPathValue("database_id", db.ID.String())
				req.SetPathValue("id", fmt.Sprintf("%d", entry.ID))
				req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
				w := httptest.NewRecorder()

				handler.GetEntryFile(w, req)

				if w.Code != http.StatusPartialContent {
					t.Fatalf("expected 206 Partial Content, got %d: %s", w.Code, w.Body.String())
				}
				if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
					t.Errorf("expected X-Content-Type-Options=nosniff, got %q", got)
				}
				if got := w.Header().Get("Content-Security-Policy"); got != "sandbox" {
					t.Errorf("expected Content-Security-Policy=sandbox, got %q", got)
				}
				cd := w.Header().Get("Content-Disposition")
				mediaType, params, err := mime.ParseMediaType(cd)
				if err != nil {
					t.Fatalf("failed to parse Content-Disposition %q: %v", cd, err)
				}
				if mediaType != tc.wantDisposition {
					t.Errorf("expected disposition %q, got %q (header: %q)", tc.wantDisposition, mediaType, cd)
				}
				if params["filename"] != tc.filename {
					t.Errorf("expected filename %q, got %q", tc.filename, params["filename"])
				}
			})
		}
	})

	t.Run("CRLF and quotes in filename are sanitized and empty filename emits fallback Content-Disposition", func(t *testing.T) {
		evilEntry := createAndStore("bad\r\nX-Injected: yes\"name.html", "text/html", "payload-bytes")
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file", db.ID, evilEntry.ID), nil)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", evilEntry.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.GetEntryFile(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}
		cd := w.Header().Get("Content-Disposition")
		if strings.ContainsAny(cd, "\r\n") {
			t.Fatalf("Content-Disposition contains CR/LF: %q", cd)
		}
		mediaType, params, err := mime.ParseMediaType(cd)
		if err != nil {
			t.Fatalf("failed to parse Content-Disposition %q: %v", cd, err)
		}
		if mediaType != "attachment" {
			t.Errorf("expected attachment disposition, got %q", mediaType)
		}
		if strings.ContainsAny(params["filename"], "\r\n") || !strings.Contains(params["filename"], "bad") {
			t.Errorf("unexpected sanitized filename: %q", params["filename"])
		}

		emptyNameEntry := createAndStore("", "application/octet-stream", "abc")
		req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file", db.ID, emptyNameEntry.ID), nil)
		req2.SetPathValue("database_id", db.ID.String())
		req2.SetPathValue("id", fmt.Sprintf("%d", emptyNameEntry.ID))
		req2 = req2.WithContext(context.WithValue(req2.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w2 := httptest.NewRecorder()

		handler.GetEntryFile(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w2.Code)
		}
		cd2 := w2.Header().Get("Content-Disposition")
		if cd2 == "" {
			t.Fatal("expected Content-Disposition even when entry.FileName is empty")
		}
		_, params2, err := mime.ParseMediaType(cd2)
		if err != nil {
			t.Fatalf("failed to parse fallback Content-Disposition %q: %v", cd2, err)
		}
		if params2["filename"] != fmt.Sprintf("%d", emptyNameEntry.ID) {
			t.Errorf("expected fallback filename %d, got %q", emptyNameEntry.ID, params2["filename"])
		}
	})

	t.Run("Converted file and preview emit security headers", func(t *testing.T) {
		rImg, storeImg, handlerImg, dbImg, entryImg := setupFileTestEnvironment(t, "image", nil)
		defer rImg.Close()

		// Transformed image response headers
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/file?format=webp", dbImg.ID, entryImg.ID), nil)
		req.SetPathValue("database_id", dbImg.ID.String())
		req.SetPathValue("id", fmt.Sprintf("%d", entryImg.ID))
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()
		handlerImg.GetEntryFile(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("transformed response expected nosniff, got %q", got)
		}
		if got := w.Header().Get("Content-Security-Policy"); got != "sandbox" {
			t.Errorf("transformed response expected CSP sandbox, got %q", got)
		}

		// Preview response header
		if _, err := storeImg.WritePreview(ctx, dbImg.ID, entryImg.ID, strings.NewReader("webp-preview-bytes")); err != nil {
			t.Fatalf("WritePreview failed: %v", err)
		}
		reqPrev := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/database/%s/entry/%d/preview", dbImg.ID, entryImg.ID), nil)
		reqPrev.SetPathValue("database_id", dbImg.ID.String())
		reqPrev.SetPathValue("id", fmt.Sprintf("%d", entryImg.ID))
		wPrev := httptest.NewRecorder()
		handlerImg.GetEntryPreview(wPrev, reqPrev)
		if wPrev.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for preview, got %d", wPrev.Code)
		}
		if got := wPrev.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("preview response expected nosniff, got %q", got)
		}
	})
}
