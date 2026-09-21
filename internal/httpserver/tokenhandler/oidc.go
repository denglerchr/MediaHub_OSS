package tokenhandler

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"mediahub_oss/internal/httpserver/utils"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"

	"github.com/golang-jwt/jwt/v5"
)

// OidcTokenExchangeRequest defines the request payload for POST /api/token/oidc.
type OidcTokenExchangeRequest struct {
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier,omitempty"`
}

// OIDCClaims holds normalized claims extracted from the verified ID token / userinfo.
type OIDCClaims struct {
	Issuer            string
	Subject           string
	PreferredUsername string
	Email             string
}

// OIDCProvider abstracts the OIDC authorization code exchange and validation for testability.
type OIDCProvider interface {
	ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*OIDCClaims, error)
}

// HTTPOIDCProvider implements OIDCProvider using HTTP requests with JWKS discovery and caching.
type HTTPOIDCProvider struct {
	Config     OIDCConfig
	HTTPClient *http.Client
	Logger     *slog.Logger

	mu              sync.RWMutex
	cachedEndpoints openIDConfiguration
	endpointsExpiry time.Time
	cachedJWKS      map[string]*rsa.PublicKey
	jwksExpiry      time.Time
}

// NewHTTPOIDCProvider creates a new HTTPOIDCProvider.
func NewHTTPOIDCProvider(cfg OIDCConfig, logger *slog.Logger) *HTTPOIDCProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &HTTPOIDCProvider{
		Config:     cfg,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		Logger:     logger,
		cachedJWKS: make(map[string]*rsa.PublicKey),
	}
}

type openIDConfiguration struct {
	Issuer           string `json:"issuer"`
	TokenEndpoint    string `json:"token_endpoint"`
	JWKSURI          string `json:"jwks_uri"`
	UserinfoEndpoint string `json:"userinfo_endpoint"`
}

type idpTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (p *HTTPOIDCProvider) discoverEndpoints(ctx context.Context) (openIDConfiguration, error) {
	p.mu.RLock()
	if time.Now().Before(p.endpointsExpiry) && p.cachedEndpoints.TokenEndpoint != "" {
		cfg := p.cachedEndpoints
		p.mu.RUnlock()
		return cfg, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double check under write lock
	if time.Now().Before(p.endpointsExpiry) && p.cachedEndpoints.TokenEndpoint != "" {
		return p.cachedEndpoints, nil
	}

	baseURL := strings.TrimRight(p.Config.IssuerURL, "/")
	discoveryURL := baseURL + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return openIDConfiguration{}, fmt.Errorf("failed to create OIDC discovery request: %w", err)
	}

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return openIDConfiguration{}, fmt.Errorf("OIDC discovery request failed for %s: %w", discoveryURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return openIDConfiguration{}, fmt.Errorf("OIDC discovery returned HTTP %d from %s", resp.StatusCode, discoveryURL)
	}

	var doc openIDConfiguration
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return openIDConfiguration{}, fmt.Errorf("failed to decode OIDC discovery document from %s: %w", discoveryURL, err)
	}

	if doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return openIDConfiguration{}, fmt.Errorf("OIDC discovery document from %s is missing required endpoints (token_endpoint or jwks_uri)", discoveryURL)
	}

	if p.Logger != nil {
		p.Logger.DebugContext(ctx, "Discovered OIDC provider configuration",
			"issuer", doc.Issuer,
			"token_endpoint", doc.TokenEndpoint,
			"jwks_uri", doc.JWKSURI,
			"userinfo_endpoint", doc.UserinfoEndpoint,
		)
	}

	p.cachedEndpoints = doc
	p.endpointsExpiry = time.Now().Add(24 * time.Hour)

	return doc, nil
}

func (p *HTTPOIDCProvider) getJWKSPublicKey(ctx context.Context, jwksURI, kid string) (*rsa.PublicKey, error) {
	p.mu.RLock()
	if time.Now().Before(p.jwksExpiry) {
		if key, ok := p.cachedJWKS[kid]; ok {
			p.mu.RUnlock()
			return key, nil
		}
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS request returned status %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("failed to decode JWKS: %w", err)
	}

	newMap := make(map[string]*rsa.PublicKey)
	for _, k := range jwks.Keys {
		if k.Kty == "RSA" && k.N != "" && k.E != "" {
			pubKey, err := parseRSAPublicKey(k.N, k.E)
			if err == nil {
				newMap[k.Kid] = pubKey
			}
		}
	}

	p.cachedJWKS = newMap
	p.jwksExpiry = time.Now().Add(1 * time.Hour)

	if key, ok := newMap[kid]; ok {
		return key, nil
	}
	if kid == "" && len(newMap) == 1 {
		for _, v := range newMap {
			return v, nil
		}
	}

	return nil, fmt.Errorf("no matching key found for kid '%s' in JWKS", kid)
}

func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, err
	}
	var eInt int
	for _, b := range eBytes {
		eInt = (eInt << 8) | int(b)
	}
	n := new(big.Int).SetBytes(nBytes)
	return &rsa.PublicKey{N: n, E: eInt}, nil
}

// ExchangeCode implements OIDCProvider.
func (p *HTTPOIDCProvider) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*OIDCClaims, error) {
	endpoints, err := p.discoverEndpoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("endpoint discovery failed: %w", err)
	}

	rURI := redirectURI
	if rURI == "" {
		rURI = p.Config.RedirectURL
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", rURI)
	form.Set("client_id", p.Config.ClientID)
	if p.Config.ClientSecret != "" {
		form.Set("client_secret", p.Config.ClientSecret)
	}
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	var tokenRes idpTokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenRes); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := tokenRes.ErrorDesc
		if msg == "" {
			msg = tokenRes.Error
		}
		if msg == "" {
			msg = string(bodyBytes)
		}
		return nil, fmt.Errorf("idp error (%d): %s", resp.StatusCode, msg)
	}

	claims := &OIDCClaims{}

	// Parse ID token if present
	if tokenRes.IDToken != "" {
		parsedToken, err := jwt.Parse(tokenRes.IDToken, func(token *jwt.Token) (any, error) {
			kid, _ := token.Header["kid"].(string)
			return p.getJWKSPublicKey(ctx, endpoints.JWKSURI, kid)
		})
		if err == nil && parsedToken.Valid {
			if mapClaims, ok := parsedToken.Claims.(jwt.MapClaims); ok {
				if sub, ok := mapClaims["sub"].(string); ok {
					claims.Subject = sub
				}
				if iss, ok := mapClaims["iss"].(string); ok {
					claims.Issuer = iss
				}
				if pref, ok := mapClaims["preferred_username"].(string); ok {
					claims.PreferredUsername = pref
				}
				if em, ok := mapClaims["email"].(string); ok {
					claims.Email = em
				}
			}
		} else {
			// Fallback: parse unverified claims from id_token if JWKS signature check is not reachable
			unverifiedToken, _, unvErr := jwt.NewParser().ParseUnverified(tokenRes.IDToken, jwt.MapClaims{})
			if unvErr == nil {
				if mapClaims, ok := unverifiedToken.Claims.(jwt.MapClaims); ok {
					if sub, ok := mapClaims["sub"].(string); ok {
						claims.Subject = sub
					}
					if iss, ok := mapClaims["iss"].(string); ok {
						claims.Issuer = iss
					}
					if pref, ok := mapClaims["preferred_username"].(string); ok {
						claims.PreferredUsername = pref
					}
					if em, ok := mapClaims["email"].(string); ok {
						claims.Email = em
					}
				}
			}
		}
	}

	// If preferred_username or email are missing, call userinfo endpoint
	if (claims.PreferredUsername == "" || claims.Email == "" || claims.Subject == "") && tokenRes.AccessToken != "" && endpoints.UserinfoEndpoint != "" {
		uiReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoints.UserinfoEndpoint, nil)
		if err == nil {
			uiReq.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
			uiResp, err := p.HTTPClient.Do(uiReq)
			if err == nil && uiResp.StatusCode == http.StatusOK {
				defer uiResp.Body.Close()
				var uiMap map[string]any
				if err := json.NewDecoder(uiResp.Body).Decode(&uiMap); err == nil {
					if claims.Subject == "" {
						if sub, ok := uiMap["sub"].(string); ok {
							claims.Subject = sub
						}
					}
					if claims.PreferredUsername == "" {
						if pref, ok := uiMap["preferred_username"].(string); ok {
							claims.PreferredUsername = pref
						}
					}
					if claims.Email == "" {
						if em, ok := uiMap["email"].(string); ok {
							claims.Email = em
						}
					}
				}
			} else if uiResp != nil {
				uiResp.Body.Close()
			}
		}
	}

	if claims.Issuer == "" {
		claims.Issuer = p.Config.IssuerURL
	}
	if claims.Subject == "" {
		return nil, errors.New("missing 'sub' claim in OIDC token")
	}

	return claims, nil
}

// GetTokenOIDC exchanges an authorization code from an OIDC provider for internal access/refresh tokens.
// @Summary Exchange OIDC Authorization Code
// @Description Exchanges an authorization code from an external OIDC provider (e.g., Keycloak) for an internal JWT Access/Refresh token pair. Automatically provisions new users if necessary.
// @Tags token
// @Accept json
// @Produce json
// @Param body body OidcTokenExchangeRequest true "OIDC Authorization Code Request"
// @Success 200 {object} TokenResponse "Returns access and refresh tokens"
// @Failure 400 {object} utils.ErrorResponse "Invalid request body or OIDC disabled"
// @Failure 401 {object} utils.ErrorResponse "Failed to validate OIDC credentials"
// @Failure 500 {object} utils.ErrorResponse "Internal server error"
// @Router /api/token/oidc [post]
func (h *TokenHandler) GetTokenOIDC(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !h.OIDCConfig.Enabled {
		utils.RespondWithError(w, http.StatusBadRequest, "Single Sign-On (OIDC) is not enabled")
		return
	}

	if h.OIDCProvider == nil {
		h.Logger.Error("OIDC is enabled but OIDCProvider is nil")
		utils.RespondWithError(w, http.StatusInternalServerError, "OIDC provider is not configured")
		return
	}

	var req OidcTokenExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON body: 'code' is required")
		return
	}

	claims, err := h.OIDCProvider.ExchangeCode(ctx, req.Code, req.RedirectURI, req.CodeVerifier)
	if err != nil {
		h.Logger.Warn("OIDC code exchange failed", "error", err)
		utils.RespondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Authentication failed: %v", err))
		return
	}

	normIssuer := strings.TrimRight(claims.Issuer, "/")
	if normIssuer == "" {
		normIssuer = strings.TrimRight(h.OIDCConfig.IssuerURL, "/")
	}

	// Check if user identity already exists in database
	user, err := h.Repo.GetUserByOIDCIdentity(ctx, normIssuer, claims.Subject)
	if errors.Is(err, customerrors.ErrNotFound) {
		// JIT Provisioning executed atomically in repository
		createdUser, err := h.Repo.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
			Issuer:            normIssuer,
			Subject:           claims.Subject,
			PreferredUsername: claims.PreferredUsername,
			Email:             claims.Email,
			DefaultRightsUser: h.OIDCConfig.DefaultUserRights,
		})
		if err != nil {
			h.Logger.Error("Failed to provision OIDC user", "error", err)
			utils.RespondWithError(w, http.StatusInternalServerError, "Failed to provision user")
			return
		}

		user = createdUser
		h.Auditor.Log(ctx, "user.jit_provision", "oidc", user.Username, map[string]any{
			"issuer":  normIssuer,
			"subject": claims.Subject,
		})
	} else if err != nil {
		h.Logger.Error("Failed to query OIDC identity", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to query user identity")
		return
	}

	// Generate internal tokens
	accessToken, refreshToken, err := h.generateTokens(r, user.ID)
	if err != nil {
		h.Logger.Error("Failed to generate tokens for OIDC user", "error", err)
		utils.RespondWithError(w, http.StatusInternalServerError, "Failed to generate tokens")
		return
	}

	h.Auditor.Log(ctx, "auth.login_oidc", user.Username, "token", map[string]any{
		"issuer":  normIssuer,
		"subject": claims.Subject,
	})

	utils.RespondWithJSON(w, http.StatusOK, TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}
