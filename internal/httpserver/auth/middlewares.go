package auth

import (
	"context"
	"fmt"
	"log"
	"mediahub_oss/internal/httpserver/utils"
	"mediahub_oss/internal/repository"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------
// Core Middleware Struct
// ---------------------------------------------------------------------

// AuthMiddleware holds dependencies required for authentication/authorization.
type AuthMiddleware struct {
	Repo             repository.Repository
	JWTSecret        []byte
	apiKeyUpdateChan chan APIKeyUpdateRequest // Buffered channel for debouncing and precision timing
	stopChan         chan struct{}
	doneChan         chan struct{}
}

// APIKeyUpdateRequest holds the exact timestamp the key was used for precise tracking.
type APIKeyUpdateRequest struct {
	KeyID  repository.ULID
	UsedAt time.Time
}

// NewAuthMiddleware creates a new AuthMiddleware service and starts background workers.
func NewAuthMiddleware(repo repository.Repository, secret string) *AuthMiddleware {
	am := &AuthMiddleware{
		Repo:             repo,
		JWTSecret:        []byte(secret),
		apiKeyUpdateChan: make(chan APIKeyUpdateRequest, 5000), // Generous buffer
		stopChan:         make(chan struct{}),
		doneChan:         make(chan struct{}),
	}

	// Start the background worker for API key debouncing
	go am.apiKeyUpdateWorker()

	return am
}

// Close stops the background API key update worker and flushes pending updates.
func (am *AuthMiddleware) Close() {
	select {
	case <-am.stopChan:
		// already closed
		return
	default:
		close(am.stopChan)
		<-am.doneChan
	}
}

// ---------------------------------------------------------------------
// 1. Authentication Middleware
// ---------------------------------------------------------------------

func (am *AuthMiddleware) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		schema, value, err := am.extractAuthCredentials(r)
		if err != nil {
			utils.RespondWithError(w, http.StatusUnauthorized, err.Error())
			return
		}

		user, apiKey, err := am.authenticateRequest(r.Context(), schema, value)
		if err != nil {
			log.Printf("Auth failure: %v", err)
			utils.RespondWithError(w, http.StatusUnauthorized, "Unauthorized: Invalid credentials")
			return
		}

		ctx := context.WithValue(r.Context(), utils.UserKey, &user)

		isAPIKey := !apiKey.CreatedAt.IsZero()
		if isAPIKey {
			am.asyncUpdateAPIKeyLastUsed(apiKey.ID) // Now uses the worker channel
		}

		ctx = am.cacheUserPermissions(ctx, user, apiKey, isAPIKey)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Extract either the Authorization header or the query parameter token. Returns the schema and value.
func (am *AuthMiddleware) extractAuthCredentials(r *http.Request) (string, string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 {
			return "", "", fmt.Errorf("Unauthorized: Invalid Authorization header format")
		}
		return parts[0], parts[1], nil
	}

	queryToken := r.URL.Query().Get("token")
	if queryToken != "" {
		return "Bearer", queryToken, nil
	}

	return "", "", fmt.Errorf("Unauthorized: Missing Authorization header or query token")
}

func (am *AuthMiddleware) authenticateRequest(ctx context.Context, schema, value string) (repository.User, repository.APIKey, error) {
	switch schema {
	case "Bearer":
		if strings.HasPrefix(value, "srv_") {
			user, apiKey, err := am.validateAPIKey(ctx, value)
			return user, apiKey, err
		}
		user, err := am.validateJWT(ctx, value)
		return user, repository.APIKey{}, err
	case "Basic":
		user, err := am.validateBasicAuth(ctx, value)
		return user, repository.APIKey{}, err
	default:
		return repository.User{}, repository.APIKey{}, fmt.Errorf("Unsupported scheme: %s", schema)
	}
}

// asyncUpdateAPIKeyLastUsed sends the update to a non-blocking channel with a precise timestamp.
func (am *AuthMiddleware) asyncUpdateAPIKeyLastUsed(keyID repository.ULID) {
	select {
	case am.apiKeyUpdateChan <- APIKeyUpdateRequest{KeyID: keyID, UsedAt: time.Now()}:
		// Queued successfully
	default:
		// Channel is full (extreme load), drop this specific timestamp update
		// to prevent the middleware from blocking HTTP requests.
		log.Printf("API key update channel full, dropping update for key %s", keyID)
	}
}

// cacheUserPermissions assigns a `PermissionHolder` into the context, which will then lazily loads bitmasked permissions as required.
func (am *AuthMiddleware) cacheUserPermissions(ctx context.Context, user repository.User, apiKey repository.APIKey, isAPIKey bool) context.Context {
	var holder utils.PermissionHolder

	isEffectiveAdmin := user.IsAdmin
	if isAPIKey {
		isEffectiveAdmin = isEffectiveAdmin && apiKey.Scope.HasAccess(repository.AccessAdmin)
	}

	if isEffectiveAdmin {
		holder = &utils.GlobalAdmin{
			UserULID: user.ID,
			Repo:     am.Repo,
		}
		return context.WithValue(ctx, utils.PermissionHolderKey, holder)
	}

	if isAPIKey {
		if user.IsAdmin {
			holder = &utils.APIKeyOfAdmin{
				UserULID: user.ID,
				Scope:    apiKey.Scope,
				Repo:     am.Repo,
			}
		} else {
			holder = &utils.UserPermissions{
				UserULID: user.ID,
				Scope:    apiKey.Scope,
				Repo:     am.Repo,
			}
		}
	} else {
		// Full scope for normal session
		holder = &utils.UserPermissions{
			UserULID: user.ID,
			Scope:    repository.NewAccessGrant(true, true, true, true, true),
			Repo:     am.Repo,
		}
	}

	return context.WithValue(ctx, utils.PermissionHolderKey, holder)
}

// ---------------------------------------------------------------------
// 2. Authorization Middlewares
// ---------------------------------------------------------------------

func (am *AuthMiddleware) RequireGlobalAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			holder := utils.GetPermissionHolderFromContext(r.Context())
			if !holder.IsGlobalAdmin() {
				utils.RespondWithError(w, http.StatusForbidden, "Forbidden: Admin access required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (am *AuthMiddleware) RequireDatabasePermission(perm repository.AccessGrant) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			dbID := r.PathValue("database_id")
			if dbID == "" {
				utils.RespondWithError(w, http.StatusBadRequest, "Bad Request: Missing database context")
				return
			}

			holder := utils.GetPermissionHolderFromContext(r.Context())
			if !holder.HasPermission(repository.ULID(dbID), perm) {
				utils.RespondWithError(w, http.StatusForbidden, fmt.Sprintf("Forbidden: You lack required rights on database '%s'", dbID))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (am *AuthMiddleware) RequireSelfOrAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := utils.GetUserFromContext(r.Context())
			holder := utils.GetPermissionHolderFromContext(r.Context())

			if holder.IsGlobalAdmin() {
				next.ServeHTTP(w, r)
				return
			}

			userULID := r.PathValue("user_ulid")
			if userULID == "" {
				userULID = r.PathValue("user_id")
			}

			if userULID == "" || repository.ULID(userULID) != user.ID {
				utils.RespondWithError(w, http.StatusForbidden, "Forbidden: You are not authorized to manage this resource")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
