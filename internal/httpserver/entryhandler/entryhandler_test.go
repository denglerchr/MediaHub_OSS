package entryhandler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mediahub_oss/internal/httpserver/entryhandler"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/processing"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
	"mediahub_oss/internal/storage/localstorage"
)

func setupTestEnvironment(t *testing.T) (*sqlite.SQLiteRepository, *localstorage.LocalStorage, *entryhandler.EntryHandler, repo.Database) {
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
		Name:        "test_images",
		ContentType: "image",
	})
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	conv := &mockMediaConverter{}
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

	return r, store, handler, db
}

func TestQueryEntries_InvalidQueryParamsReturn400(t *testing.T) {
	r, _, handler, db := setupTestEnvironment(t)
	defer r.Close()

	tests := []struct {
		name string
		url  string
	}{
		{"invalid limit", "/api/database/" + db.ID.String() + "/entries?limit=invalid"},
		{"invalid offset", "/api/database/" + db.ID.String() + "/entries?offset=abc"},
		{"invalid tstart", "/api/database/" + db.ID.String() + "/entries?tstart=foo"},
		{"invalid tend", "/api/database/" + db.ID.String() + "/entries?tend=bar"},
		{"invalid idstart", "/api/database/" + db.ID.String() + "/entries?idstart=foo"},
		{"invalid idend", "/api/database/" + db.ID.String() + "/entries?idend=bar"},
		{"negative idstart", "/api/database/" + db.ID.String() + "/entries?idstart=-1"},
		{"negative idend", "/api/database/" + db.ID.String() + "/entries?idend=-5"},
		{"inverted id range", "/api/database/" + db.ID.String() + "/entries?idstart=10&idend=5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			req.SetPathValue("database_id", db.ID.String())
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
			w := httptest.NewRecorder()

			handler.QueryEntries(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status 400 for %s, got %d", tt.url, w.Code)
			}
		})
	}
}

func TestQueryEntries_IDRangeFilter(t *testing.T) {
	r, _, handler, db := setupTestEnvironment(t)
	defer r.Close()
	ctx := context.Background()

	for i := int64(1); i <= 5; i++ {
		if _, err := r.CreateEntry(ctx, db, repo.Entry{
			ID:          i,
			FileName:    "item.jpg",
			MimeType:    "image/jpeg",
			Size:        100,
			Status:      repo.EntryStatusReady,
			MediaFields: map[string]any{"width": 100, "height": 100},
		}); err != nil {
			t.Fatalf("CreateEntry %d failed: %v", i, err)
		}
	}

	cases := []struct {
		name    string
		query   string
		wantIDs []int64
	}{
		{"idstart only", "sort_by=id&order=asc&idstart=3", []int64{3, 4, 5}},
		{"idend only", "sort_by=id&order=asc&idend=2", []int64{1, 2}},
		{"both inclusive", "sort_by=id&order=asc&idstart=2&idend=4", []int64{2, 3, 4}},
		{"descending with idend", "sort_by=id&order=desc&idend=3&limit=2", []int64{3, 2}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/database/"+db.ID.String()+"/entries?"+tc.query, nil)
			req.SetPathValue("database_id", db.ID.String())
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
			w := httptest.NewRecorder()

			handler.QueryEntries(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}

			var got []struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("got %d entries, want %d (%v)", len(got), len(tc.wantIDs), got)
			}
			for i, wantID := range tc.wantIDs {
				if got[i].ID != wantID {
					t.Errorf("got[%d].ID = %d, want %d", i, got[i].ID, wantID)
				}
			}
		})
	}
}

func TestSearchEntries_ValidationErrorsReturn400(t *testing.T) {
	r, _, handler, db := setupTestEnvironment(t)
	defer r.Close()

	// Invalid operator payload
	searchBody := map[string]any{
		"filter": map[string]any{
			"operator": "and",
			"conditions": []map[string]any{
				{
					"field":    "filename",
					"operator": "INVALID_OP",
					"value":    "test",
				},
			},
		},
		"pagination": map[string]any{
			"limit": 10,
		},
	}
	bodyBytes, _ := json.Marshal(searchBody)

	req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/search", bytes.NewReader(bodyBytes))
	req.SetPathValue("database_id", db.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	w := httptest.NewRecorder()

	handler.SearchEntries(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid operator search, got %d", w.Code)
	}
}

func TestSearchEntries_MissingLimitReturns400(t *testing.T) {
	r, _, handler, db := setupTestEnvironment(t)
	defer r.Close()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"no pagination object", map[string]any{}},
		{"missing limit", map[string]any{"pagination": map[string]any{"offset": 0}}},
		{"zero limit", map[string]any{"pagination": map[string]any{"limit": 0}}},
		{"negative limit", map[string]any{"pagination": map[string]any{"limit": -5}}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/search", bytes.NewReader(bodyBytes))
			req.SetPathValue("database_id", db.ID.String())
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
			w := httptest.NewRecorder()

			handler.SearchEntries(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status 400 when pagination.limit is missing/invalid, got %d", w.Code)
			}
		})
	}

	// Positive control: a provided limit must be accepted.
	t.Run("provided limit accepted", func(t *testing.T) {
		bodyBytes, _ := json.Marshal(map[string]any{"pagination": map[string]any{"limit": 10}})
		req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/search", bytes.NewReader(bodyBytes))
		req.SetPathValue("database_id", db.ID.String())
		req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
		w := httptest.NewRecorder()

		handler.SearchEntries(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200 when pagination.limit is provided, got %d", w.Code)
		}
	})
}

func TestDeleteEntries_SuccessAndPartialSuccess(t *testing.T) {
	r, store, handler, db := setupTestEnvironment(t)
	defer r.Close()
	ctx := context.Background()

	// Create an entry
	entry, err := r.CreateEntry(ctx, db, repo.Entry{
		FileName:    "test.jpg",
		MimeType:    "image/jpeg",
		Size:        100,
		Status:      repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 800, "height": 600},
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// Write dummy file to storage
	_, _ = store.Write(ctx, db.ID, entry.ID, bytes.NewReader([]byte("test")))

	// Bulk delete
	deleteReq := entryhandler.BulkDeleteRequest{
		IDs: []int64{entry.ID},
	}
	bodyBytes, _ := json.Marshal(deleteReq)

	req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/delete", bytes.NewReader(bodyBytes))
	req.SetPathValue("database_id", db.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	w := httptest.NewRecorder()

	handler.DeleteEntries(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for bulk delete, got %d", w.Code)
	}

	var resp entryhandler.BulkDeleteResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.DeletedCount != 1 {
		t.Errorf("expected deleted_count 1, got %d", resp.DeletedCount)
	}
}

func TestSearchEntries_CoordinateFilters(t *testing.T) {
	r, _, handler, db := setupTestEnvironment(t)
	defer r.Close()
	ctx := context.Background()

	addedField, err := r.AddCustomField(ctx, db.ID, repo.CustomFieldDef{
		Name:      "location",
		Type:      repo.CustomFieldTypeCoordinate,
		IsIndexed: true,
	})
	if err != nil {
		t.Fatalf("failed to add location field: %v", err)
	}
	db.CustomFields = append(db.CustomFields, addedField)

	// Create entry with location
	_, err = r.CreateEntry(ctx, db, repo.Entry{
		FileName: "test.jpg",
		MimeType: "image/jpeg",
		Size:     100,
		Status:   repo.EntryStatusReady,
		MediaFields: map[string]any{"width": 800, "height": 600},
		CustomFields: map[string]any{
			"location": repo.Coordinate{Latitude: 48.137, Longitude: 11.576},
		},
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	// 1. Valid in_box search matching
	validSearch := map[string]any{
		"filter": map[string]any{
			"operator": "and",
			"conditions": []map[string]any{
				{
					"field":    "location",
					"operator": "in_box",
					"value": map[string]any{
						"min_lat": 48.0, "max_lat": 49.0,
						"min_lng": 11.0, "max_lng": 12.0,
					},
				},
			},
		},
		"pagination": map[string]any{"limit": 10},
	}
	bodyBytes, _ := json.Marshal(validSearch)
	req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/search", bytes.NewReader(bodyBytes))
	req.SetPathValue("database_id", db.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	w := httptest.NewRecorder()
	handler.SearchEntries(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for valid in_box search, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Invalid scalar operator on COORDINATE field -> 400
	invalidOpSearch := map[string]any{
		"filter": map[string]any{
			"operator": "and",
			"conditions": []map[string]any{
				{
					"field":    "location",
					"operator": "=",
					"value":    48.0,
				},
			},
		},
		"pagination": map[string]any{"limit": 10},
	}
	bodyBytes, _ = json.Marshal(invalidOpSearch)
	req = httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/search", bytes.NewReader(bodyBytes))
	req.SetPathValue("database_id", db.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	w = httptest.NewRecorder()
	handler.SearchEntries(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for scalar op on coordinate field, got %d", w.Code)
	}

	// 3. Sorting by COORDINATE field -> 400
	sortCoordSearch := map[string]any{
		"sort": map[string]any{
			"field":     "location",
			"direction": "asc",
		},
		"pagination": map[string]any{"limit": 10},
	}
	bodyBytes, _ = json.Marshal(sortCoordSearch)
	req = httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/search", bytes.NewReader(bodyBytes))
	req.SetPathValue("database_id", db.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	w = httptest.NewRecorder()
	handler.SearchEntries(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for sorting by coordinate field, got %d", w.Code)
	}
}

type spyPreviewStorage struct {
	storage.StorageProvider
	readPreviewCalled bool
}

func (s *spyPreviewStorage) ReadPreview(ctx context.Context, dbID ulid.ULID, id int64) (io.ReadCloser, error) {
	s.readPreviewCalled = true
	return nil, os.ErrNotExist
}

func TestGetEntryPreview_InvalidDatabaseIDReturns400WithoutCallingStorage(t *testing.T) {
	for _, badID := range []string{"previews", ".."} {
		t.Run(badID, func(t *testing.T) {
			spy := &spyPreviewStorage{}
			handler := &entryhandler.EntryHandler{
				Storage: spy,
				Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			}

			req := httptest.NewRequest(http.MethodGet, "/api/database/invalid/entry/1/preview", nil)
			req.SetPathValue("database_id", badID)
			req.SetPathValue("id", "1")
			w := httptest.NewRecorder()

			handler.GetEntryPreview(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400 for %q, got %d: %s", badID, w.Code, w.Body.String())
			}
			if spy.readPreviewCalled {
				t.Fatalf("expected storage.ReadPreview not to be called for invalid database_id %q", badID)
			}
		})
	}
}

func TestSearchEntries_ExceedsMaxQueryLimitReturns400(t *testing.T) {
	r, _, handler, db := setupTestEnvironment(t)
	defer r.Close()

	bodyBytes, _ := json.Marshal(map[string]any{
		"pagination": map[string]any{"limit": repo.MaxQueryLimit + 1},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/search", bytes.NewReader(bodyBytes))
	req.SetPathValue("database_id", db.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	w := httptest.NewRecorder()

	handler.SearchEntries(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when limit > MaxQueryLimit, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEntryHandler_OversizedJSONBodiesReturn413(t *testing.T) {
	r, _, handler, db := setupTestEnvironment(t)
	defer r.Close()

	oversized := strings.Repeat("a", int(utils.MaxJSONBodyBytes)+16)
	bigPayload := `{"filename":"` + oversized + `"}`
	userCtx := context.WithValue(context.Background(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true})

	t.Run("PatchEntry 413", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/api/database/"+db.ID.String()+"/entry/1", strings.NewReader(bigPayload)).WithContext(userCtx)
		req.SetPathValue("database_id", db.ID.String())
		req.SetPathValue("id", "1")
		w := httptest.NewRecorder()
		handler.PatchEntry(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", w.Code)
		}
	})

	t.Run("DeleteEntries 413", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/delete", strings.NewReader(bigPayload)).WithContext(userCtx)
		req.SetPathValue("database_id", db.ID.String())
		w := httptest.NewRecorder()
		handler.DeleteEntries(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", w.Code)
		}
	})

	t.Run("SearchEntries 413", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/search", strings.NewReader(bigPayload)).WithContext(userCtx)
		req.SetPathValue("database_id", db.ID.String())
		w := httptest.NewRecorder()
		handler.SearchEntries(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", w.Code)
		}
	})

	t.Run("ExportEntries 413", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/database/"+db.ID.String()+"/entries/export", strings.NewReader(bigPayload)).WithContext(userCtx)
		req.SetPathValue("database_id", db.ID.String())
		w := httptest.NewRecorder()
		handler.ExportEntries(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", w.Code)
		}
	})
}
