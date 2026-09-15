package tokenhandler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
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
func (h *TokenHandler) handleBasicAuth(r *http.Request, username, password string) (repository.User, error) {
	var user repository.User
	var err error

	user, err = h.Repo.GetUserByUsername(r.Context(), username)
	if err != nil {
		// return cause, either user not found or connection to DB broken
		return repository.User{}, err
	}

	// Prevent Service Accounts and OIDC accounts from interactive login via Basic Auth
	if user.IsServiceAccount() || user.IsOIDC() {
		return repository.User{}, customerrors.ErrPermissionDenied
	}

	// Verify password (this handler does not have an auth middleware in front)
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		return repository.User{}, customerrors.ErrPermissionDenied
	}

	return user, nil
}
