package swagger_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "mediahub_oss/docs"
	"mediahub_oss/internal/httpserver/swagger"
)

func TestHandler(t *testing.T) {
	handler := swagger.NewHandler()

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

		assertPreviewSwaggerPaths(t, rec.Body.Bytes())
	})
}

func assertPreviewSwaggerPaths(t *testing.T, rawJSON []byte) {
	t.Helper()

	if strings.Contains(string(rawJSON), `"type":"file"`) || strings.Contains(string(rawJSON), `"type": "file"`) {
		t.Fatalf("expected normalized OpenAPI 3.1 doc to contain no legacy type:\"file\" schema")
	}

	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody *struct {
				Required bool `json:"required"`
				Content  map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
			Responses map[string]struct {
				Content map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
			Security []map[string][]string `json:"security"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rawJSON, &doc); err != nil {
		t.Fatalf("failed to unmarshal OpenAPI JSON for preview checks: %v", err)
	}

	if _, ok := doc.Components.Schemas["entryhandler.PreviewUpdateResponse"]; !ok {
		t.Fatalf("expected entryhandler.PreviewUpdateResponse in components.schemas")
	}

	previewOps, ok := doc.Paths["/database/{database_id}/entry/{id}/preview"]
	if !ok {
		t.Fatalf("expected /database/{database_id}/entry/{id}/preview in paths")
	}

	for _, method := range []string{"get", "post", "put", "delete"} {
		op, ok := previewOps[method]
		if !ok {
			t.Fatalf("expected method %s on preview path", method)
		}
		hasBasic, hasBearer := false, false
		for _, sec := range op.Security {
			if _, ok := sec["BasicAuth"]; ok {
				hasBasic = true
			}
			if _, ok := sec["BearerAuth"]; ok {
				hasBearer = true
			}
		}
		if !hasBasic || !hasBearer {
			t.Fatalf("expected BasicAuth and BearerAuth security on %s preview op, got %+v", method, op.Security)
		}
	}

	getResp := previewOps["get"].Responses["200"]
	if webp, ok := getResp.Content["image/webp"]; !ok || webp.Schema["type"] != "string" || webp.Schema["format"] != "binary" {
		t.Fatalf("expected GET preview 200 image/webp string/binary schema, got %+v", getResp.Content)
	}
	if js, ok := getResp.Content["application/json"]; !ok || js.Schema["$ref"] != "#/components/schemas/entryhandler.FileJSONResponse" {
		t.Fatalf("expected GET preview 200 application/json FileJSONResponse ref, got %+v", getResp.Content)
	}

	for _, method := range []string{"post", "put"} {
		rb := previewOps[method].RequestBody
		if rb == nil || !rb.Required || len(rb.Content) != 1 {
			t.Fatalf("expected required single-content requestBody on %s preview, got %+v", method, rb)
		}
		webp, ok := rb.Content["image/webp"]
		if !ok || webp.Schema["type"] != "string" || webp.Schema["format"] != "binary" {
			t.Fatalf("expected %s preview requestBody image/webp string/binary, got %+v", method, rb.Content)
		}
	}
}
