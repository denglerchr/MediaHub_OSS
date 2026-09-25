package tokenhandler_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mediahub_oss/internal/httpserver/tokenhandler"

	"github.com/golang-jwt/jwt/v5"
)

func TestHTTPOIDCProvider_ExchangeCode_RSA_Success(t *testing.T) {
	// Generate RSA key for test IdP
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	kid := "test-rsa-key-1"
	nStr := base64.RawURLEncoding.EncodeToString(privKey.N.Bytes())
	// Include padding in exponent string to test resilient base64 decoding
	eBytes := big.NewInt(int64(privKey.E)).Bytes()
	eStr := base64.URLEncoding.EncodeToString(eBytes) // With standard padding "="

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 serverURL,
				"authorization_endpoint": serverURL + "/auth",
				"token_endpoint":         serverURL + "/token",
				"jwks_uri":               serverURL + "/jwks",
				"userinfo_endpoint":      serverURL + "/userinfo",
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{
						"kty": "RSA",
						"kid": kid,
						"alg": "RS256",
						"n":   nStr,
						"e":   eStr,
					},
				},
			})
		case "/token":
			// Generate signed ID token
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
				"iss":                serverURL,
				"sub":                "rsa-user-456",
				"aud":                "test-client-id",
				"preferred_username": "rsauser",
				"email":              "rsa@example.com",
				"exp":                time.Now().Add(1 * time.Hour).Unix(),
			})
			token.Header["kid"] = kid
			signedIDToken, signErr := token.SignedString(privKey)
			if signErr != nil {
				http.Error(w, signErr.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "dummy_access_token",
				"id_token":     signedIDToken,
				"token_type":   "Bearer",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: serverURL,
		ClientID:  "test-client-id",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	claims, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
	if err != nil {
		t.Fatalf("expected successful exchange with verified RSA token, got error: %v", err)
	}

	if claims.Subject != "rsa-user-456" {
		t.Errorf("expected subject 'rsa-user-456', got '%s'", claims.Subject)
	}
	if claims.PreferredUsername != "rsauser" {
		t.Errorf("expected preferred_username 'rsauser', got '%s'", claims.PreferredUsername)
	}
}

func TestHTTPOIDCProvider_ExchangeCode_ECDSA_Success(t *testing.T) {
	// Generate ECDSA P-256 key for test IdP
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}

	kid := "test-ec-key-1"
	xStr := base64.RawURLEncoding.EncodeToString(privKey.X.Bytes())
	yStr := base64.RawURLEncoding.EncodeToString(privKey.Y.Bytes())

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 serverURL,
				"authorization_endpoint": serverURL + "/auth",
				"token_endpoint":         serverURL + "/token",
				"jwks_uri":               serverURL + "/jwks",
				"userinfo_endpoint":      serverURL + "/userinfo",
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{
						"kty": "EC",
						"kid": kid,
						"crv": "P-256",
						"alg": "ES256",
						"x":   xStr,
						"y":   yStr,
					},
				},
			})
		case "/token":
			token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
				"iss":                serverURL,
				"sub":                "ec-user-789",
				"aud":                "test-client-id",
				"preferred_username": "ecuser",
				"email":              "ec@example.com",
				"exp":                time.Now().Add(1 * time.Hour).Unix(),
			})
			token.Header["kid"] = kid
			signedIDToken, signErr := token.SignedString(privKey)
			if signErr != nil {
				http.Error(w, signErr.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "dummy_access_token",
				"id_token":     signedIDToken,
				"token_type":   "Bearer",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: serverURL,
		ClientID:  "test-client-id",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	claims, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
	if err != nil {
		t.Fatalf("expected successful exchange with verified ECDSA token, got error: %v", err)
	}

	if claims.Subject != "ec-user-789" {
		t.Errorf("expected subject 'ec-user-789', got '%s'", claims.Subject)
	}
	if claims.PreferredUsername != "ecuser" {
		t.Errorf("expected preferred_username 'ecuser', got '%s'", claims.PreferredUsername)
	}
}

func TestHTTPOIDCProvider_ExchangeCode_TamperedTokenRejected(t *testing.T) {
	// Generate two distinct RSA keys: genuine and attacker
	genuineKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	attackerKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	kid := "genuine-key-1"
	nStr := base64.RawURLEncoding.EncodeToString(genuineKey.N.Bytes())
	eBytes := big.NewInt(int64(genuineKey.E)).Bytes()
	eStr := base64.RawURLEncoding.EncodeToString(eBytes)

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 serverURL,
				"authorization_endpoint": serverURL + "/auth",
				"token_endpoint":         serverURL + "/token",
				"jwks_uri":               serverURL + "/jwks",
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{
						"kty": "RSA",
						"kid": kid,
						"n":   nStr,
						"e":   eStr,
					},
				},
			})
		case "/token":
			// Token signed with attackerKey instead of genuineKey!
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
				"iss":                serverURL,
				"sub":                "attacker-impersonated-admin",
				"aud":                "test-client-id",
				"preferred_username": "admin",
				"exp":                time.Now().Add(1 * time.Hour).Unix(),
			})
			token.Header["kid"] = kid
			signedIDToken, _ := token.SignedString(attackerKey)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "dummy_access_token",
				"id_token":     signedIDToken,
				"token_type":   "Bearer",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: serverURL,
		ClientID:  "test-client-id",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	// Must fail because signature cannot be verified with JWKS key; must NOT fall back to unverified claims!
	_, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
	if err == nil {
		t.Fatal("expected tampered token to be rejected, but exchange succeeded")
	}
}

func TestHTTPOIDCProvider_ExchangeCode_MismatchedAudienceRejected(t *testing.T) {
	privKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "rsa-key-aud"
	nStr := base64.RawURLEncoding.EncodeToString(privKey.N.Bytes())
	eBytes := big.NewInt(int64(privKey.E)).Bytes()
	eStr := base64.RawURLEncoding.EncodeToString(eBytes)

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":         serverURL,
				"token_endpoint": serverURL + "/token",
				"jwks_uri":       serverURL + "/jwks",
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{
						"kty": "RSA",
						"kid": kid,
						"n":   nStr,
						"e":   eStr,
					},
				},
			})
		case "/token":
			// Token signed with wrong audience
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
				"iss":                serverURL,
				"sub":                "user-1",
				"aud":                "wrong-client-id",
				"preferred_username": "user1",
				"exp":                time.Now().Add(1 * time.Hour).Unix(),
			})
			token.Header["kid"] = kid
			signedIDToken, _ := token.SignedString(privKey)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "dummy_access_token",
				"id_token":     signedIDToken,
				"token_type":   "Bearer",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: serverURL,
		ClientID:  "expected-client-id",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	_, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
	if err == nil {
		t.Fatal("expected mismatched audience token to be rejected, but exchange succeeded")
	}
}

func TestHTTPOIDCProvider_ExchangeCode_UserinfoFallback(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 serverURL,
				"authorization_endpoint": serverURL + "/auth",
				"token_endpoint":         serverURL + "/token",
				"jwks_uri":               serverURL + "/jwks",
				"userinfo_endpoint":      serverURL + "/userinfo",
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
		case "/token":
			// Provider returns access token but NO id_token
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "valid_userinfo_access_token",
				"token_type":   "Bearer",
			})
		case "/userinfo":
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer valid_userinfo_access_token" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":                "userinfo-sub-999",
				"preferred_username": "userinfo_guy",
				"email":              "userinfo@example.com",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: serverURL,
		ClientID:  "test-client-id",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	claims, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
	if err != nil {
		t.Fatalf("expected successful userinfo fallback when id_token is absent, got error: %v", err)
	}

	if claims.Subject != "userinfo-sub-999" {
		t.Errorf("expected subject 'userinfo-sub-999', got '%s'", claims.Subject)
	}
	if claims.PreferredUsername != "userinfo_guy" {
		t.Errorf("expected preferred_username 'userinfo_guy', got '%s'", claims.PreferredUsername)
	}
}

func TestHTTPOIDCProvider_GetAuthEndpoint(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 serverURL,
				"authorization_endpoint": serverURL + "/oauth2/authorize",
				"token_endpoint":         serverURL + "/oauth2/token",
				"jwks_uri":               serverURL + "/oauth2/jwks",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	serverURL = server.URL

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: serverURL,
		ClientID:  "test-client-id",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	authEndpoint, err := provider.GetAuthEndpoint(context.Background())
	if err != nil {
		t.Fatalf("expected GetAuthEndpoint to succeed, got %v", err)
	}
	if authEndpoint != serverURL+"/oauth2/authorize" {
		t.Errorf("expected auth endpoint '%s', got '%s'", serverURL+"/oauth2/authorize", authEndpoint)
	}
}
