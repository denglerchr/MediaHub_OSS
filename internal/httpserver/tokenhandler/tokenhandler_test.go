package tokenhandler_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mediahub_oss/internal/httpserver/tokenhandler"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/logging/audit"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"

	"golang.org/x/crypto/bcrypt"
)

type mockTokenRepo struct {
	repo.Repository
	user          repo.User
	refreshTokens map[string]repo.ULID
}

func (m *mockTokenRepo) GetUserByUsername(ctx context.Context, username string) (repo.User, error) {
	if m.user.Username == username {
		return m.user, nil
	}
	return repo.User{}, customerrors.ErrNotFound
}

func (m *mockTokenRepo) StoreRefreshToken(ctx context.Context, userID repo.ULID, tokenHash string, duration time.Duration) error {
	if m.refreshTokens == nil {
		m.refreshTokens = make(map[string]repo.ULID)
	}
	m.refreshTokens[tokenHash] = userID
	return nil
}

func (m *mockTokenRepo) ConsumeRefreshToken(ctx context.Context, tokenHash string) (repo.ULID, error) {
	if m.refreshTokens == nil {
		return repo.ULID{}, customerrors.ErrNotFound
	}
	userID, ok := m.refreshTokens[tokenHash]
	if !ok {
		return repo.ULID{}, customerrors.ErrNotFound
	}
	delete(m.refreshTokens, tokenHash)
	return userID, nil
}

func (m *mockTokenRepo) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	if m.refreshTokens != nil {
		delete(m.refreshTokens, tokenHash)
	}
	return nil
}

func TestGetToken_ServiceAccountBasicAuthRejected(t *testing.T) {
	password := "service_secret_pass"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	mockRepo := &mockTokenRepo{
		user: repo.User{
			ID:           ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
			Username:     "service_worker",
			PasswordHash: string(hash),
			AccountType:  repo.AccountTypeService,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)

	handler := tokenhandler.TokenHandler{
		Logger:          logger,
		Auditor:         auditor,
		Repo:            mockRepo,
		JWTSecret:       []byte("test_secret_12345678901234567890"),
		AccessDuration:  5 * time.Minute,
		RefreshDuration: 24 * time.Hour,
	}

	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("service_worker:"+password))
	req := httptest.NewRequest(http.MethodPost, "/api/token", nil)
	req.Header.Set("Authorization", authHeader)
	rec := httptest.NewRecorder()

	handler.GetToken(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 Unauthorized for service account basic auth, got %d", rec.Code)
	}
}

func TestGetToken_RegularUserBasicAuthAccepted(t *testing.T) {
	password := "user_secret_pass"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	mockRepo := &mockTokenRepo{
		user: repo.User{
			ID:           ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
			Username:     "regular_user",
			PasswordHash: string(hash),
			AccountType:  repo.AccountTypeLocal,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)

	handler := tokenhandler.TokenHandler{
		Logger:          logger,
		Auditor:         auditor,
		Repo:            mockRepo,
		JWTSecret:       []byte("test_secret_12345678901234567890"),
		AccessDuration:  5 * time.Minute,
		RefreshDuration: 24 * time.Hour,
	}

	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("regular_user:"+password))
	req := httptest.NewRequest(http.MethodPost, "/api/token", nil)
	req.Header.Set("Authorization", authHeader)
	rec := httptest.NewRecorder()

	handler.GetToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK for regular user basic auth, got %d", rec.Code)
	}
}

type mockOIDCProvider struct {
	claims *tokenhandler.OIDCClaims
	err    error
}

func (m *mockOIDCProvider) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*tokenhandler.OIDCClaims, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.claims, nil
}

func (m *mockOIDCProvider) GetAuthEndpoint(ctx context.Context) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return "https://test-idp.example.com/oauth/authorize", nil
}

type mockOIDCTokenRepo struct {
	repo.Repository
	users           map[string]repo.User
	identities      map[string]repo.User // key: issuer+":"+subject
	createErr       error
	createRaceUser  *repo.User
	clonedFrom      string
	clonedTo        repo.ULID
}

func newMockOIDCTokenRepo() *mockOIDCTokenRepo {
	return &mockOIDCTokenRepo{
		users:      make(map[string]repo.User),
		identities: make(map[string]repo.User),
	}
}

func (m *mockOIDCTokenRepo) GetUserByOIDCIdentity(ctx context.Context, issuer, subject string) (repo.User, error) {
	key := issuer + ":" + subject
	if u, ok := m.identities[key]; ok {
		return u, nil
	}
	return repo.User{}, customerrors.ErrNotFound
}

func (m *mockOIDCTokenRepo) CreateOIDCUser(ctx context.Context, params repo.CreateOIDCUserParams) (repo.User, error) {
	if m.createErr != nil {
		if m.createRaceUser != nil {
			key := params.Issuer + ":" + params.Subject
			m.identities[key] = *m.createRaceUser
			m.users[m.createRaceUser.Username] = *m.createRaceUser
		}
		return repo.User{}, m.createErr
	}
	baseUsername := repo.ExtractUsernameBase(params.PreferredUsername, params.Email)
	var colliding []string
	for name := range m.users {
		if name == baseUsername || strings.HasPrefix(name, baseUsername+"_") {
			colliding = append(colliding, name)
		}
	}
	uniqueUsername := repo.ResolveUniqueUsername(baseUsername, colliding)

	u := repo.User{
		ID:          ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQQ"),
		Username:    uniqueUsername,
		AccountType: repo.AccountTypeOIDC,
	}
	m.users[u.Username] = u
	key := params.Issuer + ":" + params.Subject
	m.identities[key] = u
	m.clonedFrom = params.DefaultRightsUser
	m.clonedTo = u.ID
	return u, nil
}

func (m *mockOIDCTokenRepo) StoreRefreshToken(ctx context.Context, userID repo.ULID, tokenHash string, duration time.Duration) error {
	return nil
}

func TestGetTokenOIDC_Disabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, nil)
	handler := tokenhandler.TokenHandler{
		Logger:     logger,
		Auditor:    auditor,
		OIDCConfig: tokenhandler.OIDCConfig{Enabled: false},
	}

	body := strings.NewReader(`{"code":"auth_code_123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.GetTokenOIDC(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request when OIDC is disabled, got %d", rec.Code)
	}
}

func TestGetTokenOIDC_ProviderNotConfigured(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, nil)
	handler := tokenhandler.TokenHandler{
		Logger:       logger,
		Auditor:      auditor,
		OIDCConfig:   tokenhandler.OIDCConfig{Enabled: true},
		OIDCProvider: nil,
	}

	body := strings.NewReader(`{"code":"auth_code_123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.GetTokenOIDC(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 Internal Server Error when OIDCProvider is nil, got %d", rec.Code)
	}
}

func TestGetTokenOIDC_ExistingUser(t *testing.T) {
	repoMock := newMockOIDCTokenRepo()
	existingUser := repo.User{
		ID:          ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
		Username:    "existing_oidc_user",
		AccountType: repo.AccountTypeOIDC,
	}
	repoMock.users[existingUser.Username] = existingUser
	repoMock.identities["http://localhost:8081/realms/mediahub:sub-123"] = existingUser

	providerMock := &mockOIDCProvider{
		claims: &tokenhandler.OIDCClaims{
			Issuer:            "http://localhost:8081/realms/mediahub",
			Subject:           "sub-123",
			PreferredUsername: "existing_oidc_user",
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, repoMock)
	handler := tokenhandler.TokenHandler{
		Logger:          logger,
		Auditor:         auditor,
		Repo:            repoMock,
		JWTSecret:       []byte("test_secret_12345678901234567890"),
		AccessDuration:  5 * time.Minute,
		RefreshDuration: 24 * time.Hour,
		OIDCConfig: tokenhandler.OIDCConfig{
			Enabled:   true,
			IssuerURL: "http://localhost:8081/realms/mediahub",
		},
		OIDCProvider: providerMock,
	}

	body := strings.NewReader(`{"code":"valid_code"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.GetTokenOIDC(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for existing OIDC user, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetTokenOIDC_JITProvisioning_CollisionResolution(t *testing.T) {
	repoMock := newMockOIDCTokenRepo()
	// Simulate existing user "john.doe"
	repoMock.users["john.doe"] = repo.User{
		ID:          ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMN001"),
		Username:    "john.doe",
		AccountType: repo.AccountTypeLocal,
	}

	providerMock := &mockOIDCProvider{
		claims: &tokenhandler.OIDCClaims{
			Issuer:            "http://localhost:8081/realms/mediahub",
			Subject:           "sub-456",
			PreferredUsername: "john.doe",
			Email:             "john.doe@example.com",
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, repoMock)
	handler := tokenhandler.TokenHandler{
		Logger:          logger,
		Auditor:         auditor,
		Repo:            repoMock,
		JWTSecret:       []byte("test_secret_12345678901234567890"),
		AccessDuration:  5 * time.Minute,
		RefreshDuration: 24 * time.Hour,
		OIDCConfig: tokenhandler.OIDCConfig{
			Enabled:           true,
			IssuerURL:         "http://localhost:8081/realms/mediahub",
			DefaultUserRights: "_oidc_user",
		},
		OIDCProvider: providerMock,
	}

	body := strings.NewReader(`{"code":"new_user_code"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.GetTokenOIDC(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	// Should resolve to "john.doe_2"
	if _, ok := repoMock.users["john.doe_2"]; !ok {
		t.Fatalf("expected user 'john.doe_2' to be provisioned, existing users: %v", repoMock.users)
	}

	// Should have linked identity
	key := "http://localhost:8081/realms/mediahub:sub-456"
	if _, ok := repoMock.identities[key]; !ok {
		t.Fatalf("expected identity %s to be linked", key)
	}

	// Should have cloned permissions from default_user_rights
	if repoMock.clonedFrom != "_oidc_user" {
		t.Fatalf("expected cloned permissions from '_oidc_user', got %s", repoMock.clonedFrom)
	}
}

func TestGetTokenOIDC_JITProvisioning_EmailFallback(t *testing.T) {
	repoMock := newMockOIDCTokenRepo()
	providerMock := &mockOIDCProvider{
		claims: &tokenhandler.OIDCClaims{
			Issuer:  "http://localhost:8081/realms/mediahub",
			Subject: "sub-789",
			Email:   "alice.wonderland@example.com",
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, repoMock)
	handler := tokenhandler.TokenHandler{
		Logger:          logger,
		Auditor:         auditor,
		Repo:            repoMock,
		JWTSecret:       []byte("test_secret_12345678901234567890"),
		AccessDuration:  5 * time.Minute,
		RefreshDuration: 24 * time.Hour,
		OIDCConfig: tokenhandler.OIDCConfig{
			Enabled:   true,
			IssuerURL: "http://localhost:8081/realms/mediahub",
		},
		OIDCProvider: providerMock,
	}

	body := strings.NewReader(`{"code":"alice_code"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.GetTokenOIDC(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, ok := repoMock.users["alice.wonderland"]; !ok {
		t.Fatalf("expected user 'alice.wonderland' to be provisioned from email, got %v", repoMock.users)
	}
}

func TestGetTokenOIDC_JITProvisioning_FullFallback(t *testing.T) {
	repoMock := newMockOIDCTokenRepo()
	providerMock := &mockOIDCProvider{
		claims: &tokenhandler.OIDCClaims{
			Issuer:  "http://localhost:8081/realms/mediahub",
			Subject: "sub-999",
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, repoMock)
	handler := tokenhandler.TokenHandler{
		Logger:          logger,
		Auditor:         auditor,
		Repo:            repoMock,
		JWTSecret:       []byte("test_secret_12345678901234567890"),
		AccessDuration:  5 * time.Minute,
		RefreshDuration: 24 * time.Hour,
		OIDCConfig: tokenhandler.OIDCConfig{
			Enabled:   true,
			IssuerURL: "http://localhost:8081/realms/mediahub",
		},
		OIDCProvider: providerMock,
	}

	body := strings.NewReader(`{"code":"fallback_code"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.GetTokenOIDC(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, ok := repoMock.users["user"]; !ok {
		t.Fatalf("expected user 'user' to be provisioned as full fallback, got %v", repoMock.users)
	}
}

func TestRefreshToken_AtomicRotationAndOversizedBody(t *testing.T) {
	password := "user_secret_pass"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	mockRepo := &mockTokenRepo{
		user: repo.User{
			ID:           ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
			Username:     "regular_user",
			PasswordHash: string(hash),
			AccountType:  repo.AccountTypeLocal,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, mockRepo)
	handler := tokenhandler.TokenHandler{
		Logger:          logger,
		Auditor:         auditor,
		Repo:            mockRepo,
		JWTSecret:       []byte("test_secret_12345678901234567890"),
		AccessDuration:  5 * time.Minute,
		RefreshDuration: 24 * time.Hour,
	}

	// Obtain initial token pair
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("regular_user:"+password))
	loginReq := httptest.NewRequest(http.MethodPost, "/api/token", nil)
	loginReq.Header.Set("Authorization", authHeader)
	loginRec := httptest.NewRecorder()
	handler.GetToken(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GetToken, got %d", loginRec.Code)
	}

	var loginResp tokenhandler.TokenResponse
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}

	// First refresh consumes old refresh token and returns new pair
	refreshBody, _ := json.Marshal(tokenhandler.TokenRequest{RefreshToken: loginResp.RefreshToken})
	req1 := httptest.NewRequest(http.MethodPost, "/api/token/refresh", bytes.NewReader(refreshBody))
	rec1 := httptest.NewRecorder()
	handler.RefreshToken(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on first refresh, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Replaying the same refresh token must fail with 401
	req2 := httptest.NewRequest(http.MethodPost, "/api/token/refresh", bytes.NewReader(refreshBody))
	rec2 := httptest.NewRecorder()
	handler.RefreshToken(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized on replayed refresh token, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Oversized JSON body returns 413 on RefreshToken and Logout
	oversized := bytes.Repeat([]byte("a"), int(utils.MaxJSONBodyBytes)+16)
	bigPayload := append(append([]byte(`{"refresh_token":"`), oversized...), []byte(`"}`)...)

	bigRefreshReq := httptest.NewRequest(http.MethodPost, "/api/token/refresh", bytes.NewReader(bigPayload))
	bigRefreshRec := httptest.NewRecorder()
	handler.RefreshToken(bigRefreshRec, bigRefreshReq)
	if bigRefreshRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 on oversized RefreshToken body, got %d", bigRefreshRec.Code)
	}

	bigLogoutReq := httptest.NewRequest(http.MethodPost, "/api/logout", bytes.NewReader(bigPayload))
	bigLogoutReq = bigLogoutReq.WithContext(context.WithValue(bigLogoutReq.Context(), utils.UserKey, &mockRepo.user))
	bigLogoutRec := httptest.NewRecorder()
	handler.Logout(bigLogoutRec, bigLogoutReq)
	if bigLogoutRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 on oversized Logout body, got %d", bigLogoutRec.Code)
	}
}

func TestGetTokenOIDC_GenericErrorAndNonceAndConflictFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("provider error returns generic 401 without reflecting internal details", func(t *testing.T) {
		repoMock := newMockOIDCTokenRepo()
		auditor := audit.NewAuditLogger(false, "stdio", logger, repoMock)
		handler := tokenhandler.TokenHandler{
			Logger:  logger,
			Auditor: auditor,
			Repo:    repoMock,
			OIDCConfig: tokenhandler.OIDCConfig{
				Enabled:   true,
				IssuerURL: "http://localhost:8081/realms/mediahub",
			},
			OIDCProvider: &mockOIDCProvider{
				err: errors.New("sensitive internal idp secret leak xyz"),
			},
		}

		req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", strings.NewReader(`{"code":"bad"}`))
		rec := httptest.NewRecorder()
		handler.GetTokenOIDC(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		var errResp utils.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if errResp.Error != "OIDC authentication failed" {
			t.Fatalf("expected generic error 'OIDC authentication failed', got %q", errResp.Error)
		}
		if strings.Contains(rec.Body.String(), "sensitive internal") {
			t.Fatalf("response leaked raw internal error: %s", rec.Body.String())
		}
	})

	t.Run("nonce mismatch returns 401 and matching nonce succeeds", func(t *testing.T) {
		repoMock := newMockOIDCTokenRepo()
		existingUser := repo.User{
			ID:          ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
			Username:    "nonce_user",
			AccountType: repo.AccountTypeOIDC,
		}
		repoMock.identities["http://localhost:8081/realms/mediahub:sub-nonce"] = existingUser
		auditor := audit.NewAuditLogger(false, "stdio", logger, repoMock)

		handler := tokenhandler.TokenHandler{
			Logger:          logger,
			Auditor:         auditor,
			Repo:            repoMock,
			JWTSecret:       []byte("test_secret_12345678901234567890"),
			AccessDuration:  5 * time.Minute,
			RefreshDuration: 24 * time.Hour,
			OIDCConfig: tokenhandler.OIDCConfig{
				Enabled:   true,
				IssuerURL: "http://localhost:8081/realms/mediahub",
			},
			OIDCProvider: &mockOIDCProvider{
				claims: &tokenhandler.OIDCClaims{
					Issuer:  "http://localhost:8081/realms/mediahub",
					Subject: "sub-nonce",
					Nonce:   "actual-nonce",
				},
			},
		}

		reqBad := httptest.NewRequest(http.MethodPost, "/api/token/oidc", strings.NewReader(`{"code":"ok","nonce":"different-nonce"}`))
		recBad := httptest.NewRecorder()
		handler.GetTokenOIDC(recBad, reqBad)
		if recBad.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 on nonce mismatch, got %d", recBad.Code)
		}

		reqGood := httptest.NewRequest(http.MethodPost, "/api/token/oidc", strings.NewReader(`{"code":"ok","nonce":"actual-nonce"}`))
		recGood := httptest.NewRecorder()
		handler.GetTokenOIDC(recGood, reqGood)
		if recGood.Code != http.StatusOK {
			t.Fatalf("expected 200 on matching nonce, got %d: %s", recGood.Code, recGood.Body.String())
		}
	})

	t.Run("multi-pod JIT conflict falls back to GetUserByOIDCIdentity", func(t *testing.T) {
		for _, conflictErr := range []error{customerrors.ErrConflict, customerrors.ErrUserExists} {
			repoMock := newMockOIDCTokenRepo()
			raceWinner := repo.User{
				ID:          ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQ9"),
				Username:    "raced_user",
				AccountType: repo.AccountTypeOIDC,
			}
			repoMock.createErr = conflictErr
			repoMock.createRaceUser = &raceWinner

			auditor := audit.NewAuditLogger(false, "stdio", logger, repoMock)
			handler := tokenhandler.TokenHandler{
				Logger:          logger,
				Auditor:         auditor,
				Repo:            repoMock,
				JWTSecret:       []byte("test_secret_12345678901234567890"),
				AccessDuration:  5 * time.Minute,
				RefreshDuration: 24 * time.Hour,
				OIDCConfig: tokenhandler.OIDCConfig{
					Enabled:   true,
					IssuerURL: "http://localhost:8081/realms/mediahub",
				},
				OIDCProvider: &mockOIDCProvider{
					claims: &tokenhandler.OIDCClaims{
						Issuer:  "http://localhost:8081/realms/mediahub",
						Subject: "sub-race",
					},
				},
			}

			req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", strings.NewReader(`{"code":"race"}`))
			rec := httptest.NewRecorder()
			handler.GetTokenOIDC(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 after JIT %v fallback, got %d: %s", conflictErr, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("oversized OIDC JSON body returns 413", func(t *testing.T) {
		repoMock := newMockOIDCTokenRepo()
		auditor := audit.NewAuditLogger(false, "stdio", logger, repoMock)
		handler := tokenhandler.TokenHandler{
			Logger:       logger,
			Auditor:      auditor,
			Repo:         repoMock,
			OIDCConfig:   tokenhandler.OIDCConfig{Enabled: true},
			OIDCProvider: &mockOIDCProvider{},
		}
		oversized := bytes.Repeat([]byte("x"), int(utils.MaxJSONBodyBytes)+16)
		bigPayload := append(append([]byte(`{"code":"`), oversized...), []byte(`"}`)...)
		req := httptest.NewRequest(http.MethodPost, "/api/token/oidc", bytes.NewReader(bigPayload))
		rec := httptest.NewRecorder()
		handler.GetTokenOIDC(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 on oversized OIDC request, got %d", rec.Code)
		}
	})
}

