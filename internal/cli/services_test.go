package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"mediahub_oss/internal/httpserver/auth"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/ulid"
)

type mockServiceAuthRepo struct {
	repo.Repository
	mu                sync.Mutex
	user              repo.User
	apiKey            repo.APIKey
	updatedAPIKeyID   repo.ULID
	updateAPIKeyCount int
}

func (m *mockServiceAuthRepo) GetAPIKeyWithOwnerByHash(ctx context.Context, hash string) (repo.APIKey, repo.User, error) {
	return m.apiKey, m.user, nil
}

func (m *mockServiceAuthRepo) UpdateAPIKeyLastUsed(ctx context.Context, id repo.ULID, dur time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedAPIKeyID = id
	m.updateAPIKeyCount++
	return nil
}

func TestBackgroundServices_CloseServices(t *testing.T) {
	t.Run("nil receiver does not panic", func(t *testing.T) {
		var svcs *backgroundServices
		svcs.closeServices()
	})

	t.Run("nil fields do not panic", func(t *testing.T) {
		svcs := &backgroundServices{}
		svcs.closeServices()
	})

	t.Run("flushes and stops live authMiddleware", func(t *testing.T) {
		keyID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
		mockRepo := &mockServiceAuthRepo{
			user: repo.User{
				ID:       ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
				Username: "serviceuser",
			},
			apiKey: repo.APIKey{
				ID:        keyID,
				CreatedAt: time.Now().Add(-1 * time.Hour),
			},
		}

		am := auth.NewAuthMiddleware(mockRepo, "test_jwt_secret_key_123456789012")
		svcs := &backgroundServices{
			authMiddleware: am,
		}

		handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer srv_validapikey12345")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}

		svcs.closeServices()
		// Verify repeated calls remain safe after authMiddleware worker stops.
		svcs.closeServices()

		mockRepo.mu.Lock()
		count := mockRepo.updateAPIKeyCount
		updatedID := mockRepo.updatedAPIKeyID
		mockRepo.mu.Unlock()

		if count != 1 {
			t.Fatalf("expected 1 flushed API key update, got %d", count)
		}
		if updatedID != keyID {
			t.Fatalf("expected updated key ID %s, got %s", keyID, updatedID)
		}
	})
}
