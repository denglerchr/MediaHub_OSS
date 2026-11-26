package databasehandler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
)

type spyDatabaseRepo struct {
	repository.Repository
	called bool
}

func (r *spyDatabaseRepo) GetDatabase(ctx context.Context, dbID repository.ULID) (repository.Database, error) {
	r.called = true
	return repository.Database{}, nil
}

func (r *spyDatabaseRepo) DeleteDatabase(ctx context.Context, dbID repository.ULID) error {
	r.called = true
	return nil
}

type spyDatabaseStorage struct {
	storage.StorageProvider
	called bool
}

func (s *spyDatabaseStorage) DeleteDatabase(ctx context.Context, dbID ulid.ULID) error {
	s.called = true
	return nil
}

func TestDeleteDatabase_InvalidDatabaseIDReturns400WithoutTouchingStorageOrRepo(t *testing.T) {
	for _, badID := range []string{"previews", "..", ".", `foo\bar`} {
		t.Run(badID, func(t *testing.T) {
			spyRepo := &spyDatabaseRepo{}
			spyStorage := &spyDatabaseStorage{}
			h := &DatabaseHandler{
				Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
				Repo:    spyRepo,
				Storage: spyStorage,
			}

			req := httptest.NewRequest(http.MethodDelete, "/api/database/invalid", nil)
			req.SetPathValue("database_id", badID)
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repository.User{Username: "admin", IsAdmin: true}))
			rec := httptest.NewRecorder()

			h.DeleteDatabase(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400 for %q, got %d: %s", badID, rec.Code, rec.Body.String())
			}
			var errResp utils.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
				t.Fatalf("failed to decode JSON error response for %q: %v", badID, err)
			}
			if errResp.Error != "Invalid database_id path parameter" {
				t.Fatalf("unexpected error message for %q: %q", badID, errResp.Error)
			}
			if spyRepo.called {
				t.Fatalf("expected repo not to be called for invalid database_id %q", badID)
			}
			if spyStorage.called {
				t.Fatalf("expected storage not to be called for invalid database_id %q", badID)
			}
		})
	}
}

func TestDatabaseHousekeepingValidationAndBodyLimits(t *testing.T) {
	dbID := "01HGFB9Z5W7ABCDEFGHJKMNPQR"
	spyRepo := &spyDatabaseRepo{}
	h := &DatabaseHandler{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Repo:   spyRepo,
	}

	malformedPayloads := []struct {
		name string
		hk   HousekeepingPayload
	}{
		{"invalid interval", HousekeepingPayload{Interval: "bad", DiskSpace: "100G", MaxAge: "30d"}},
		{"invalid disk_space unit", HousekeepingPayload{Interval: "1h", DiskSpace: "100XB", MaxAge: "30d"}},
		{"invalid max_age unit", HousekeepingPayload{Interval: "1h", DiskSpace: "100G", MaxAge: "30weeks"}},
	}

	for _, tc := range malformedPayloads {
		t.Run("CreateDatabase "+tc.name+" returns 400", func(t *testing.T) {
			spyRepo.called = false
			payload := DatabaseCreatePayload{
				Name:         "test_db",
				ContentType:  "image",
				Housekeeping: tc.hk,
			}
			body, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/api/database", bytes.NewReader(body))
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repository.User{Username: "admin", IsAdmin: true}))
			rec := httptest.NewRecorder()

			h.CreateDatabase(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request, got %d: %s", rec.Code, rec.Body.String())
			}
		})

		t.Run("UpdateDatabase "+tc.name+" returns 400", func(t *testing.T) {
			payload := DatabaseUpdatePayload{
				Name:         "test_db",
				Housekeeping: tc.hk,
			}
			body, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPut, "/api/database/"+dbID, bytes.NewReader(body))
			req.SetPathValue("database_id", dbID)
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repository.User{Username: "admin", IsAdmin: true}))
			rec := httptest.NewRecorder()

			h.UpdateDatabase(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("valid housekeeping aliases 100GB and 30days parse cleanly", func(t *testing.T) {
		hk, err := (HousekeepingPayload{Interval: "1h", DiskSpace: "100GB", MaxAge: "30days"}).toModel()
		if err != nil {
			t.Fatalf("expected valid aliases to succeed, got %v", err)
		}
		if hk.DiskSpace != 100*(1<<30) {
			t.Fatalf("unexpected disk space: %d", hk.DiskSpace)
		}
	})

	t.Run("oversized JSON bodies return 413 across database endpoints", func(t *testing.T) {
		oversized := strings.Repeat("a", int(utils.MaxJSONBodyBytes)+16)
		bigBody := `{"name":"` + oversized + `"}`

		req1 := httptest.NewRequest(http.MethodPost, "/api/database", strings.NewReader(bigBody))
		req1 = req1.WithContext(context.WithValue(req1.Context(), utils.UserKey, &repository.User{Username: "admin", IsAdmin: true}))
		rec1 := httptest.NewRecorder()
		h.CreateDatabase(rec1, req1)
		if rec1.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("CreateDatabase expected 413, got %d", rec1.Code)
		}

		req2 := httptest.NewRequest(http.MethodPut, "/api/database/"+dbID, strings.NewReader(bigBody))
		req2.SetPathValue("database_id", dbID)
		req2 = req2.WithContext(context.WithValue(req2.Context(), utils.UserKey, &repository.User{Username: "admin", IsAdmin: true}))
		rec2 := httptest.NewRecorder()
		h.UpdateDatabase(rec2, req2)
		if rec2.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("UpdateDatabase expected 413, got %d", rec2.Code)
		}

		req3 := httptest.NewRequest(http.MethodPost, "/api/database/"+dbID+"/field", strings.NewReader(bigBody))
		req3.SetPathValue("database_id", dbID)
		rec3 := httptest.NewRecorder()
		h.AddField(rec3, req3)
		if rec3.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("AddField expected 413, got %d", rec3.Code)
		}

		req4 := httptest.NewRequest(http.MethodPatch, "/api/database/"+dbID+"/field/1", strings.NewReader(bigBody))
		req4.SetPathValue("database_id", dbID)
		req4.SetPathValue("field_id", "1")
		rec4 := httptest.NewRecorder()
		h.UpdateField(rec4, req4)
		if rec4.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("UpdateField expected 413, got %d", rec4.Code)
		}
	})
}
