package tokenhandler_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestHTTPOIDCProvider_ExchangeCode_MissingIDToken_Rejected(t *testing.T) {
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

	_, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
	if err == nil {
		t.Fatal("expected missing id_token to be rejected, but exchange succeeded")
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

func TestHTTPOIDCProvider_ExchangeCode_MissingExpRejected(t *testing.T) {
	privKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "rsa-no-exp"
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
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
				"iss": serverURL,
				"sub": "user-without-exp",
				"aud": "test-client-id",
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
		ClientID:  "test-client-id",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	_, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
	if err == nil {
		t.Fatal("expected token without exp claim to be rejected, but exchange succeeded")
	}
}

func TestHTTPOIDCProvider_ExchangeCode_UserinfoSubVerification(t *testing.T) {
	privKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "rsa-userinfo-sub"
	nStr := base64.RawURLEncoding.EncodeToString(privKey.N.Bytes())
	eBytes := big.NewInt(int64(privKey.E)).Bytes()
	eStr := base64.RawURLEncoding.EncodeToString(eBytes)

	t.Run("matching sub in userinfo succeeds", func(t *testing.T) {
		var serverURL string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"issuer":            serverURL,
					"token_endpoint":    serverURL + "/token",
					"jwks_uri":          serverURL + "/jwks",
					"userinfo_endpoint": serverURL + "/userinfo",
				})
			case "/jwks":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"keys": []map[string]any{
						{"kty": "RSA", "kid": kid, "n": nStr, "e": eStr},
					},
				})
			case "/token":
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
					"iss": serverURL,
					"sub": "subject-123",
					"aud": "test-client-id",
					"exp": time.Now().Add(1 * time.Hour).Unix(),
				})
				token.Header["kid"] = kid
				signedIDToken, _ := token.SignedString(privKey)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"access_token": "valid_access_token",
					"id_token":     signedIDToken,
					"token_type":   "Bearer",
				})
			case "/userinfo":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"sub":                "subject-123",
					"preferred_username": "verified_user",
					"email":              "verified@example.com",
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		serverURL = server.URL

		cfg := tokenhandler.OIDCConfig{Enabled: true, IssuerURL: serverURL, ClientID: "test-client-id"}
		provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

		claims, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
		if err != nil {
			t.Fatalf("expected successful exchange with matching userinfo sub, got: %v", err)
		}
		if claims.Subject != "subject-123" {
			t.Errorf("expected subject 'subject-123', got '%s'", claims.Subject)
		}
		if claims.PreferredUsername != "verified_user" {
			t.Errorf("expected preferred_username 'verified_user', got '%s'", claims.PreferredUsername)
		}
	})

	t.Run("mismatched sub in userinfo is rejected", func(t *testing.T) {
		var serverURL string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"issuer":            serverURL,
					"token_endpoint":    serverURL + "/token",
					"jwks_uri":          serverURL + "/jwks",
					"userinfo_endpoint": serverURL + "/userinfo",
				})
			case "/jwks":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"keys": []map[string]any{
						{"kty": "RSA", "kid": kid, "n": nStr, "e": eStr},
					},
				})
			case "/token":
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
					"iss": serverURL,
					"sub": "subject-123",
					"aud": "test-client-id",
					"exp": time.Now().Add(1 * time.Hour).Unix(),
				})
				token.Header["kid"] = kid
				signedIDToken, _ := token.SignedString(privKey)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"access_token": "valid_access_token",
					"id_token":     signedIDToken,
					"token_type":   "Bearer",
				})
			case "/userinfo":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"sub":                "different-subject-456",
					"preferred_username": "impersonator",
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		serverURL = server.URL

		cfg := tokenhandler.OIDCConfig{Enabled: true, IssuerURL: serverURL, ClientID: "test-client-id"}
		provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

		_, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
		if err == nil {
			t.Fatal("expected mismatched userinfo sub to be rejected, but exchange succeeded")
		}
	})

	t.Run("empty sub in userinfo is rejected", func(t *testing.T) {
		var serverURL string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"issuer":            serverURL,
					"token_endpoint":    serverURL + "/token",
					"jwks_uri":          serverURL + "/jwks",
					"userinfo_endpoint": serverURL + "/userinfo",
				})
			case "/jwks":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"keys": []map[string]any{
						{"kty": "RSA", "kid": kid, "n": nStr, "e": eStr},
					},
				})
			case "/token":
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
					"iss": serverURL,
					"sub": "subject-123",
					"aud": "test-client-id",
					"exp": time.Now().Add(1 * time.Hour).Unix(),
				})
				token.Header["kid"] = kid
				signedIDToken, _ := token.SignedString(privKey)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"access_token": "valid_access_token",
					"id_token":     signedIDToken,
					"token_type":   "Bearer",
				})
			case "/userinfo":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"preferred_username": "no_sub_user",
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		serverURL = server.URL

		cfg := tokenhandler.OIDCConfig{Enabled: true, IssuerURL: serverURL, ClientID: "test-client-id"}
		provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

		_, err := provider.ExchangeCode(context.Background(), "auth_code", "http://localhost/callback", "")
		if err == nil {
			t.Fatal("expected empty userinfo sub to be rejected, but exchange succeeded")
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonHTTPResponse(status int, payload any) *http.Response {
	b, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

func TestHTTPOIDCProvider_SecurityHardening(t *testing.T) {
	privKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	nStr := base64.RawURLEncoding.EncodeToString(privKey.N.Bytes())
	eBytes := big.NewInt(int64(privKey.E)).Bytes()
	eStr := base64.RawURLEncoding.EncodeToString(eBytes)
	const issuerURL = "https://idp.example.test"

	newProvider := func(rt roundTripFunc) *tokenhandler.HTTPOIDCProvider {
		p := tokenhandler.NewHTTPOIDCProvider(tokenhandler.OIDCConfig{
			Enabled:   true,
			IssuerURL: issuerURL,
			ClientID:  "test-client-id",
		}, nil)
		p.HTTPClient = &http.Client{Transport: rt}
		return p
	}

	t.Run("rejects HS256 and none signing algorithms", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			sign func() string
		}{
			{
				name: "HS256",
				sign: func() string {
					tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
						"iss": issuerURL,
						"sub": "user-hs",
						"aud": "test-client-id",
						"exp": time.Now().Add(time.Hour).Unix(),
					})
					tok.Header["kid"] = "rsa-key-1"
					s, _ := tok.SignedString([]byte("secret"))
					return s
				},
			},
			{
				name: "none",
				sign: func() string {
					tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
						"iss": issuerURL,
						"sub": "user-none",
						"aud": "test-client-id",
						"exp": time.Now().Add(time.Hour).Unix(),
					})
					tok.Header["kid"] = "rsa-key-1"
					s, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
					return s
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var jwksCalled atomic.Bool
				provider := newProvider(func(r *http.Request) (*http.Response, error) {
					switch r.URL.Path {
					case "/.well-known/openid-configuration":
						return jsonHTTPResponse(http.StatusOK, map[string]string{
							"issuer":         issuerURL,
							"token_endpoint": issuerURL + "/token",
							"jwks_uri":       issuerURL + "/jwks",
						}), nil
					case "/jwks":
						jwksCalled.Store(true)
						return jsonHTTPResponse(http.StatusOK, map[string]any{
							"keys": []map[string]any{
								{"kty": "RSA", "kid": "rsa-key-1", "use": "sig", "alg": "RS256", "n": nStr, "e": eStr},
							},
						}), nil
					case "/token":
						return jsonHTTPResponse(http.StatusOK, map[string]string{
							"access_token": "tok",
							"id_token":     tc.sign(),
						}), nil
					default:
						return jsonHTTPResponse(http.StatusNotFound, map[string]string{}), nil
					}
				})

				_, err := provider.ExchangeCode(context.Background(), "code", "http://localhost/cb", "")
				if err == nil {
					t.Fatalf("expected %s token to be rejected", tc.name)
				}
				if jwksCalled.Load() {
					t.Fatalf("expected %s alg to be rejected before JWKS lookup", tc.name)
				}
			})
		}
	})

	t.Run("ignores JWK with use=enc and rejects JWK alg mismatch", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			jwk     map[string]any
			wantSub string
		}{
			{
				name:    "use=enc ignored",
				jwk:     map[string]any{"kty": "RSA", "kid": "k1", "use": "enc", "alg": "RS256", "n": nStr, "e": eStr},
				wantSub: "no matching key",
			},
			{
				name:    "alg mismatch",
				jwk:     map[string]any{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS384", "n": nStr, "e": eStr},
				wantSub: "does not match JWK alg",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				provider := newProvider(func(r *http.Request) (*http.Response, error) {
					switch r.URL.Path {
					case "/.well-known/openid-configuration":
						return jsonHTTPResponse(http.StatusOK, map[string]string{
							"issuer":         issuerURL,
							"token_endpoint": issuerURL + "/token",
							"jwks_uri":       issuerURL + "/jwks",
						}), nil
					case "/jwks":
						return jsonHTTPResponse(http.StatusOK, map[string]any{"keys": []map[string]any{tc.jwk}}), nil
					case "/token":
						tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
							"iss": issuerURL,
							"sub": "user-1",
							"aud": "test-client-id",
							"exp": time.Now().Add(time.Hour).Unix(),
						})
						tok.Header["kid"] = "k1"
						signed, _ := tok.SignedString(privKey)
						return jsonHTTPResponse(http.StatusOK, map[string]string{"access_token": "tok", "id_token": signed}), nil
					default:
						return jsonHTTPResponse(http.StatusNotFound, map[string]string{}), nil
					}
				})
				_, err := provider.ExchangeCode(context.Background(), "code", "http://localhost/cb", "")
				if err == nil {
					t.Fatalf("expected %s to fail", tc.name)
				}
				if !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("expected error containing %q, got %v", tc.wantSub, err)
				}
			})
		}
	})

	t.Run("enforces azp claim on single and multi-audience tokens and extracts nonce", func(t *testing.T) {
		runCase := func(claims jwt.MapClaims) (*tokenhandler.OIDCClaims, error) {
			provider := newProvider(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					return jsonHTTPResponse(http.StatusOK, map[string]string{
						"issuer":         issuerURL,
						"token_endpoint": issuerURL + "/token",
						"jwks_uri":       issuerURL + "/jwks",
					}), nil
				case "/jwks":
					return jsonHTTPResponse(http.StatusOK, map[string]any{
						"keys": []map[string]any{
							{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256", "n": nStr, "e": eStr},
						},
					}), nil
				case "/token":
					claims["iss"] = issuerURL
					claims["sub"] = "user-azp"
					claims["exp"] = time.Now().Add(time.Hour).Unix()
					tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
					tok.Header["kid"] = "k1"
					signed, _ := tok.SignedString(privKey)
					return jsonHTTPResponse(http.StatusOK, map[string]string{"access_token": "tok", "id_token": signed}), nil
				default:
					return jsonHTTPResponse(http.StatusNotFound, map[string]string{}), nil
				}
			})
			return provider.ExchangeCode(context.Background(), "code", "http://localhost/cb", "")
		}

		if _, err := runCase(jwt.MapClaims{"aud": []string{"test-client-id", "other-client"}}); err == nil {
			t.Fatal("expected multi-audience token without azp to be rejected")
		}
		if _, err := runCase(jwt.MapClaims{"aud": []string{"test-client-id", "other-client"}, "azp": "other-client"}); err == nil {
			t.Fatal("expected multi-audience token with wrong azp to be rejected")
		}
		if _, err := runCase(jwt.MapClaims{"aud": "test-client-id", "azp": "other-client"}); err == nil {
			t.Fatal("expected single-audience token with wrong azp to be rejected")
		}
		parsed, err := runCase(jwt.MapClaims{
			"aud":   []string{"test-client-id", "other-client"},
			"azp":   "test-client-id",
			"nonce": "nonce-123",
		})
		if err != nil {
			t.Fatalf("expected valid multi-audience token with matching azp to succeed, got: %v", err)
		}
		if parsed.Nonce != "nonce-123" {
			t.Fatalf("expected nonce 'nonce-123', got %q", parsed.Nonce)
		}
	})

	t.Run("single-flight discovery/JWKS does not hold mutex across slow HTTP calls", func(t *testing.T) {
		var discoveryCalls atomic.Int32
		gate := make(chan struct{})
		inDiscovery := make(chan struct{}, 1)

		provider := newProvider(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/.well-known/openid-configuration" {
				if discoveryCalls.Add(1) == 1 {
					select {
					case inDiscovery <- struct{}{}:
					default:
					}
					<-gate
				}
				return jsonHTTPResponse(http.StatusOK, map[string]string{
					"issuer":                 issuerURL,
					"authorization_endpoint": issuerURL + "/auth",
					"token_endpoint":         issuerURL + "/token",
					"jwks_uri":               issuerURL + "/jwks",
				}), nil
			}
			return jsonHTTPResponse(http.StatusNotFound, map[string]string{}), nil
		})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = provider.GetAuthEndpoint(context.Background())
		}()

		<-inDiscovery

		// Second request with short deadline must fail promptly with context.DeadlineExceeded
		// instead of hanging behind a mutex held across HTTP I/O; third request shares flight once gate opens.
		shortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, shortErr := provider.GetAuthEndpoint(shortCtx)
		if shortErr == nil {
			t.Fatal("expected short-timeout waiter to return deadline exceeded while discovery is in flight")
		}

		wg.Add(1)
		var endpoint2 string
		var err2 error
		go func() {
			defer wg.Done()
			endpoint2, err2 = provider.GetAuthEndpoint(context.Background())
		}()

		time.Sleep(15 * time.Millisecond)
		close(gate)
		wg.Wait()

		if err2 != nil {
			t.Fatalf("expected coalesced request to succeed, got %v", err2)
		}
		if endpoint2 != issuerURL+"/auth" {
			t.Fatalf("unexpected endpoint %q", endpoint2)
		}
		if got := discoveryCalls.Load(); got != 1 {
			t.Fatalf("expected 1 single-flight discovery call, got %d", got)
		}
	})
}
