package infohandler_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"mediahub_oss/internal/httpserver/infohandler"
	"mediahub_oss/internal/logging/audit"
	"mediahub_oss/internal/media"
)

type mockMediaConverter struct {
	media.MediaConverter
}

func (m *mockMediaConverter) GetOutputMimeTypes(contentType string) []string {
	return []string{}
}

type mockAuthEndpointProvider struct {
	authEndpoint string
	err          error
}

func (m *mockAuthEndpointProvider) GetAuthEndpoint(ctx context.Context) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.authEndpoint, nil
}

func TestInfoHandler_GetInfo_OIDCEnabled_WithAuthEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, nil)
	mc := &mockMediaConverter{}
	provider := &mockAuthEndpointProvider{
		authEndpoint: "https://idp.example.com/oauth2/authorize",
	}

	handler := infohandler.NewInfoHandler(
		logger,
		auditor,
		"1.0.0-test",
		mc,
		true, // oidcEnabled
		false,
		"https://idp.example.com",
		"mediahub-client",
		"https://app.example.com/auth/callback",
		provider,
		false,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	rec := httptest.NewRecorder()

	handler.GetInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}

	var resp infohandler.InfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.OIDC.Enabled {
		t.Fatal("expected OIDC to be enabled")
	}
	if resp.OIDC.AuthEndpoint != "https://idp.example.com/oauth2/authorize" {
		t.Fatalf("expected auth endpoint 'https://idp.example.com/oauth2/authorize', got '%s'", resp.OIDC.AuthEndpoint)
	}
}

func TestInfoHandler_GetInfo_OIDCDisabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditor := audit.NewAuditLogger(false, "stdio", logger, nil)
	mc := &mockMediaConverter{}

	handler := infohandler.NewInfoHandler(
		logger,
		auditor,
		"1.0.0-test",
		mc,
		false, // oidcEnabled
		false,
		"",
		"",
		"",
		nil,
		false,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	rec := httptest.NewRecorder()

	handler.GetInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}

	var resp infohandler.InfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.OIDC.Enabled {
		t.Fatal("expected OIDC to be disabled")
	}
	if resp.OIDC.AuthEndpoint != "" {
		t.Fatalf("expected empty auth endpoint when disabled, got '%s'", resp.OIDC.AuthEndpoint)
	}
}

func TestInfoHandler_HealthCheck(t *testing.T) {
	handler := &infohandler.InfoHandler{}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.HealthCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
}
