package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// validateJWT parses the token string, validates the signature, and retrieves the user.
func (am *AuthMiddleware) validateJWT(ctx context.Context, tokenString string) (repository.User, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		// Pin the algorithm to HS256. Accepting any HMAC method (HS384/HS512)
		// widens the attack surface for algorithm-confusion issues.
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return am.JWTSecret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))

	if err != nil {
		return repository.User{}, fmt.Errorf("%w: %w", customerrors.ErrPermissionDenied, err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		// Check expiration
		exp, err := claims.GetExpirationTime()
		if err != nil || exp == nil {
			return repository.User{}, fmt.Errorf("%w: missing or invalid expiration claim", customerrors.ErrPermissionDenied)
		}
		if exp.Before(time.Now()) {
			return repository.User{}, fmt.Errorf("%w: token expired", customerrors.ErrPermissionDenied)
		}

		// Extract User ID (ULID string)
		userIDStr, ok := claims["sub"].(string)
		if !ok || userIDStr == "" {
			return repository.User{}, fmt.Errorf("%w: invalid subject in token", customerrors.ErrPermissionDenied)
		}
		userID, err := ulid.Parse(userIDStr)
		if err != nil {
			return repository.User{}, fmt.Errorf("%w: invalid subject in token: %w", customerrors.ErrPermissionDenied, err)
		}

		// Fetch fresh user data from DB to ensure they still exist / weren't banned
		return am.Repo.GetUserByID(ctx, userID)
	}

	return repository.User{}, fmt.Errorf("%w: invalid token claims", customerrors.ErrPermissionDenied)
}

// validateBasicAuth decodes base64 credentials and verifies the password hash.
func (am *AuthMiddleware) validateBasicAuth(ctx context.Context, encodedValue string) (repository.User, error) {
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedValue)
	if err != nil {
		return repository.User{}, fmt.Errorf("%w: invalid base64", customerrors.ErrPermissionDenied)
	}

	pair := strings.SplitN(string(decodedBytes), ":", 2)
	if len(pair) != 2 {
		return repository.User{}, fmt.Errorf("%w: invalid basic auth format", customerrors.ErrPermissionDenied)
	}

	return ValidateBasicAuth(ctx, am.Repo, pair[0], pair[1])
}

// ValidateBasicAuth verifies that the user exists and the password is correct.
// It is the shared Basic Auth credential check used by both the auth middleware
// and the token handler.
//
// Service Accounts and OIDC accounts are rejected because they must not log in
// interactively via Basic Auth. Credential failures return errors matching
// customerrors.ErrPermissionDenied and a missing user wraps the repository's
// not-found error, so callers can map them to 401 responses; any other
// repository error indicates an infrastructure failure.
func ValidateBasicAuth(ctx context.Context, repo repository.Repository, username, password string) (repository.User, error) {
	user, err := repo.GetUserByUsername(ctx, username)
	if err != nil {
		return repository.User{}, fmt.Errorf("user not found: %w", err)
	}

	// Prevent Service Accounts and OIDC accounts from using Basic Auth
	if user.IsServiceAccount() || user.IsOIDC() {
		return repository.User{}, fmt.Errorf("%w: basic auth is not allowed for this account type", customerrors.ErrPermissionDenied)
	}

	// Verify Password using bcrypt
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return repository.User{}, fmt.Errorf("%w: invalid password", customerrors.ErrPermissionDenied)
	}

	return user, nil
}

// validateAPIKey hashes the secret part of the token, queries the API key joined with the owner, and checks for expiry.
func (am *AuthMiddleware) validateAPIKey(ctx context.Context, token string) (repository.User, repository.APIKey, error) {
	if len(token) <= 4 {
		return repository.User{}, repository.APIKey{}, fmt.Errorf("%w: invalid token length", customerrors.ErrPermissionDenied)
	}

	secret := token[4:]
	hashBytes := sha256.Sum256([]byte(secret))
	keyHash := hex.EncodeToString(hashBytes[:])

	key, user, err := am.Repo.GetAPIKeyWithOwnerByHash(ctx, keyHash)
	if err != nil {
		return repository.User{}, repository.APIKey{}, err
	}

	// Validate expiry
	if !key.ExpiresAt.IsZero() && time.Now().After(key.ExpiresAt) {
		return repository.User{}, repository.APIKey{}, fmt.Errorf("%w: token expired", customerrors.ErrPermissionDenied)
	}

	return user, key, nil
}
