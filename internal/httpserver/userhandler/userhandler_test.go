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
