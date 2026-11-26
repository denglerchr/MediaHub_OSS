package utils_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/ulid"
)

func TestGlobalAdmin(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	allBits := []repository.AccessGrant{
		repository.AccessView,
		repository.AccessCreate,
		repository.AccessEdit,
		repository.AccessDelete,
		repository.AccessAdmin,
	}

	g := &utils.GlobalAdmin{}
	if !g.IsGlobalAdmin() {
		t.Errorf("expected GlobalAdmin.IsGlobalAdmin() == true")
	}
	for _, bit := range allBits {
		allowed, err := g.HasPermission(ctx, dbID, bit)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Errorf("expected GlobalAdmin to grant %v", bit)
		}
	}
	full := repository.NewAccessGrant(true, true, true, true, true)
	allowed, err := g.HasPermission(ctx, dbID, full)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Errorf("expected GlobalAdmin to grant full mask")
	}
	perms, err := g.GetAllPermissions(ctx)
	if err != nil {
		t.Fatalf("unexpected error from GetAllPermissions: %v", err)
	}
	if len(perms) != 0 {
		t.Errorf("expected empty map from GlobalAdmin.GetAllPermissions, got %v", perms)
	}
}

func TestAPIKeyOfAdmin(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	full := repository.NewAccessGrant(true, true, true, true, true)

	cases := []struct {
		name      string
		scope     repository.AccessGrant
		wantAdmin bool
		allow     []repository.AccessGrant
		deny      []repository.AccessGrant
	}{
		{
			name:      "zero-scope APIKeyOfAdmin has IsGlobalAdmin false and denies all bits",
			scope:     0,
			wantAdmin: false,
			deny: []repository.AccessGrant{
				repository.AccessView,
				repository.AccessCreate,
				repository.AccessEdit,
				repository.AccessDelete,
				repository.AccessAdmin,
				full,
			},
		},
		{
			name:      "full-scope APIKeyOfAdmin has IsGlobalAdmin true and grants all bits",
			scope:     full,
			wantAdmin: true,
			allow: []repository.AccessGrant{
				repository.AccessView,
				repository.AccessCreate,
				repository.AccessEdit,
				repository.AccessDelete,
				repository.AccessAdmin,
				full,
			},
		},
		{
			name:      "admin-only API key has IsGlobalAdmin true, allows AccessAdmin, and denies non-admin bits",
			scope:     repository.AccessAdmin,
			wantAdmin: true,
			allow: []repository.AccessGrant{
				repository.AccessAdmin,
				repository.AccessAdmin | repository.AccessView,
			},
			deny: []repository.AccessGrant{
				repository.AccessView,
				repository.AccessCreate,
				repository.AccessEdit,
				repository.AccessDelete,
				repository.AccessView | repository.AccessDelete,
			},
		},
		{
			name:      "mixed scopes AccessAdmin | AccessView",
			scope:     repository.AccessAdmin | repository.AccessView,
			wantAdmin: true,
			allow: []repository.AccessGrant{
				repository.AccessAdmin,
				repository.AccessView,
				repository.AccessAdmin | repository.AccessDelete,
				repository.AccessView | repository.AccessEdit,
			},
			deny: []repository.AccessGrant{
				repository.AccessCreate,
				repository.AccessEdit,
				repository.AccessDelete,
				repository.AccessCreate | repository.AccessEdit | repository.AccessDelete,
			},
		},
		{
			name:      "non-admin API key has IsGlobalAdmin false and enforces scope",
			scope:     repository.AccessView | repository.AccessEdit,
			wantAdmin: false,
			allow: []repository.AccessGrant{
				repository.AccessView,
				repository.AccessEdit,
				repository.AccessView | repository.AccessDelete,
			},
			deny: []repository.AccessGrant{
				repository.AccessCreate,
				repository.AccessDelete,
				repository.AccessAdmin,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &utils.APIKeyOfAdmin{Scope: tc.scope}
			if got := a.IsGlobalAdmin(); got != tc.wantAdmin {
				t.Errorf("IsGlobalAdmin() = %v, want %v", got, tc.wantAdmin)
			}
			for _, grant := range tc.allow {
				allowed, err := a.HasPermission(ctx, dbID, grant)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !allowed {
					t.Errorf("expected grant %v to be allowed", grant)
				}
			}
			for _, grant := range tc.deny {
				allowed, err := a.HasPermission(ctx, dbID, grant)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if allowed {
					t.Errorf("expected grant %v to be denied", grant)
				}
			}
		})
	}
}

type mockPermRepo struct {
	repository.Repository
	calls int
	err   error
	perms []repository.UserPermissions
}

func (m *mockPermRepo) GetAllUserPermissions(ctx context.Context, userID repository.ULID) ([]repository.UserPermissions, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.perms, nil
}

func TestUserPermissions_DBErrorPropagatedWithoutCaching(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	userID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQS")

	repoMock := &mockPermRepo{
		err: errors.New("db connection lost"),
		perms: []repository.UserPermissions{
			{UserID: userID, DatabaseID: dbID, Roles: repository.AccessView},
		},
	}

	u := &utils.UserPermissions{
		UserULID: userID,
		Scope:    repository.NewAccessGrant(true, true, true, true, true),
		Repo:     repoMock,
	}

	allowed, err := u.HasPermission(ctx, dbID, repository.AccessView)
	if err == nil {
		t.Fatal("expected error when GetAllUserPermissions fails, got nil")
	}
	if allowed {
		t.Fatal("expected allowed=false on error")
	}

	// Clear the error: because failure must NOT set u.loaded=true, the next call retries and succeeds.
	repoMock.err = nil
	allowed, err = u.HasPermission(ctx, dbID, repository.AccessView)
	if err != nil {
		t.Fatalf("expected retry after DB recovery to succeed, got error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed=true after DB recovery")
	}
	if repoMock.calls != 2 {
		t.Fatalf("expected 2 repo calls (no caching on failure), got %d", repoMock.calls)
	}

	// Third call should hit the cache.
	allowed, err = u.HasPermission(ctx, dbID, repository.AccessView)
	if err != nil || !allowed {
		t.Fatalf("expected cached success, got allowed=%v err=%v", allowed, err)
	}
	if repoMock.calls != 2 {
		t.Fatalf("expected cached 2 repo calls, got %d", repoMock.calls)
	}
}

func TestUserPermissions_GetAllPermissions_DBErrorPropagated(t *testing.T) {
	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	userID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQS")

	repoMock := &mockPermRepo{
		err: errors.New("db timeout"),
		perms: []repository.UserPermissions{
			{UserID: userID, DatabaseID: dbID, Roles: repository.AccessView | repository.AccessCreate},
		},
	}

	u := &utils.UserPermissions{
		UserULID: userID,
		Scope:    repository.AccessView,
		Repo:     repoMock,
	}

	if _, err := u.GetAllPermissions(ctx); err == nil {
		t.Fatal("expected error from GetAllPermissions on DB failure")
	}

	repoMock.err = nil
	perms, err := u.GetAllPermissions(ctx)
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if perms[dbID] != repository.AccessView {
		t.Fatalf("expected masked AccessView, got %v", perms[dbID])
	}
}

func TestDecodeAndReadJSONBody(t *testing.T) {
	t.Run("valid JSON succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
		rec := httptest.NewRecorder()
		var dst struct {
			Name string `json:"name"`
		}
		status, msg := utils.DecodeJSONBody(rec, req, &dst)
		if status != 0 || msg != "" {
			t.Fatalf("expected (0, \"\"), got (%d, %q)", status, msg)
		}
		if dst.Name != "ok" {
			t.Fatalf("expected Name=ok, got %q", dst.Name)
		}
	})

	t.Run("nil body returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Body = nil
		rec := httptest.NewRecorder()
		var dst map[string]any
		status, _ := utils.DecodeJSONBody(rec, req, &dst)
		if status != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", status)
		}
	})

	t.Run("malformed JSON returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":`))
		rec := httptest.NewRecorder()
		var dst map[string]any
		status, msg := utils.DecodeJSONBody(rec, req, &dst)
		if status != http.StatusBadRequest || msg != "Invalid JSON payload" {
			t.Fatalf("expected 400 Invalid JSON payload, got (%d, %q)", status, msg)
		}
	})

	t.Run("oversized body returns 413", func(t *testing.T) {
		oversized := bytes.Repeat([]byte("a"), int(utils.MaxJSONBodyBytes)+16)
		body := append([]byte(`{"data":"`), oversized...)
		body = append(body, []byte(`"}`)...)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		var dst map[string]any
		status, msg := utils.DecodeJSONBody(rec, req, &dst)
		if status != http.StatusRequestEntityTooLarge || msg != "Request body too large" {
			t.Fatalf("expected 413 Request body too large, got (%d, %q)", status, msg)
		}
	})
}
