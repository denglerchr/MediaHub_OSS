package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"mediahub_oss/internal/httpserver/auth"
	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
)

type mockPermHolder struct {
	isGlobalAdmin bool
	hasPerm       bool
	permErr       error
}

func (m *mockPermHolder) IsGlobalAdmin() bool {
	return m.isGlobalAdmin
}

func (m *mockPermHolder) HasPermission(ctx context.Context, database repo.ULID, ag repo.AccessGrant) (bool, error) {
	if m.permErr != nil {
		return false, m.permErr
	}
	return m.hasPerm, nil
}

func (m *mockPermHolder) GetAllPermissions(ctx context.Context) (map[repo.ULID]repo.AccessGrant, error) {
	return map[repo.ULID]repo.AccessGrant{}, nil
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

	t.Run("invalid database_id returns 400 JSON", func(t *testing.T) {
		for _, badID := range []string{"previews", "..", "db1"} {
			nextCalled := false
			guarded := am.RequireDatabasePermission(repo.AccessView)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/database/invalid", nil)
			req.SetPathValue("database_id", badID)
			holder := &mockPermHolder{isGlobalAdmin: true, hasPerm: true}
			req = req.WithContext(context.WithValue(req.Context(), utils.PermissionHolderKey, holder))
			rec := httptest.NewRecorder()

			guarded.ServeHTTP(rec, req)

			if nextCalled {
				t.Fatalf("expected next handler not to be called for invalid database_id %q", badID)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400 for %q, got %d", badID, rec.Code)
			}
			if ctype := rec.Header().Get("Content-Type"); ctype != "application/json" {
				t.Fatalf("expected Content-Type application/json for %q, got %q", badID, ctype)
			}
			var errResp utils.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
				t.Fatalf("failed to decode JSON error response for %q: %v", badID, err)
			}
			if errResp.Error != "Bad Request: Invalid database ID" {
				t.Fatalf("unexpected error message for %q: %q", badID, errResp.Error)
			}
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
	getUserErr        error
	getAPIKeyErr      error
	getPermsErr       error
	updatedAPIKeyID   repo.ULID
	updatedAPIKeyDur  time.Duration
	updateAPIKeyCount int
}

func (m *mockAuthRepo) GetUserByID(ctx context.Context, id repo.ULID) (repo.User, error) {
	if m.getUserErr != nil {
		return repo.User{}, m.getUserErr
	}
	return m.user, nil
}

func (m *mockAuthRepo) GetUserByUsername(ctx context.Context, username string) (repo.User, error) {
	if m.getUserErr != nil {
		return repo.User{}, m.getUserErr
	}
	return m.user, nil
}

func (m *mockAuthRepo) GetAPIKeyWithOwnerByHash(ctx context.Context, hash string) (repo.APIKey, repo.User, error) {
	if m.getAPIKeyErr != nil {
		return repo.APIKey{}, repo.User{}, m.getAPIKeyErr
	}
	return m.apiKey, m.user, nil
}

func (m *mockAuthRepo) UpdateAPIKeyLastUsed(ctx context.Context, id repo.ULID, dur time.Duration) error {
	m.updatedAPIKeyID = id
	m.updatedAPIKeyDur = dur
	m.updateAPIKeyCount++
	return nil
}

func (m *mockAuthRepo) GetAllUserPermissions(ctx context.Context, userID repo.ULID) ([]repo.UserPermissions, error) {
	if m.getPermsErr != nil {
		return nil, m.getPermsErr
	}
	return nil, nil
}

func TestJWT_ExpClaimEnforcement(t *testing.T) {
	secret := "test_jwt_secret_key_123456789012"
	mockRepo := &mockAuthRepo{
		user: repo.User{
			ID:       ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
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

	t.Run("token with invalid ULID sub is rejected", func(t *testing.T) {
		claims := jwt.MapClaims{
			"sub": "not-a-valid-ulid",
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

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401 for token with invalid ULID sub, got %d", rec.Code)
		}
	})
}

func TestAPIKeyWorker_CloseFlushesUpdates(t *testing.T) {
	secret := "test_jwt_secret_key_123456789012"
	keyID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	mockRepo := &mockAuthRepo{
		user: repo.User{
			ID:       ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
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

	user1ID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	user2ID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNXYZ")

	t.Run("global admin caller can manage any user", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+user2ID.String()+"/keys", nil)
		req.SetPathValue("user_ulid", user2ID.String())

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: user1ID, IsAdmin: true})
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.GlobalAdmin{})

		protected.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("regular session user can manage own keys", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+user1ID.String()+"/keys", nil)
		req.SetPathValue("user_ulid", user1ID.String())

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
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+user2ID.String()+"/keys", nil)
		req.SetPathValue("user_ulid", user2ID.String())

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

	t.Run("invalid user_ulid returns 403", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/user/not-a-ulid/keys", nil)
		req.SetPathValue("user_ulid", "not-a-ulid")

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
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+user1ID.String()+"/keys", nil)
		req.SetPathValue("user_ulid", user1ID.String())

		apiKey := &repo.APIKey{
			ID:        ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQ1"),
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
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+user1ID.String()+"/keys", nil)
		req.SetPathValue("user_ulid", user1ID.String())

		apiKey := &repo.APIKey{
			ID:        ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQ2"),
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
		req := httptest.NewRequest(http.MethodGet, "/api/user/"+user1ID.String()+"/keys", nil)
		req.SetPathValue("user_ulid", user1ID.String())

		apiKey := &repo.APIKey{
			ID:        ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQ3"),
			UserID:    user1ID,
			Scope:     repo.AccessView | repo.AccessCreate, // Admin user's key, but no AccessAdmin
			CreatedAt: time.Now(),
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: user1ID, IsAdmin: true})
		ctx = context.WithValue(ctx, utils.APIKeyKey, apiKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.APIKeyOfAdmin{
			Scope: apiKey.Scope,
		})

		protected.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rec.Code)
		}
	})
}

func TestAuthMiddleware_AdminAPIKeyScopeEnforcement(t *testing.T) {
	adminUser := repo.User{
		ID:       ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Username: "admin",
		IsAdmin:  true,
	}
	adminOnlyKey := repo.APIKey{
		ID:        ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQS"),
		UserID:    adminUser.ID,
		Scope:     repo.AccessAdmin,
		CreatedAt: time.Now().Add(-1 * time.Hour),
	}
	mockRepo := &mockAuthRepo{
		user:   adminUser,
		apiKey: adminOnlyKey,
	}

	am := auth.NewAuthMiddleware(mockRepo, "test_jwt_secret_key_123456789012")
	defer am.Close()

	dbID := "01HGFB9Z5W7ABCDEFGHJKMNPQT"

	tests := []struct {
		name       string
		perm       repo.AccessGrant
		wantStatus int
	}{
		{"denies AccessView", repo.AccessView, http.StatusForbidden},
		{"denies AccessDelete", repo.AccessDelete, http.StatusForbidden},
		{"allows AccessAdmin", repo.AccessAdmin, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nextCalled := false
			h := am.AuthMiddleware(am.RequireDatabasePermission(tc.perm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})))

			req := httptest.NewRequest(http.MethodGet, "/api/database/"+dbID, nil)
			req.SetPathValue("database_id", dbID)
			req.Header.Set("Authorization", "Bearer srv_adminonlykey123")
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			wantNext := tc.wantStatus == http.StatusOK
			if nextCalled != wantNext {
				t.Fatalf("nextCalled = %v, want %v", nextCalled, wantNext)
			}
		})
	}

	t.Run("uses APIKeyOfAdmin holder and passes RequireGlobalAdmin", func(t *testing.T) {
		nextCalled := false
		var gotHolder utils.PermissionHolder
		h := am.AuthMiddleware(am.RequireGlobalAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			gotHolder = utils.GetPermissionHolderFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})))

		req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		req.Header.Set("Authorization", "Bearer srv_adminonlykey123")
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if !nextCalled {
			t.Fatal("expected next handler to be called")
		}
		apiKeyHolder, ok := gotHolder.(*utils.APIKeyOfAdmin)
		if !ok {
			t.Fatalf("expected *utils.APIKeyOfAdmin holder, got %T", gotHolder)
		}
		if !apiKeyHolder.IsGlobalAdmin() {
			t.Fatal("expected APIKeyOfAdmin with AccessAdmin to return IsGlobalAdmin() == true")
		}
	})
}

func TestAuthMiddleware_DBErrorsReturn500(t *testing.T) {
	secret := "test_jwt_secret_key_123456789012"
	dbErr := errors.New("database connection refused")

	t.Run("JWT GetUserByID DB error returns 500", func(t *testing.T) {
		mockRepo := &mockAuthRepo{getUserErr: dbErr}
		am := auth.NewAuthMiddleware(mockRepo, secret)
		defer am.Close()

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "01HGFB9Z5W7ABCDEFGHJKMNPQR",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		tokenStr, _ := token.SignedString([]byte(secret))

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		rec := httptest.NewRecorder()

		am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 on DB error, got %d: %s", rec.Code, rec.Body.String())
		}
		var errResp utils.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if errResp.Error != "Authentication service temporarily unavailable" {
			t.Fatalf("unexpected error message: %q", errResp.Error)
		}
	})

	t.Run("Basic Auth GetUserByUsername DB error returns 500", func(t *testing.T) {
		mockRepo := &mockAuthRepo{getUserErr: dbErr}
		am := auth.NewAuthMiddleware(mockRepo, secret)
		defer am.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("alice:pass")))
		rec := httptest.NewRecorder()

		am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 on DB error, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("API key DB error returns 500 and ErrNotFound returns 401", func(t *testing.T) {
		mockRepo := &mockAuthRepo{getAPIKeyErr: dbErr}
		am := auth.NewAuthMiddleware(mockRepo, secret)
		defer am.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Bearer srv_somekey123")
		rec := httptest.NewRecorder()

		am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 on DB error, got %d", rec.Code)
		}

		mockRepo.getAPIKeyErr = customerrors.ErrNotFound
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req2.Header.Set("Authorization", "Bearer srv_somekey123")
		am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 on ErrNotFound, got %d", rec2.Code)
		}
	})

	t.Run("unsupported auth scheme returns 401", func(t *testing.T) {
		am := auth.NewAuthMiddleware(&mockAuthRepo{}, secret)
		defer am.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("Authorization", "Digest foo")
		rec := httptest.NewRecorder()

		am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 on unsupported scheme, got %d", rec.Code)
		}
	})
}

func TestRequireDatabasePermission_DBErrorReturns500(t *testing.T) {
	dbID := "01HGFB9Z5W7ABCDEFGHJKMNPQR"
	mockRepo := &mockAuthRepo{
		user: repo.User{
			ID:       ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQS"),
			Username: "regular",
			IsAdmin:  false,
		},
		getPermsErr: errors.New("database timeout"),
	}
	am := auth.NewAuthMiddleware(mockRepo, "test_jwt_secret_key_123456789012")
	defer am.Close()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": mockRepo.user.ID.String(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, _ := token.SignedString([]byte("test_jwt_secret_key_123456789012"))

	h := am.AuthMiddleware(am.RequireDatabasePermission(repo.AccessView)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/database/"+dbID, nil)
	req.SetPathValue("database_id", dbID)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when permission lookup fails, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddleware_ZeroCreatedAtAPIKeyStillTreatedAsAPIKey(t *testing.T) {
	adminUser := repo.User{
		ID:       ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Username: "admin",
		IsAdmin:  true,
	}
	keyID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQS")
	mockRepo := &mockAuthRepo{
		user: adminUser,
		apiKey: repo.APIKey{
			ID:     keyID,
			UserID: adminUser.ID,
			Scope:  repo.AccessView, // No AccessAdmin
			// CreatedAt intentionally zero!
		},
	}

	am := auth.NewAuthMiddleware(mockRepo, "test_jwt_secret_key_123456789012")

	var gotAPIKey bool
	var gotIsGlobalAdmin bool
	h := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotAPIKey = utils.GetAPIKeyFromContext(r.Context())
		gotIsGlobalAdmin = utils.GetPermissionHolderFromContext(r.Context()).IsGlobalAdmin()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer srv_zerocreatedatkey")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	am.Close()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !gotAPIKey {
		t.Fatal("expected APIKeyKey in context even when CreatedAt is zero")
	}
	if gotIsGlobalAdmin {
		t.Fatal("expected admin's non-admin-scoped API key not to be treated as GlobalAdmin even when CreatedAt is zero")
	}
	if mockRepo.updateAPIKeyCount != 1 || mockRepo.updatedAPIKeyID != keyID {
		t.Fatalf("expected API key last-used update for %s, got count=%d id=%s", keyID, mockRepo.updateAPIKeyCount, mockRepo.updatedAPIKeyID)
	}
}
