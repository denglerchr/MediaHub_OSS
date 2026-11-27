package httpserver

import (
	"net/http"
	"strings"

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

// NewSwaggerHandler serves Swagger UI v5 from embedded swaggerFiles.FS and the
// OpenAPI 3.1 document registered with swaggo/swag/v2 at /swagger/doc.json.
func NewSwaggerHandler() http.Handler {
	fileServer := http.StripPrefix("/swagger/", http.FileServer(http.FS(swaggerFiles.FS)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/swagger" {
			http.Redirect(w, r, "/swagger/", http.StatusMovedPermanently)
			return
		}

		subPath := strings.TrimPrefix(r.URL.Path, "/swagger/")
		switch subPath {
		case "doc.json":
			doc, err := swag.ReadDoc(swag.Name)
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
