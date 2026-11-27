package httpserver_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"mediahub_oss/internal/httpserver"
	"mediahub_oss/internal/httpserver/auth"
	"mediahub_oss/internal/httpserver/entryhandler"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage/localstorage"
)

type noopAuditLogger struct{}

func (n *noopAuditLogger) Log(_ context.Context, _, _, _ string, _ map[string]any) {}

func validRouterTestWebP() []byte {
	payload := []byte{0x2F, 0x0F, 0x00, 0x00, 0x00} // 16x1 VP8L
	var body bytes.Buffer
	body.WriteString("WEBP")
	body.WriteString("VP8L")
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], uint32(len(payload)))
	body.Write(sz[:])
	body.Write(payload)
	body.WriteByte(0) // pad odd payload

	var out bytes.Buffer
	out.WriteString("RIFF")
	var riffSize [4]byte
	binary.LittleEndian.PutUint32(riffSize[:], uint32(body.Len()))
	out.Write(riffSize[:])
	out.Write(body.Bytes())
	return out.Bytes()
}

func setupPreviewRouterTest(t *testing.T) (http.Handler, *sqlite.SQLiteRepository, *localstorage.LocalStorage, repo.Database, repo.Entry) {
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
		Name:        "rbac_preview_db",
		ContentType: "file",
	})
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	entry, err := r.CreateEntry(ctx, db, repo.Entry{
		Timestamp: time.Now(),
		MimeType:  "application/pdf",
		FileName:  "doc.pdf",
		Size:      12,
		Status:    repo.EntryStatusReady,
	})
	if err != nil {
		t.Fatalf("failed to create entry: %v", err)
	}

	am := auth.NewAuthMiddleware(r, "test-router-jwt-secret-1234567890")
	t.Cleanup(am.Close)

	handlers := &httpserver.Handlers{
		EntryHandler: entryhandler.EntryHandler{
			Repo:    r,
			Storage: store,
			Auditor: &noopAuditLogger{},
			Logger:  logger,
		},
	}

	router := httpserver.SetupRouter(handlers, nil, am, "/", nil)
	return router, r, store, db, entry
}

func createRouterTestUser(t *testing.T, r *sqlite.SQLiteRepository, username, password string, dbID repo.ULID, grant repo.AccessGrant) repo.User {
	t.Helper()
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt failed: %v", err)
	}
	u, err := r.CreateUser(ctx, repo.User{
		Username:     username,
		PasswordHash: string(hash),
		AccountType:  repo.AccountTypeLocal,
	})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if grant != 0 {
		if err := r.SetUserPermissions(ctx, repo.UserPermissions{
			UserID:     u.ID,
			DatabaseID: dbID,
			Roles:      grant,
		}); err != nil {
			t.Fatalf("SetUserPermissions failed: %v", err)
		}
	}
	return u
}

func createRouterTestAPIKey(t *testing.T, r *sqlite.SQLiteRepository, userID repo.ULID, secret string, scope repo.AccessGrant) string {
	t.Helper()
	sum := sha256.Sum256([]byte(secret))
	_, err := r.CreateAPIKey(context.Background(), repo.APIKey{
		ID:      ulid.New(),
		UserID:  userID,
		Name:    "test-key",
		KeyHash: hex.EncodeToString(sum[:]),
		KeyHint: secret[:4],
		Scope:   scope,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}
	return "srv_" + secret
}

func basicAuthHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func TestPreviewRoutes_RBACAndAPIKeyScope(t *testing.T) {
	router, r, _, db, entry := setupPreviewRouterTest(t)
	webp := validRouterTestWebP()
	previewURL := "/api/database/" + db.ID.String() + "/entry/1/preview"
	_ = entry

	doReq := func(method, authHeader string, body []byte) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req := httptest.NewRequest(method, previewURL, reader)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if body != nil {
			req.Header.Set("Content-Type", "image/webp")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	t.Run("unauthenticated requests return 401", func(t *testing.T) {
		for _, tc := range []struct {
			method string
			body   []byte
		}{
			{http.MethodGet, nil},
			{http.MethodPost, webp},
			{http.MethodPut, webp},
			{http.MethodDelete, nil},
		} {
			rec := doReq(tc.method, "", tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s unauthenticated: expected 401, got %d", tc.method, rec.Code)
			}
		}
	})

	t.Run("users lacking specific permissions receive 403", func(t *testing.T) {
		createRouterTestUser(t, r, "no_perms", "pass1234", db.ID, 0)
		createRouterTestUser(t, r, "view_only", "pass1234", db.ID, repo.AccessView)
		createRouterTestUser(t, r, "create_only", "pass1234", db.ID, repo.AccessCreate)
		createRouterTestUser(t, r, "edit_only", "pass1234", db.ID, repo.AccessEdit)
		createRouterTestUser(t, r, "delete_only", "pass1234", db.ID, repo.AccessDelete)

		if rec := doReq(http.MethodGet, basicAuthHeader("no_perms", "pass1234"), nil); rec.Code != http.StatusForbidden {
			t.Fatalf("GET without CanView: expected 403, got %d", rec.Code)
		}
		if rec := doReq(http.MethodPost, basicAuthHeader("view_only", "pass1234"), webp); rec.Code != http.StatusForbidden {
			t.Fatalf("POST without CanCreate: expected 403, got %d", rec.Code)
		}
		if rec := doReq(http.MethodPut, basicAuthHeader("create_only", "pass1234"), webp); rec.Code != http.StatusForbidden {
			t.Fatalf("PUT without CanEdit: expected 403, got %d", rec.Code)
		}
		if rec := doReq(http.MethodDelete, basicAuthHeader("edit_only", "pass1234"), nil); rec.Code != http.StatusForbidden {
			t.Fatalf("DELETE without CanDelete: expected 403, got %d", rec.Code)
		}
		if rec := doReq(http.MethodPut, basicAuthHeader("delete_only", "pass1234"), webp); rec.Code != http.StatusForbidden {
			t.Fatalf("PUT with only CanDelete: expected 403, got %d", rec.Code)
		}
	})

	t.Run("authorized users and API key scope attenuation", func(t *testing.T) {
		fullGrant := repo.NewAccessGrant(true, true, true, true, false)
		fullUser := createRouterTestUser(t, r, "full_user", "pass1234", db.ID, fullGrant)

		viewKey := createRouterTestAPIKey(t, r, fullUser.ID, "viewonlysecret01", repo.AccessView)
		createKey := createRouterTestAPIKey(t, r, fullUser.ID, "createonlysecret02", repo.AccessCreate)
		editDeleteKey := createRouterTestAPIKey(t, r, fullUser.ID, "editdelsecret03", repo.AccessEdit|repo.AccessDelete|repo.AccessView)

		// Attenuated view-only API key cannot POST, PUT, or DELETE
		if rec := doReq(http.MethodPost, "Bearer "+viewKey, webp); rec.Code != http.StatusForbidden {
			t.Fatalf("POST with view-only API key: expected 403, got %d", rec.Code)
		}
		if rec := doReq(http.MethodPut, "Bearer "+viewKey, webp); rec.Code != http.StatusForbidden {
			t.Fatalf("PUT with view-only API key: expected 403, got %d", rec.Code)
		}
		if rec := doReq(http.MethodDelete, "Bearer "+viewKey, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("DELETE with view-only API key: expected 403, got %d", rec.Code)
		}

		// Create API key creates preview (201), cannot PUT or DELETE
		if rec := doReq(http.MethodPost, "Bearer "+createKey, webp); rec.Code != http.StatusCreated {
			t.Fatalf("POST with create API key: expected 201, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec := doReq(http.MethodPut, "Bearer "+createKey, webp); rec.Code != http.StatusForbidden {
			t.Fatalf("PUT with create-only API key: expected 403, got %d", rec.Code)
		}
		if rec := doReq(http.MethodDelete, "Bearer "+createKey, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("DELETE with create-only API key: expected 403, got %d", rec.Code)
		}

		// View-only API key reads preview (200)
		if rec := doReq(http.MethodGet, "Bearer "+viewKey, nil); rec.Code != http.StatusOK {
			t.Fatalf("GET with view API key: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		// Edit+Delete API key replaces preview (200) and deletes preview (200)
		if rec := doReq(http.MethodPut, "Bearer "+editDeleteKey, webp); rec.Code != http.StatusOK {
			t.Fatalf("PUT with edit API key: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec := doReq(http.MethodDelete, "Bearer "+editDeleteKey, nil); rec.Code != http.StatusOK {
			t.Fatalf("DELETE with delete API key: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
