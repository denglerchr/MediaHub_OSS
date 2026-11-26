package audithandler_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"mediahub_oss/internal/httpserver/audithandler"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/repository"
)

func TestGetLogs_InvalidQueryParamsReturn400(t *testing.T) {
	handler := &audithandler.AuditHandler{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	tests := []struct {
		name string
		url  string
	}{
		{"invalid limit", "/api/audit?limit=abc"},
		{"excessive limit", "/api/audit?limit=100000"},
		{"invalid offset", "/api/audit?offset=xyz"},
		{"invalid tstart", "/api/audit?tstart=not-a-number"},
		{"invalid tend", "/api/audit?tend=foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			req = req.WithContext(context.WithValue(req.Context(), utils.UserKey, &repository.User{Username: "admin", IsAdmin: true}))
			w := httptest.NewRecorder()

			handler.GetLogs(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status 400 for %s, got %d", tt.url, w.Code)
			}
		})
	}
}
