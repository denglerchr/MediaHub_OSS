package tokenhandler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"mediahub_oss/internal/httpserver/auth"
	"mediahub_oss/internal/repository"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// generateTokens creates a new JWT Access Token and a secure random Refresh Token.
func (h *TokenHandler) generateTokens(r *http.Request, userID repository.ULID) (string, string, error) {
	// 1. Generate JWT Access Token
	claims := jwt.MapClaims{
		"sub": userID.String(),
		"exp": time.Now().Add(h.AccessDuration).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString(h.JWTSecret)
	if err != nil {
		return "", "", err
	}

	// 2. Generate cryptographically secure Refresh Token
	rawTokenBytes := make([]byte, 32)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		return "", "", err
	}
	refreshToken := base64.URLEncoding.EncodeToString(rawTokenBytes)

	// 3. Hash the Refresh Token for storage
	tokenHash := hashToken(refreshToken)

	// 4. Store the hash in the DB
	err = h.Repo.StoreRefreshToken(r.Context(), userID, tokenHash, h.RefreshDuration)
	if err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

// hashToken takes a plaintext refresh token and returns its SHA-256 hash as a hex string.
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// verify the user exists and the password is correct. Return the user id
// Delegates to the shared Basic Auth credential check in the auth package so
// both paths apply the same rules (including the Service Account / OIDC rejection).
func (h *TokenHandler) handleBasicAuth(r *http.Request, username, password string) (repository.User, error) {
	return auth.ValidateBasicAuth(r.Context(), h.Repo, username, password)
}
