package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "mediahub_oss/docs"
	"mediahub_oss/internal/httpserver"
)

func TestSwaggerHandler(t *testing.T) {
	handler := httpserver.NewSwaggerHandler()

	t.Run("redirects /swagger to /swagger/", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/swagger", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected 301, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/swagger/" {
			t.Fatalf("expected redirect to /swagger/, got %q", loc)
		}
	})

	t.Run("serves Swagger UI v5 index and assets", func(t *testing.T) {
		for _, tc := range []struct {
			path    string
			contain string
		}{
			{path: "/swagger/", contain: "Swagger UI"},
			{path: "/swagger/swagger-initializer.js", contain: `url: "doc.json"`},
			{path: "/swagger/swagger-ui-bundle.js", contain: "SwaggerUIBundle"},
		} {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s: expected 200, got %d", tc.path, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.contain) {
				t.Fatalf("%s: expected body to contain %q", tc.path, tc.contain)
			}
		}
	})

	t.Run("serves OpenAPI 3.1 doc.json with native HTTP Basic and Bearer schemes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/swagger/doc.json", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("expected JSON Content-Type, got %q", ct)
		}

		var doc struct {
			OpenAPI string `json:"openapi"`
			Info    struct {
				Title   string `json:"title"`
				Version string `json:"version"`
			} `json:"info"`
			Servers []struct {
				URL string `json:"url"`
			} `json:"servers"`
			Components struct {
				SecuritySchemes map[string]struct {
					Type   string `json:"type"`
					Scheme string `json:"scheme"`
				} `json:"securitySchemes"`
			} `json:"components"`
			Paths map[string]any `json:"paths"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("invalid OpenAPI JSON: %v", err)
		}

		if !strings.HasPrefix(doc.OpenAPI, "3.1") {
			t.Fatalf("expected openapi 3.1.x, got %q", doc.OpenAPI)
		}
		if doc.Info.Title == "" || doc.Info.Version == "" {
			t.Fatalf("expected non-empty info title/version, got %+v", doc.Info)
		}
		if len(doc.Servers) == 0 || doc.Servers[0].URL != "/api" {
			t.Fatalf("expected server URL /api, got %+v", doc.Servers)
		}
		bearer, ok := doc.Components.SecuritySchemes["BearerAuth"]
		if !ok || bearer.Type != "http" || bearer.Scheme != "bearer" {
			t.Fatalf("expected BearerAuth http/bearer scheme, got %+v (present=%v)", bearer, ok)
		}
		basic, ok := doc.Components.SecuritySchemes["BasicAuth"]
		if !ok || basic.Type != "http" || basic.Scheme != "basic" {
			t.Fatalf("expected BasicAuth http/basic scheme, got %+v (present=%v)", basic, ok)
		}
		if _, hasDoublePrefix := doc.Paths["/api/token"]; hasDoublePrefix {
			t.Fatalf("found double-prefixed path /api/token under /api server base")
		}
		if _, hasToken := doc.Paths["/token"]; !hasToken {
			t.Fatalf("expected /token path in OpenAPI paths")
		}
	})
}
