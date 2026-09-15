package tokenhandler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"mediahub_oss/internal/httpserver/tokenhandler"
)

func TestHTTPOIDCProvider_DiscoverEndpoints_SuccessAndCache(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			atomic.AddInt32(&requestCount, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":            "https://test-idp.example.com",
				"token_endpoint":    "https://test-idp.example.com/oauth/token",
				"jwks_uri":          "https://test-idp.example.com/oauth/jwks",
				"userinfo_endpoint": "https://test-idp.example.com/oauth/userinfo",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: server.URL,
		ClientID:  "test-client",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	ctx := context.Background()

	// First call should fetch from server
	claims, err := provider.ExchangeCode(ctx, "invalid_code", "http://localhost/callback", "")
	// ExchangeCode calls discoverEndpoints first; it will proceed to POST to token_endpoint which will fail,
	// but discovery itself succeeded.
	if err == nil {
		t.Fatalf("expected error from exchange against test-idp.example.com, got claims: %v", claims)
	}
	// Verify that the discovery endpoint was requested once
	if count := atomic.LoadInt32(&requestCount); count != 1 {
		t.Fatalf("expected 1 discovery request, got %d", count)
	}

	// Second call should use cached discovery, so requestCount remains 1
	_, _ = provider.ExchangeCode(ctx, "invalid_code", "http://localhost/callback", "")
	if count := atomic.LoadInt32(&requestCount); count != 1 {
		t.Fatalf("expected cached discovery (count=1), got %d", count)
	}
}

func TestHTTPOIDCProvider_DiscoverEndpoints_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: server.URL,
		ClientID:  "test-client",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	_, err := provider.ExchangeCode(context.Background(), "code", "http://localhost/callback", "")
	if err == nil {
		t.Fatal("expected error on discovery failure, got nil")
	}
}

func TestHTTPOIDCProvider_DiscoverEndpoints_MissingEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": "https://test-idp.example.com",
			// Missing token_endpoint and jwks_uri
		})
	}))
	defer server.Close()

	cfg := tokenhandler.OIDCConfig{
		Enabled:   true,
		IssuerURL: server.URL,
		ClientID:  "test-client",
	}
	provider := tokenhandler.NewHTTPOIDCProvider(cfg, nil)

	_, err := provider.ExchangeCode(context.Background(), "code", "http://localhost/callback", "")
	if err == nil {
		t.Fatal("expected error when discovery document is missing endpoints, got nil")
	}
}
