package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"mediahub_oss/internal/httpserver/auth"
	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
)

type mockPermHolder struct {
	isGlobalAdmin bool
	userULID      repo.ULID
	hasPerm       bool
}

func (m *mockPermHolder) IsGlobalAdmin() bool {
	return m.isGlobalAdmin
}

func (m *mockPermHolder) HasPermission(database repo.ULID, ag repo.AccessGrant) bool {
	return m.hasPerm
}

func (m *mockPermHolder) GetUserULID() repo.ULID {
	return m.userULID
}

func (m *mockPermHolder) GetAllPermissions(ctx context.Context) (map[repo.ULID]repo.AccessGrant, error) {
	return map[repo.ULID]repo.AccessGrant{}, nil
}

func (m *mockPermHolder) HasGlobalScope(ag repo.AccessGrant) bool {
	return m.isGlobalAdmin
}

func (m *mockPermHolder) EffectiveScope() repo.AccessGrant {
	if m.isGlobalAdmin {
		return repo.NewAccessGrant(true, true, true, true, true)
	}
	return 0
}

func TestAuthMiddleware_JSONErrorResponses(t *testing.T) {
	am := auth.NewAuthMiddleware(nil, "test_jwt_secret_key_123456789012")

	handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("missing auth header returns 401 JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}
		if ctype := rec.Header().Get("Content-Type"); ctype != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ctype)
		}

		var errResp utils.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode JSON error response: %v", err)
		}
		if errResp.Error == "" {
			t.Fatalf("expected non-empty error message in JSON payload")
		}
	})

	t.Run("invalid credentials format returns 401 JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "InvalidHeaderFormat")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}
		if ctype := rec.Header().Get("Content-Type"); ctype != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ctype)
		}

		var errResp utils.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode JSON error response: %v", err)
		}
		if errResp.Error == "" {
			t.Fatalf("expected non-empty error message in JSON payload")
		}
	})
}

func TestRequireGlobalAdmin_JSONErrorResponse(t *testing.T) {
	am := auth.NewAuthMiddleware(nil, "test_jwt_secret_key_123456789012")

	handler := am.RequireGlobalAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("non-admin returns 403 JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
		holder := &mockPermHolder{isGlobalAdmin: false}
		req = req.WithContext(context.WithValue(req.Context(), utils.PermissionHolderKey, holder))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}
		if ctype := rec.Header().Get("Content-Type"); ctype != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ctype)
		}

		var errResp utils.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode JSON error response: %v", err)
		}
		if errResp.Error != "Forbidden: Admin access required" {
			t.Fatalf("unexpected error message: %q", errResp.Error)
		}
	})
}

func TestRequireDatabasePermission_JSONErrorResponse(t *testing.T) {
	am := auth.NewAuthMiddleware(nil, "test_jwt_secret_key_123456789012")

	handler := am.RequireDatabasePermission(repo.AccessView)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("missing database_id returns 400 JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/database", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
		if ctype := rec.Header().Get("Content-Type"); ctype != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ctype)
		}

		var errResp utils.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode JSON error response: %v", err)
		}
		if errResp.Error != "Bad Request: Missing database context" {
			t.Fatalf("unexpected error message: %q", errResp.Error)
		}
	})

	t.Run("lacking database permission returns 403 JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/database/01HGFB9Z5W7ABCDEFGHJKMNPQR", nil)
		req.SetPathValue("database_id", "01HGFB9Z5W7ABCDEFGHJKMNPQR")
		holder := &mockPermHolder{isGlobalAdmin: false, hasPerm: false}
		req = req.WithContext(context.WithValue(req.Context(), utils.PermissionHolderKey, holder))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}
		if ctype := rec.Header().Get("Content-Type"); ctype != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ctype)
		}

		var errResp utils.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode JSON error response: %v", err)
		}
		if errResp.Error == "" {
			t.Fatalf("expected non-empty error message")
		}
	})
}

type mockAuthRepo struct {
	repo.Repository
	user              repo.User
	apiKey            repo.APIKey
	updatedAPIKeyID   repo.ULID
	updatedAPIKeyDur  time.Duration
	updateAPIKeyCount int
}

func (m *mockAuthRepo) GetUserByID(ctx context.Context, id repo.ULID) (repo.User, error) {
	return m.user, nil
}

func (m *mockAuthRepo) GetAPIKeyWithOwnerByHash(ctx context.Context, hash string) (repo.APIKey, repo.User, error) {
	return m.apiKey, m.user, nil
}

func (m *mockAuthRepo) UpdateAPIKeyLastUsed(ctx context.Context, id repo.ULID, dur time.Duration) error {
	m.updatedAPIKeyID = id
	m.updatedAPIKeyDur = dur
	m.updateAPIKeyCount++
	return nil
}

func (m *mockAuthRepo) GetUserPermissions(ctx context.Context, userID repo.ULID, dbID repo.ULID) (repo.UserPermissions, error) {
	return repo.UserPermissions{}, nil
}

func (m *mockAuthRepo) GetAllUserPermissions(ctx context.Context, userID repo.ULID) ([]repo.UserPermissions, error) {
	return nil, nil
}

func TestJWT_ExpClaimEnforcement(t *testing.T) {
	secret := "test_jwt_secret_key_123456789012"
	mockRepo := &mockAuthRepo{
		user: repo.User{
			ID:       "01HGFB9Z5W7ABCDEFGHJKMNPQR",
			Username: "testuser",
		},
	}
	am := auth.NewAuthMiddleware(mockRepo, secret)
	defer am.Close()

	handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("token without exp claim is rejected", func(t *testing.T) {
		claims := jwt.MapClaims{
			"sub": "01HGFB9Z5W7ABCDEFGHJKMNPQR",
			"iat": time.Now().Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenStr, err := token.SignedString([]byte(secret))
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401 for token without exp claim, got %d", rec.Code)
		}
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		claims := jwt.MapClaims{
			"sub": "01HGFB9Z5W7ABCDEFGHJKMNPQR",
			"exp": time.Now().Add(-1 * time.Hour).Unix(),
			"iat": time.Now().Add(-2 * time.Hour).Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenStr, err := token.SignedString([]byte(secret))
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401 for expired token, got %d", rec.Code)
		}
	})

	t.Run("valid token with exp is accepted", func(t *testing.T) {
		claims := jwt.MapClaims{
			"sub": "01HGFB9Z5W7ABCDEFGHJKMNPQR",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
			"iat": time.Now().Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenStr, err := token.SignedString([]byte(secret))
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 for valid token, got %d", rec.Code)
		}
	})
}

func TestAPIKeyWorker_CloseFlushesUpdates(t *testing.T) {
	secret := "test_jwt_secret_key_123456789012"
	keyID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	mockRepo := &mockAuthRepo{
		user: repo.User{
			ID:       "01HGFB9Z5W7ABCDEFGHJKMNPQR",
			Username: "apikeyuser",
		},
		apiKey: repo.APIKey{
			ID:        keyID,
			CreatedAt: time.Now().Add(-1 * time.Hour),
		},
	}

	am := auth.NewAuthMiddleware(mockRepo, secret)

	handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer srv_validapikey12345")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Close immediately, which should trigger flush before worker exits
	am.Close()

	if mockRepo.updateAPIKeyCount != 1 {
		t.Fatalf("expected 1 update to repo on Close, got %d", mockRepo.updateAPIKeyCount)
	}
	if mockRepo.updatedAPIKeyID != keyID {
		t.Fatalf("expected keyID %s, got %s", keyID, mockRepo.updatedAPIKeyID)
	}
}

func TestRequireSelfOrAdmin(t *testing.T) {
	am := auth.NewAuthMiddleware(nil, "secret")
	protected := am.RequireSelfOrAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	user1ID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	user2ID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNXYZ")

	t.Run("global admin caller can manage any user", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+string(user2ID)+"/keys", nil)
		req.SetPathValue("user_ulid", string(user2ID))

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: user1ID, IsAdmin: true})
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.GlobalAdmin{UserULID: user1ID})

		protected.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("regular session user can manage own keys", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+string(user1ID)+"/keys", nil)
		req.SetPathValue("user_ulid", string(user1ID))

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: user1ID, IsAdmin: false})
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: user1ID,
			Scope:    repo.NewAccessGrant(true, true, true, true, true),
		})

		protected.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("regular session user cannot manage other user keys", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+string(user2ID)+"/keys", nil)
		req.SetPathValue("user_ulid", string(user2ID))

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: user1ID, IsAdmin: false})
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: user1ID,
			Scope:    repo.NewAccessGrant(true, true, true, true, true),
		})

		protected.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rec.Code)
		}
	})

	t.Run("API key caller without admin scope rejected on self", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+string(user1ID)+"/keys", nil)
		req.SetPathValue("user_ulid", string(user1ID))

		apiKey := &repo.APIKey{
			ID:        "01HKEY123",
			UserID:    user1ID,
			Scope:     repo.AccessView | repo.AccessEdit, // No AccessAdmin
			CreatedAt: time.Now(),
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: user1ID, IsAdmin: false})
		ctx = context.WithValue(ctx, utils.APIKeyKey, apiKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: user1ID,
			Scope:    apiKey.Scope,
		})

		protected.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rec.Code)
		}
	})

	t.Run("API key caller with admin scope allowed on self", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+string(user1ID)+"/keys", nil)
		req.SetPathValue("user_ulid", string(user1ID))

		apiKey := &repo.APIKey{
			ID:        "01HKEY456",
			UserID:    user1ID,
			Scope:     repo.AccessView | repo.AccessAdmin,
			CreatedAt: time.Now(),
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: user1ID, IsAdmin: false})
		ctx = context.WithValue(ctx, utils.APIKeyKey, apiKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: user1ID,
			Scope:    apiKey.Scope,
		})

		protected.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("APIKeyOfAdmin without AccessAdmin rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+string(user1ID)+"/keys", nil)
		req.SetPathValue("user_ulid", string(user1ID))

		apiKey := &repo.APIKey{
			ID:        "01HKEY789",
			UserID:    user1ID,
			Scope:     repo.AccessView | repo.AccessCreate, // Admin user's key, but no AccessAdmin
			CreatedAt: time.Now(),
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: user1ID, IsAdmin: true})
		ctx = context.WithValue(ctx, utils.APIKeyKey, apiKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.APIKeyOfAdmin{
			UserULID: user1ID,
			Scope:    apiKey.Scope,
		})

		protected.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rec.Code)
		}
	})
}
