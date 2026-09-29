package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mediahub_oss/internal/repository"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// validateJWT parses the token string, validates the signature, and retrieves the user.
func (am *AuthMiddleware) validateJWT(ctx context.Context, tokenString string) (repository.User, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return repository.User{}, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return am.JWTSecret, nil
	})

	if err != nil {
		return repository.User{}, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		// Check expiration
		exp, err := claims.GetExpirationTime()
		if err != nil || exp == nil {
			return repository.User{}, errors.New("missing or invalid expiration claim")
		}
		if exp.Before(time.Now()) {
			return repository.User{}, errors.New("token expired")
		}

		// Extract User ID (ULID string)
		userIDStr, ok := claims["sub"].(string)
		if !ok {
			return repository.User{}, errors.New("invalid subject in token")
		}

		// Fetch fresh user data from DB to ensure they still exist / weren't banned
		return am.Repo.GetUserByID(ctx, repository.ULID(userIDStr))
	}

	return repository.User{}, errors.New("invalid token claims")
}

// validateBasicAuth decodes base64 credentials and verifies the password hash.
func (am *AuthMiddleware) validateBasicAuth(ctx context.Context, encodedValue string) (repository.User, error) {
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedValue)
	if err != nil {
		return repository.User{}, errors.New("invalid base64")
	}

	pair := strings.SplitN(string(decodedBytes), ":", 2)
	if len(pair) != 2 {
		return repository.User{}, errors.New("invalid basic auth format")
	}

	username, password := pair[0], pair[1]

	user, err := am.Repo.GetUserByUsername(ctx, username)
	if err != nil {
		return repository.User{}, errors.New("user not found")
	}

	// Prevent Service Accounts and OIDC accounts from using Basic Auth
	if user.IsServiceAccount() || user.IsOIDC() {
		return repository.User{}, errors.New("basic auth is not allowed for this account type")
	}

	// Verify Password using bcrypt
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return repository.User{}, errors.New("invalid password")
	}

	return user, nil
}

// validateAPIKey hashes the secret part of the token, queries the API key joined with the owner, and checks for expiry.
func (am *AuthMiddleware) validateAPIKey(ctx context.Context, token string) (repository.User, repository.APIKey, error) {
	if len(token) <= 4 {
		return repository.User{}, repository.APIKey{}, errors.New("invalid token length")
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
		return repository.User{}, repository.APIKey{}, errors.New("token expired")
	}

	return user, key, nil
}
