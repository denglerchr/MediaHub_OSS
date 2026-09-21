package httpserver

import (
	"fmt"
	"io"
	"mediahub_oss/internal/httpserver/auth"
	repo "mediahub_oss/internal/repository"
	"net/http"
	"strings"

	httpSwagger "github.com/swaggo/http-swagger"
)

// SetupRouter configures the main router using the Go Standard Library.
func SetupRouter(h *Handlers, frontendFS http.FileSystem, am *auth.AuthMiddleware, basePath string, allowedOrigins []string) http.Handler {
	mux := http.NewServeMux()

	// --- 1. Public Endpoints ---
	mux.HandleFunc("GET /health", h.InfoHandler.HealthCheck)
	mux.HandleFunc("GET /api/info", h.InfoHandler.GetInfo)
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)

	// --- 2. Public Token Endpoints ---
	mux.HandleFunc("POST /api/token", h.TokenHandler.GetToken)
	mux.HandleFunc("POST /api/token/oidc", h.TokenHandler.GetTokenOIDC)
	mux.HandleFunc("POST /api/token/refresh", h.TokenHandler.RefreshToken)

	// --- 3. Authenticated Routes (Logout & User Self-Management) ---
	// Auth is required, but no specific role/permission.
	// We use the Chain helper for clean stacking: Chain(Handler, Auth)
	Auth := am.AuthMiddleware

	mux.Handle("POST /api/logout", Chain(h.TokenHandler.Logout, Auth))
	mux.Handle("GET /api/me", Chain(h.UserHandler.GetMe, Auth))
	mux.Handle("PATCH /api/me", Chain(h.UserHandler.UpdateMe, Auth))

	// --- 4. Feature Routes ---
	addAdminRoutes(mux, h, am)
	addDatabaseRoutes(mux, h, am)

	// --- 5. Frontend (SPA) ---
	addFrontendRoutes(mux, frontendFS, "index.html", basePath)

	// --- 6. Global Middleware Wrap ---
	// Wrap the entire router with the CORS middleware before returning
	return CORSMiddleware(allowedOrigins)(mux)
}

// addAdminRoutes configures global administrative routes.
func addAdminRoutes(mux *http.ServeMux, h *Handlers, am *auth.AuthMiddleware) {
	// Middleware Stack: Auth -> IsAdmin
	ReqAdmin := func(h http.HandlerFunc) http.Handler {
		return Chain(h, am.AuthMiddleware, am.RequireGlobalAdmin())
	}

	// User Management
	mux.Handle("GET /api/users", ReqAdmin(h.UserHandler.GetUsers))
	mux.Handle("POST /api/user", ReqAdmin(h.UserHandler.CreateUser))
	mux.Handle("GET /api/user/{user_ulid}", ReqAdmin(h.UserHandler.GetUser))
	mux.Handle("PATCH /api/user/{user_ulid}", ReqAdmin(h.UserHandler.UpdateUser))
	mux.Handle("DELETE /api/user/{user_ulid}", ReqAdmin(h.UserHandler.DeleteUser))

	// Global Database Creation and Deletion (Restricted to Admin)
	mux.Handle("POST /api/database", ReqAdmin(h.DatabaseHandler.CreateDatabase))
	mux.Handle("DELETE /api/database/{database_id}", ReqAdmin(h.DatabaseHandler.DeleteDatabase))

	// Audit Logs (Restricted to Admin)
	mux.Handle("GET /api/audit", ReqAdmin(h.AuditHandler.GetLogs))

	// API Keys Management (Admin only)
	mux.Handle("GET /api/users/keys", ReqAdmin(h.UserHandler.GetAllAPIKeys))

	// API Keys Management (Self or Admin)
	ReqSelfOrAdmin := func(hf http.HandlerFunc) http.Handler {
		return Chain(hf, am.AuthMiddleware, am.RequireSelfOrAdmin())
	}
	mux.Handle("POST /api/user/{user_ulid}/keys", ReqSelfOrAdmin(h.UserHandler.CreateAPIKey))
	mux.Handle("GET /api/user/{user_ulid}/keys", ReqSelfOrAdmin(h.UserHandler.GetAPIKeys))
	mux.Handle("GET /api/user/{user_ulid}/keys/{key_ulid}", ReqSelfOrAdmin(h.UserHandler.GetAPIKey))
	mux.Handle("PATCH /api/user/{user_ulid}/keys/{key_ulid}", ReqSelfOrAdmin(h.UserHandler.UpdateAPIKey))
	mux.Handle("DELETE /api/user/{user_ulid}/keys/{key_ulid}", ReqSelfOrAdmin(h.UserHandler.DeleteAPIKey))
}

// addDatabaseRoutes configures database routes AND nested entry routes.
func addDatabaseRoutes(mux *http.ServeMux, h *Handlers, am *auth.AuthMiddleware) {
	// Stack: Auth -> Check Permission for {database_id}
	ReqPerm := func(perm repo.AccessGrant, h http.HandlerFunc) http.Handler {
		return Chain(h, am.AuthMiddleware, am.RequireDatabasePermission(perm))
	}
	// 1. Global Database List (Any Authenticated User)
	mux.Handle("GET /api/databases", Chain(h.DatabaseHandler.GetDatabases, am.AuthMiddleware))

	// 2. Database Admin Operations (Global Admin or DB Admin)
	mux.Handle("PUT /api/database/{database_id}", ReqPerm(repo.AccessAdmin, h.DatabaseHandler.UpdateDatabase))
	mux.Handle("POST /api/database/{database_id}/field", ReqPerm(repo.AccessAdmin, h.DatabaseHandler.AddField))
	mux.Handle("PATCH /api/database/{database_id}/field/{field_id}", ReqPerm(repo.AccessAdmin, h.DatabaseHandler.UpdateField))
	mux.Handle("DELETE /api/database/{database_id}/field/{field_id}", ReqPerm(repo.AccessAdmin, h.DatabaseHandler.DeleteField))

	// 3. Database View Operations (CanView / CanCreate / CanEdit / CanDelete/ CanAdmin)
	// Covers getting DB stats, searching entries, and viewing specific entries
	mux.Handle("GET /api/database/{database_id}", ReqPerm(repo.AccessView|repo.AccessCreate|repo.AccessEdit|repo.AccessDelete|repo.AccessAdmin, h.DatabaseHandler.GetDatabase))
	mux.Handle("GET /api/database/{database_id}/fields", ReqPerm(repo.AccessView|repo.AccessCreate|repo.AccessEdit|repo.AccessDelete|repo.AccessAdmin, h.DatabaseHandler.GetFields))

	// Bulk Operations (List/Search/Export/Import)
	mux.Handle("GET /api/database/{database_id}/entries", ReqPerm(repo.AccessView, h.EntryHandler.QueryEntries))
	mux.Handle("POST /api/database/{database_id}/entries/search", ReqPerm(repo.AccessView, h.EntryHandler.SearchEntries))
	mux.Handle("POST /api/database/{database_id}/entries/export", ReqPerm(repo.AccessView, h.EntryHandler.ExportEntries))
	mux.Handle("POST /api/database/{database_id}/entries/import", ReqPerm(repo.AccessCreate, h.EntryHandler.ImportEntries))

	// Single Entry Read Operations
	mux.Handle("GET /api/database/{database_id}/entry/{id}", ReqPerm(repo.AccessView, h.EntryHandler.GetEntryMeta))
	mux.Handle("GET /api/database/{database_id}/entry/{id}/file", ReqPerm(repo.AccessView, h.EntryHandler.GetEntryFile))
	mux.Handle("GET /api/database/{database_id}/entry/{id}/preview", ReqPerm(repo.AccessView, h.EntryHandler.GetEntryPreview))

	// 4. Database Write Operations (CanCreate / CanEdit)
	mux.Handle("POST /api/database/{database_id}/entry", ReqPerm(repo.AccessCreate, h.EntryHandler.PostEntry))
	mux.Handle("PATCH /api/database/{database_id}/entry/{id}", ReqPerm(repo.AccessEdit, h.EntryHandler.PatchEntry))

	// 5. Database Delete Operations (CanDelete)
	mux.Handle("POST /api/database/{database_id}/housekeeping", ReqPerm(repo.AccessDelete, h.DatabaseHandler.TriggerHousekeeping))
	mux.Handle("POST /api/database/{database_id}/entries/delete", ReqPerm(repo.AccessDelete, h.EntryHandler.DeleteEntries))
	mux.Handle("DELETE /api/database/{database_id}/entry/{id}", ReqPerm(repo.AccessDelete, h.EntryHandler.DeleteEntry))
}

func addFrontendRoutes(mux *http.ServeMux, frontendFS http.FileSystem, indexFile string, basePath string) {
	fileServer := http.FileServer(frontendFS)

	// Angular requires the base href to end with a trailing slash
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")

		// If the path is empty, they want the root index
		if path == "" {
			serveModifiedIndex(w, frontendFS, indexFile, basePath)
			return
		}

		// Check if it exists AND is not a directory
		file, err := frontendFS.Open(path)
		if err == nil {
			stat, statErr := file.Stat()
			file.Close()

			if statErr == nil && !stat.IsDir() {
				// It's a real file (like .js, .css, .png). Serve it normally.
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// It wasn't a physical file, so it's an Angular route.
		// Serve the dynamically modified index.html.
		serveModifiedIndex(w, frontendFS, indexFile, basePath)
	})
}

// Helper function to dynamically modify and serve index.html
func serveModifiedIndex(w http.ResponseWriter, fs http.FileSystem, indexFile, basePath string) {
	file, err := fs.Open(indexFile)
	if err != nil {
		http.Error(w, "Index file not found", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Read the HTML into memory
	htmlBytes, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read index file", http.StatusInternalServerError)
		return
	}

	// Dynamically replace the base href
	// We assume Angular built it with the standard <base href="/">
	htmlStr := string(htmlBytes)
	htmlStr = strings.Replace(htmlStr, `<base href="/">`, fmt.Sprintf(`<base href="%s">`, basePath), 1)

	// Send it to the browser
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(htmlStr))
}

// --- Middleware Helpers ---

// Middleware defines a function that wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain allows for linear stacking of middleware.
// Usage: Chain(finalHandler, Middleware1, Middleware2)
// Execution: Middleware1 -> Middleware2 -> finalHandler
func Chain(h http.HandlerFunc, mws ...Middleware) http.Handler {
	var final http.Handler = h
	// Loop backwards so the first middleware in the slice becomes the outermost wrapper
	for i := len(mws) - 1; i >= 0; i-- {
		final = mws[i](final)
	}
	return final
}
