package userhandler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"mediahub_oss/internal/httpserver/userhandler"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/logging/audit"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

type mockUserRepo struct {
	repo.Repository
	users     map[repo.ULID]repo.User
	userSlice []repo.User
	permsErr  map[repo.ULID]error
	apiKeys   map[repo.ULID]repo.APIKey
}

func (m *mockUserRepo) GetUserByID(ctx context.Context, id repo.ULID) (repo.User, error) {
	if u, ok := m.users[id]; ok {
		return u, nil
	}
	return repo.User{}, customerrors.ErrNotFound
}

func (m *mockUserRepo) CreateUser(ctx context.Context, u repo.User) (repo.User, error) {
	u.ID = "01HGFB9Z5W7ABCDEFGHJKMNPQR"
	if m.users == nil {
		m.users = make(map[repo.ULID]repo.User)
	}
	m.users[u.ID] = u
	return u, nil
}

func (m *mockUserRepo) UpdateUser(ctx context.Context, u repo.User) (repo.User, error) {
	if _, ok := m.users[u.ID]; !ok {
		return repo.User{}, customerrors.ErrNotFound
	}
	for id, existing := range m.users {
		if id != u.ID && existing.Username == u.Username {
			return repo.User{}, customerrors.ErrUserExists
		}
	}
	m.users[u.ID] = u
	return u, nil
}

func (m *mockUserRepo) SetUserPermissions(ctx context.Context, p repo.UserPermissions) error {
	return nil
}

func (m *mockUserRepo) GetUsers(ctx context.Context, accountType *repo.AccountType) ([]repo.User, error) {
	return m.userSlice, nil
}

func (m *mockUserRepo) GetAllUserPermissions(ctx context.Context, userID repo.ULID) ([]repo.UserPermissions, error) {
	if m.permsErr != nil {
		if err, ok := m.permsErr[userID]; ok {
			return nil, err
		}
	}
	return nil, nil
}

func (m *mockUserRepo) CreateAPIKey(ctx context.Context, key repo.APIKey) (repo.APIKey, error) {
	if m.apiKeys == nil {
		m.apiKeys = make(map[repo.ULID]repo.APIKey)
	}
	m.apiKeys[key.ID] = key
	return key, nil
}

func (m *mockUserRepo) GetAPIKeyByID(ctx context.Context, id repo.ULID) (repo.APIKey, error) {
	if m.apiKeys != nil {
		if k, ok := m.apiKeys[id]; ok {
			return k, nil
		}
	}
	return repo.APIKey{}, customerrors.ErrNotFound
}

func (m *mockUserRepo) UpdateAPIKey(ctx context.Context, key repo.APIKey) (repo.APIKey, error) {
	if m.apiKeys != nil {
		m.apiKeys[key.ID] = key
	}
	return key, nil
}

func (m *mockUserRepo) DeleteAPIKey(ctx context.Context, id repo.ULID) error {
	if m.apiKeys != nil {
		delete(m.apiKeys, id)
	}
	return nil
}

func TestCreateUser_DuplicateDatabasePermissionsRejected(t *testing.T) {
	mockRepo := &mockUserRepo{
		users: make(map[repo.ULID]repo.User),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{
		Logger:  logger,
		Auditor: auditor,
		Repo:    mockRepo,
	}

	payload := userhandler.CreateUserPayload{
		Username: "newuser",
		Password: "password123",
		Permissions: []userhandler.DatabasePermission{
			{DatabaseID: "01HGFB9Z5W7ABCDEFGHJKMNPQR", CanView: true},
			{DatabaseID: "01HGFB9Z5W7ABCDEFGHJKMNPQR", CanEdit: true}, // duplicate
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/user", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	handler.CreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for duplicate DB permissions, got %d", rec.Code)
	}
}

func TestUpdateUser_DuplicateDatabasePermissionsRejected(t *testing.T) {
	targetULID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	mockRepo := &mockUserRepo{
		users: map[repo.ULID]repo.User{
			targetULID: {
				ID:           targetULID,
				Username:     "existinguser",
				PasswordHash: "$2a$10$abcdef",
			},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{
		Logger:  logger,
		Auditor: auditor,
		Repo:    mockRepo,
	}

	payload := userhandler.UpdateUserPayload{
		Permissions: []userhandler.DatabasePermission{
			{DatabaseID: "01HGFB9Z5W7ABCDEFGHJKMNPQR", CanView: true},
			{DatabaseID: "01HGFB9Z5W7ABCDEFGHJKMNPQR", CanEdit: true}, // duplicate
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/api/user/"+string(targetULID), bytes.NewReader(body))
	req.SetPathValue("user_ulid", string(targetULID))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	handler.UpdateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for duplicate DB permissions in UpdateUser, got %d", rec.Code)
	}
}

func TestCreateUser_OIDCDirectCreationRejected(t *testing.T) {
	mockRepo := &mockUserRepo{users: make(map[repo.ULID]repo.User)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	payload := userhandler.CreateUserPayload{
		Username:    "oidc_user",
		AccountType: repo.AccountTypeOIDC,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/user", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	handler.CreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request when directly creating OIDC user, got %d", rec.Code)
	}
}

func TestUpdateUser_OIDCPasswordChangeRejected(t *testing.T) {
	targetULID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	mockRepo := &mockUserRepo{
		users: map[repo.ULID]repo.User{
			targetULID: {
				ID:          targetULID,
				Username:    "oidc_user",
				AccountType: repo.AccountTypeOIDC,
			},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	payload := userhandler.UpdateUserPayload{
		Password: "new_password_attempt",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/api/user/"+string(targetULID), bytes.NewReader(body))
	req.SetPathValue("user_ulid", string(targetULID))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	handler.UpdateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request when changing password for OIDC user, got %d", rec.Code)
	}
}

func TestUpdateUser_ServiceAccountPasswordChangeRejected(t *testing.T) {
	targetULID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	mockRepo := &mockUserRepo{
		users: map[repo.ULID]repo.User{
			targetULID: {
				ID:          targetULID,
				Username:    "svc_account",
				AccountType: repo.AccountTypeService,
			},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	payload := userhandler.UpdateUserPayload{
		Password: "new_secret_password",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/api/user/"+string(targetULID), bytes.NewReader(body))
	req.SetPathValue("user_ulid", string(targetULID))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	handler.UpdateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request when changing password for service account, got %d", rec.Code)
	}
}

func TestUpdateMe_OIDCPasswordChangeRejected(t *testing.T) {
	targetULID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	mockRepo := &mockUserRepo{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	body := bytes.NewReader([]byte(`{"old_password":"old","new_password":"new"}`))
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{
		ID:          targetULID,
		Username:    "oidc_user",
		AccountType: repo.AccountTypeOIDC,
	}))
	rec := httptest.NewRecorder()

	handler.UpdateMe(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request when OIDC user updates own password, got %d", rec.Code)
	}
}

func TestUpdateMe_ServiceAccountPasswordChangeRejected(t *testing.T) {
	targetULID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	mockRepo := &mockUserRepo{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	body := bytes.NewReader([]byte(`{"old_password":"old","new_password":"new"}`))
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{
		ID:          targetULID,
		Username:    "svc_user",
		AccountType: repo.AccountTypeService,
	}))
	rec := httptest.NewRecorder()

	handler.UpdateMe(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request when service account updates own password, got %d", rec.Code)
	}
}

func TestCreateUser_DefaultAccountTypeLocal(t *testing.T) {
	mockRepo := &mockUserRepo{users: make(map[repo.ULID]repo.User)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	body := bytes.NewReader([]byte(`{"username":"localuser","password":"password123"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/user", body)
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	handler.CreateUser(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created when account_type is omitted, got %d: %s", rec.Code, rec.Body.String())
	}

	var created repo.User
	for _, u := range mockRepo.users {
		created = u
		break
	}
	if created.AccountType != repo.AccountTypeLocal {
		t.Fatalf("expected AccountTypeLocal (0), got %v", created.AccountType)
	}
}

func TestGetUsers_SkipsUserOnPermissionsErrorWithoutZeroValueEntry(t *testing.T) {
	u1 := repo.User{ID: "01HGFB9Z5W7ABCDEFGHJKMNPQ1", Username: "alice", IsAdmin: false, AccountType: repo.AccountTypeLocal}
	u2 := repo.User{ID: "01HGFB9Z5W7ABCDEFGHJKMNPQ2", Username: "broken", IsAdmin: false, AccountType: repo.AccountTypeLocal}
	u3 := repo.User{ID: "01HGFB9Z5W7ABCDEFGHJKMNPQ3", Username: "bob", IsAdmin: true, AccountType: repo.AccountTypeLocal}

	mockRepo := &mockUserRepo{
		userSlice: []repo.User{u1, u2, u3},
		permsErr: map[repo.ULID]error{
			u2.ID: errors.New("db error"),
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	req := httptest.NewRequest(http.MethodGet, "/api/user", nil)
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	handler.GetUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var users []userhandler.UserResponse
	if err := json.NewDecoder(rec.Body).Decode(&users); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(users) != 2 {
		t.Fatalf("expected 2 users in response, got %d: %+v", len(users), users)
	}
	for i, u := range users {
		if u.ID == "" || u.Username == "" {
			t.Errorf("unexpected zero-value user at index %d: %+v", i, u)
		}
	}
	if users[0].Username != "alice" || users[1].Username != "bob" {
		t.Errorf("expected [alice, bob], got [%s, %s]", users[0].Username, users[1].Username)
	}
}

func TestUpdateUser_DuplicateUsernameReturnsConflict(t *testing.T) {
	u1ID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQ1")
	u2ID := repo.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQ2")
	mockRepo := &mockUserRepo{
		users: map[repo.ULID]repo.User{
			u1ID: {ID: u1ID, Username: "alice", PasswordHash: "hash1"},
			u2ID: {ID: u2ID, Username: "bob", PasswordHash: "hash2"},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	payload := userhandler.UpdateUserPayload{
		Username: "alice",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/api/user/"+string(u2ID), bytes.NewReader(body))
	req.SetPathValue("user_ulid", string(u2ID))
	req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repo.User{Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()

	handler.UpdateUser(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409 Conflict for duplicate username in UpdateUser, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKey_SecurityConstraints(t *testing.T) {
	adminID := repo.ULID("01ADMIN00000000000000000000")
	userID := repo.ULID("01USER000000000000000000000")
	existingKeyID := repo.ULID("01KEY0000000000000000000000")

	mockRepo := &mockUserRepo{
		users: map[repo.ULID]repo.User{
			adminID: {ID: adminID, Username: "admin", IsAdmin: true},
			userID:  {ID: userID, Username: "user", IsAdmin: false},
		},
		apiKeys: map[repo.ULID]repo.APIKey{
			existingKeyID: {
				ID:     existingKeyID,
				UserID: userID,
				Name:   "existing_key",
				Scope:  repo.AccessView,
			},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := userhandler.UserHandler{Logger: logger, Auditor: auditor, Repo: mockRepo}

	t.Run("non-admin user cannot grant ScopeAdmin on CreateAPIKey", func(t *testing.T) {
		payload := userhandler.CreateAPIKeyPayload{
			Name:       "admin-key-attempt",
			ScopeView:  true,
			ScopeAdmin: true,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/user/"+string(userID)+"/keys", bytes.NewReader(body))
		req.SetPathValue("user_ulid", string(userID))

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: userID, Username: "user", IsAdmin: false})
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: userID,
			Scope:    repo.NewAccessGrant(true, true, true, true, true),
		})

		rec := httptest.NewRecorder()
		handler.CreateAPIKey(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden when non-admin grants admin scope, got %d", rec.Code)
		}
	})

	t.Run("API key caller without AccessAdmin cannot CreateAPIKey", func(t *testing.T) {
		payload := userhandler.CreateAPIKeyPayload{
			Name:      "new-key",
			ScopeView: true,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/user/"+string(userID)+"/keys", bytes.NewReader(body))
		req.SetPathValue("user_ulid", string(userID))

		callerKey := &repo.APIKey{
			ID:     "01CALLERKEY",
			UserID: userID,
			Scope:  repo.AccessView | repo.AccessEdit, // No AccessAdmin
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: userID, Username: "user", IsAdmin: false})
		ctx = context.WithValue(ctx, utils.APIKeyKey, callerKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: userID,
			Scope:    callerKey.Scope,
		})

		rec := httptest.NewRecorder()
		handler.CreateAPIKey(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden when API key without admin scope creates key, got %d", rec.Code)
		}
	})

	t.Run("API key caller cannot grant scopes exceeding its own scope", func(t *testing.T) {
		// Key has AccessAdmin and AccessView, but NOT AccessDelete
		payload := userhandler.CreateAPIKeyPayload{
			Name:        "escalated-key",
			ScopeView:   true,
			ScopeDelete: true, // Not possessed by caller!
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/user/"+string(userID)+"/keys", bytes.NewReader(body))
		req.SetPathValue("user_ulid", string(userID))

		callerKey := &repo.APIKey{
			ID:     "01CALLERKEY2",
			UserID: userID,
			Scope:  repo.AccessAdmin | repo.AccessView,
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: userID, Username: "user", IsAdmin: false})
		ctx = context.WithValue(ctx, utils.APIKeyKey, callerKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: userID,
			Scope:    callerKey.Scope,
		})

		rec := httptest.NewRecorder()
		handler.CreateAPIKey(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden due to scope attenuation violation, got %d", rec.Code)
		}
	})

	t.Run("non-admin user cannot escalate existing key with ScopeAdmin on UpdateAPIKey", func(t *testing.T) {
		scopeAdmin := true
		payload := userhandler.UpdateAPIKeyPayload{
			ScopeAdmin: &scopeAdmin,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPatch, "/api/user/"+string(userID)+"/keys/"+string(existingKeyID), bytes.NewReader(body))
		req.SetPathValue("user_ulid", string(userID))
		req.SetPathValue("key_ulid", string(existingKeyID))

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: userID, Username: "user", IsAdmin: false})
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: userID,
			Scope:    repo.NewAccessGrant(true, true, true, true, true),
		})

		rec := httptest.NewRecorder()
		handler.UpdateAPIKey(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden when non-admin sets ScopeAdmin in UpdateAPIKey, got %d", rec.Code)
		}
	})

	t.Run("API key caller without AccessAdmin cannot UpdateAPIKey", func(t *testing.T) {
		name := "updated-name"
		payload := userhandler.UpdateAPIKeyPayload{
			Name: &name,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPatch, "/api/user/"+string(userID)+"/keys/"+string(existingKeyID), bytes.NewReader(body))
		req.SetPathValue("user_ulid", string(userID))
		req.SetPathValue("key_ulid", string(existingKeyID))

		callerKey := &repo.APIKey{
			ID:     "01CALLERKEY3",
			UserID: userID,
			Scope:  repo.AccessView,
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: userID, Username: "user", IsAdmin: false})
		ctx = context.WithValue(ctx, utils.APIKeyKey, callerKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: userID,
			Scope:    callerKey.Scope,
		})

		rec := httptest.NewRecorder()
		handler.UpdateAPIKey(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden when API key without admin scope updates key, got %d", rec.Code)
		}
	})

	t.Run("API key caller cannot grant scopes exceeding its own scope in UpdateAPIKey", func(t *testing.T) {
		scopeDelete := true
		payload := userhandler.UpdateAPIKeyPayload{
			ScopeDelete: &scopeDelete,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPatch, "/api/user/"+string(userID)+"/keys/"+string(existingKeyID), bytes.NewReader(body))
		req.SetPathValue("user_ulid", string(userID))
		req.SetPathValue("key_ulid", string(existingKeyID))

		callerKey := &repo.APIKey{
			ID:     "01CALLERKEY4",
			UserID: userID,
			Scope:  repo.AccessAdmin | repo.AccessView, // Does not have AccessDelete
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: userID, Username: "user", IsAdmin: false})
		ctx = context.WithValue(ctx, utils.APIKeyKey, callerKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: userID,
			Scope:    callerKey.Scope,
		})

		rec := httptest.NewRecorder()
		handler.UpdateAPIKey(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden due to scope attenuation in UpdateAPIKey, got %d", rec.Code)
		}
	})

	t.Run("API key caller without AccessAdmin cannot DeleteAPIKey", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/user/"+string(userID)+"/keys/"+string(existingKeyID), nil)
		req.SetPathValue("user_ulid", string(userID))
		req.SetPathValue("key_ulid", string(existingKeyID))

		callerKey := &repo.APIKey{
			ID:     "01CALLERKEY5",
			UserID: userID,
			Scope:  repo.AccessView | repo.AccessDelete, // No AccessAdmin
		}

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: userID, Username: "user", IsAdmin: false})
		ctx = context.WithValue(ctx, utils.APIKeyKey, callerKey)
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.UserPermissions{
			UserULID: userID,
			Scope:    callerKey.Scope,
		})

		rec := httptest.NewRecorder()
		handler.DeleteAPIKey(rec, req.WithContext(ctx))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden when API key without admin scope deletes key, got %d", rec.Code)
		}
	})

	t.Run("global admin can create key with ScopeAdmin", func(t *testing.T) {
		payload := userhandler.CreateAPIKeyPayload{
			Name:       "legit-admin-key",
			ScopeAdmin: true,
			ScopeView:  true,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/user/"+string(adminID)+"/keys", bytes.NewReader(body))
		req.SetPathValue("user_ulid", string(adminID))

		ctx := context.WithValue(req.Context(), utils.UserKey, &repo.User{ID: adminID, Username: "admin", IsAdmin: true})
		ctx = context.WithValue(ctx, utils.PermissionHolderKey, &utils.GlobalAdmin{UserULID: adminID})

		rec := httptest.NewRecorder()
		handler.CreateAPIKey(rec, req.WithContext(ctx))

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created when admin creates admin key, got %d: %s", rec.Code, rec.Body.String())
		}
	})

}
