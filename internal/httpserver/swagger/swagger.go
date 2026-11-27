package swagger

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	swaggerFiles "github.com/swaggo/files/v2"
	"github.com/swaggo/swag/v2"
)

const swaggerInitializerJS = `window.onload = function() {
  window.ui = SwaggerUIBundle({
    url: "doc.json",
    dom_id: '#swagger-ui',
    deepLinking: true,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [
      SwaggerUIBundle.plugins.DownloadUrl
    ],
    layout: "StandaloneLayout"
  });
};
`

var (
	openAPIDocOnce sync.Once
	openAPIDocJSON string
	openAPIDocErr  error
)

func loadOpenAPIDoc() (string, error) {
	openAPIDocOnce.Do(func() {
		raw, err := swag.ReadDoc(swag.Name)
		if err != nil || raw == "" {
			openAPIDocErr = err
			return
		}
		openAPIDocJSON, openAPIDocErr = normalizeOpenAPIDoc(raw)
	})
	return openAPIDocJSON, openAPIDocErr
}

// normalizeOpenAPIDoc upgrades swag/v2-generated OpenAPI 3.1 edge cases:
// - replaces legacy OpenAPI "type": "file" schemas with valid JSON Schema 2020-12 binary strings
// - ensures GET .../preview and GET .../file distinguish binary media types from application/json
// - ensures POST/PUT .../preview request bodies require raw binary image/webp only
func normalizeOpenAPIDoc(raw string) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return raw, nil
	}

	replaceLegacyFileSchema(doc)

	paths, _ := doc["paths"].(map[string]any)
	if previewPath, ok := paths["/database/{database_id}/entry/{id}/preview"].(map[string]any); ok {
		if getOp, ok := previewPath["get"].(map[string]any); ok {
			setDualContentResponse(getOp, "200", "image/webp", "#/components/schemas/entryhandler.FileJSONResponse")
		}
		for _, method := range []string{"post", "put"} {
			if op, ok := previewPath[method].(map[string]any); ok {
				op["requestBody"] = map[string]any{
					"description": "Raw WebP image binary (max 128 KiB, max 200x200 px, static or animated)",
					"required":    true,
					"content": map[string]any{
						"image/webp": map[string]any{
							"schema": map[string]any{
								"type":   "string",
								"format": "binary",
							},
						},
					},
				}
			}
		}
	}

	if filePath, ok := paths["/database/{database_id}/entry/{id}/file"].(map[string]any); ok {
		if getOp, ok := filePath["get"].(map[string]any); ok {
			setDualContentResponse(getOp, "200", "application/octet-stream", "#/components/schemas/entryhandler.FileJSONResponse")
			setSingleBinaryResponse(getOp, "206", "application/octet-stream")
		}
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return raw, nil
	}
	return string(out), nil
}

func setDualContentResponse(op map[string]any, statusCode, binaryMime, jsonRef string) {
	responses, _ := op["responses"].(map[string]any)
	resp, _ := responses[statusCode].(map[string]any)
	if resp == nil {
		return
	}
	resp["content"] = map[string]any{
		binaryMime: map[string]any{
			"schema": map[string]any{
				"type":   "string",
				"format": "binary",
			},
		},
		"application/json": map[string]any{
			"schema": map[string]any{
				"$ref": jsonRef,
			},
		},
	}
}

func setSingleBinaryResponse(op map[string]any, statusCode, binaryMime string) {
	responses, _ := op["responses"].(map[string]any)
	resp, _ := responses[statusCode].(map[string]any)
	if resp == nil {
		return
	}
	resp["content"] = map[string]any{
		binaryMime: map[string]any{
			"schema": map[string]any{
				"type":   "string",
				"format": "binary",
			},
		},
	}
}

func replaceLegacyFileSchema(v any) {
	switch node := v.(type) {
	case map[string]any:
		if t, ok := node["type"].(string); ok && t == "file" {
			node["type"] = "string"
			node["format"] = "binary"
		}
		for _, val := range node {
			replaceLegacyFileSchema(val)
		}
	case []any:
		for _, item := range node {
			replaceLegacyFileSchema(item)
		}
	}
}

// NewHandler serves Swagger UI v5 from embedded swaggerFiles.FS and the
// OpenAPI 3.1 document registered with swaggo/swag/v2 at /swagger/doc.json.
func NewHandler() http.Handler {
	fileServer := http.StripPrefix("/swagger/", http.FileServer(http.FS(swaggerFiles.FS)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/swagger" {
			http.Redirect(w, r, "/swagger/", http.StatusMovedPermanently)
			return
		}

		subPath := strings.TrimPrefix(r.URL.Path, "/swagger/")
		switch subPath {
		case "doc.json":
			doc, err := loadOpenAPIDoc()
			if err != nil || doc == "" {
				http.Error(w, "OpenAPI specification not available", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(doc))
		case "swagger-initializer.js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(swaggerInitializerJS))
		default:
			fileServer.ServeHTTP(w, r)
		}
	})
}
