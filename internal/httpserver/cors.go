package httpserver

import (
	"net/http"
	"strings"
)

// CORSMiddleware creates a middleware that handles Cross-Origin Resource Sharing (CORS).
// It verifies the Origin header against the configured allowed origins.
func CORSMiddleware(allowedOrigins []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Responses vary depending on the Origin header (the CORS headers are
			// only added for allowed origins), so caches must key on it.
			w.Header().Add("Vary", "Origin")

			origin := r.Header.Get("Origin")

			// If no Origin header is present (same-origin request), or if the server
			// hasn't configured any allowed origins, we skip the CORS logic.
			if origin == "" || len(allowedOrigins) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Check if the request's origin matches our allowed list
			isExactMatch := false
			isWildcard := false
			for _, o := range allowedOrigins {
				o = strings.TrimSpace(o)
				if o == origin {
					isExactMatch = true
					break
				}
				if o == "*" {
					isWildcard = true
				}
			}

			// If the origin is allowed, append the required CORS headers
			if isExactMatch {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, Range")
				w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges, Content-Disposition")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			} else if isWildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, Range")
				w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges, Content-Disposition")
			}

			// Handle preflight requests (OPTIONS method)
			// The browser sends this before the actual request to verify permissions.
			if r.Method == http.MethodOptions {
				// Preflight responses also depend on the requested method and headers.
				w.Header().Add("Vary", "Access-Control-Request-Method, Access-Control-Request-Headers")
				if isExactMatch || isWildcard {
					// Cache the preflight result for 10 minutes to avoid an extra
					// OPTIONS round-trip before every actual request.
					w.Header().Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// For all other standard requests, pass the execution to the next handler
			next.ServeHTTP(w, r)
		})
	}
}
