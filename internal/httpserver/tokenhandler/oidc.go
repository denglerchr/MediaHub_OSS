package tokenhandler

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
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

const maxOIDCResponseBytes int64 = 1 << 20

// OidcTokenExchangeRequest defines the request payload for POST /api/token/oidc.
type OidcTokenExchangeRequest struct {
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier,omitempty"`
	Nonce        string `json:"nonce,omitempty"`
}

// OIDCClaims holds normalized claims extracted from the verified ID token / userinfo.
type OIDCClaims struct {
	Issuer            string
	Subject           string
	PreferredUsername string
	Email             string
	Nonce             string
}

// OIDCProvider abstracts the OIDC authorization code exchange and validation for testability.
type OIDCProvider interface {
	ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*OIDCClaims, error)
	GetAuthEndpoint(ctx context.Context) (string, error)
}

type jwkEntry struct {
	key crypto.PublicKey
	alg string
}

// HTTPOIDCProvider implements OIDCProvider using HTTP requests with JWKS discovery and caching.
type HTTPOIDCProvider struct {
	Config     OIDCConfig
	HTTPClient *http.Client
	Logger     *slog.Logger

	mu              sync.RWMutex
	cachedEndpoints openIDConfiguration
	endpointsExpiry time.Time
	endpointsFlight chan struct{}
	cachedJWKS      map[string]jwkEntry
	jwksExpiry      time.Time
	jwksFlight      chan struct{}
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
		cachedJWKS: make(map[string]jwkEntry),
	}
}

type openIDConfiguration struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
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
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// GetAuthEndpoint returns the discovered authorization endpoint URL.
func (p *HTTPOIDCProvider) GetAuthEndpoint(ctx context.Context) (string, error) {
	endpoints, err := p.discoverEndpoints(ctx)
	if err != nil {
		return "", err
	}
	return endpoints.AuthorizationEndpoint, nil
}

func (p *HTTPOIDCProvider) discoverEndpoints(ctx context.Context) (openIDConfiguration, error) {
	for {
		p.mu.RLock()
		if time.Now().Before(p.endpointsExpiry) && p.cachedEndpoints.TokenEndpoint != "" {
			cfg := p.cachedEndpoints
			p.mu.RUnlock()
			return cfg, nil
		}
		waitCh := p.endpointsFlight
		p.mu.RUnlock()

		if waitCh != nil {
			select {
			case <-waitCh:
				continue
			case <-ctx.Done():
				return openIDConfiguration{}, ctx.Err()
			}
		}

		p.mu.Lock()
		if time.Now().Before(p.endpointsExpiry) && p.cachedEndpoints.TokenEndpoint != "" {
			cfg := p.cachedEndpoints
			p.mu.Unlock()
			return cfg, nil
		}
		if p.endpointsFlight != nil {
			waitCh = p.endpointsFlight
			p.mu.Unlock()
			select {
			case <-waitCh:
				continue
			case <-ctx.Done():
				return openIDConfiguration{}, ctx.Err()
			}
		}
		flight := make(chan struct{})
		p.endpointsFlight = flight
		p.mu.Unlock()

		var doc openIDConfiguration
		var fetchErr error
		var succeeded bool
		func() {
			defer func() {
				p.mu.Lock()
				if succeeded {
					p.cachedEndpoints = doc
					p.endpointsExpiry = time.Now().Add(24 * time.Hour)
				}
				p.endpointsFlight = nil
				close(flight)
				p.mu.Unlock()
			}()
			doc, fetchErr = p.fetchDiscoveryEndpoints(ctx)
			succeeded = fetchErr == nil
		}()

		if fetchErr != nil {
			return openIDConfiguration{}, fetchErr
		}
		return doc, nil
	}
}

func (p *HTTPOIDCProvider) fetchDiscoveryEndpoints(ctx context.Context) (openIDConfiguration, error) {
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOIDCResponseBytes)).Decode(&doc); err != nil {
		return openIDConfiguration{}, fmt.Errorf("failed to decode OIDC discovery document from %s: %w", discoveryURL, err)
	}

	if doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return openIDConfiguration{}, fmt.Errorf("OIDC discovery document from %s is missing required endpoints (token_endpoint or jwks_uri)", discoveryURL)
	}

	if p.Logger != nil {
		p.Logger.DebugContext(ctx, "Discovered OIDC provider configuration",
			"issuer", doc.Issuer,
			"authorization_endpoint", doc.AuthorizationEndpoint,
			"token_endpoint", doc.TokenEndpoint,
			"jwks_uri", doc.JWKSURI,
			"userinfo_endpoint", doc.UserinfoEndpoint,
		)
	}

	return doc, nil
}

func lookupJWKEntry(jwks map[string]jwkEntry, kid string) (jwkEntry, bool) {
	if entry, ok := jwks[kid]; ok {
		return entry, true
	}
	if kid == "" && len(jwks) == 1 {
		for _, v := range jwks {
			return v, true
		}
	}
	return jwkEntry{}, false
}

func (p *HTTPOIDCProvider) getJWKSEntry(ctx context.Context, jwksURI, kid string) (jwkEntry, error) {
	var waitedForRefresh bool
	for {
		p.mu.RLock()
		cacheValid := time.Now().Before(p.jwksExpiry)
		if cacheValid {
			if entry, ok := lookupJWKEntry(p.cachedJWKS, kid); ok {
				p.mu.RUnlock()
				return entry, nil
			}
		}
		if waitedForRefresh && cacheValid {
			p.mu.RUnlock()
			return jwkEntry{}, fmt.Errorf("no matching key found for kid '%s' in JWKS", kid)
		}
		waitCh := p.jwksFlight
		p.mu.RUnlock()

		if waitCh != nil {
			select {
			case <-waitCh:
				waitedForRefresh = true
				continue
			case <-ctx.Done():
				return jwkEntry{}, ctx.Err()
			}
		}

		p.mu.Lock()
		cacheValid = time.Now().Before(p.jwksExpiry)
		if cacheValid {
			if entry, ok := lookupJWKEntry(p.cachedJWKS, kid); ok {
				p.mu.Unlock()
				return entry, nil
			}
		}
		if waitedForRefresh && cacheValid {
			p.mu.Unlock()
			return jwkEntry{}, fmt.Errorf("no matching key found for kid '%s' in JWKS", kid)
		}
		if p.jwksFlight != nil {
			waitCh = p.jwksFlight
			p.mu.Unlock()
			select {
			case <-waitCh:
				waitedForRefresh = true
				continue
			case <-ctx.Done():
				return jwkEntry{}, ctx.Err()
			}
		}
		flight := make(chan struct{})
		p.jwksFlight = flight
		p.mu.Unlock()

		var newMap map[string]jwkEntry
		var fetchErr error
		var succeeded bool
		func() {
			defer func() {
				p.mu.Lock()
				if succeeded {
					p.cachedJWKS = newMap
					p.jwksExpiry = time.Now().Add(1 * time.Hour)
				}
				p.jwksFlight = nil
				close(flight)
				p.mu.Unlock()
			}()
			newMap, fetchErr = p.fetchJWKS(ctx, jwksURI)
			succeeded = fetchErr == nil
		}()

		if fetchErr != nil {
			return jwkEntry{}, fetchErr
		}
		if entry, ok := lookupJWKEntry(newMap, kid); ok {
			return entry, nil
		}
		return jwkEntry{}, fmt.Errorf("no matching key found for kid '%s' in JWKS", kid)
	}
}

func (p *HTTPOIDCProvider) fetchJWKS(ctx context.Context, jwksURI string) (map[string]jwkEntry, error) {
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOIDCResponseBytes)).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("failed to decode JWKS: %w", err)
	}

	newMap := make(map[string]jwkEntry)
	for _, k := range jwks.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			if k.N != "" && k.E != "" {
				pubKey, err := parseRSAPublicKey(k.N, k.E)
				if err == nil {
					newMap[k.Kid] = jwkEntry{key: pubKey, alg: k.Alg}
				}
			}
		case "EC":
			if k.Crv != "" && k.X != "" && k.Y != "" {
				pubKey, err := parseECPublicKey(k.Crv, k.X, k.Y)
				if err == nil {
					newMap[k.Kid] = jwkEntry{key: pubKey, alg: k.Alg}
				}
			}
		}
	}

	return newMap, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := decodeBase64URL(nStr)
	if err != nil {
		return nil, err
	}
	eBytes, err := decodeBase64URL(eStr)
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

func parseECPublicKey(crvStr, xStr, yStr string) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch crvStr {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported EC curve '%s'", crvStr)
	}

	xBytes, err := decodeBase64URL(xStr)
	if err != nil {
		return nil, fmt.Errorf("invalid x coordinate in EC key: %w", err)
	}
	yBytes, err := decodeBase64URL(yStr)
	if err != nil {
		return nil, fmt.Errorf("invalid y coordinate in EC key: %w", err)
	}

	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)

	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
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

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxOIDCResponseBytes))
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

	// Per OIDC Core 1.0 section 3.1.3.3, code exchange MUST return an id_token
	if tokenRes.IDToken == "" {
		return nil, errors.New("missing id_token in token response")
	}

	claims, err := p.verifyIDToken(ctx, tokenRes.IDToken, endpoints)
	if err != nil {
		return nil, err
	}

	// If preferred_username or email are missing, call userinfo endpoint
	if (claims.PreferredUsername == "" || claims.Email == "") && tokenRes.AccessToken != "" && endpoints.UserinfoEndpoint != "" {
		if err := p.enrichClaimsFromUserinfo(ctx, tokenRes.AccessToken, endpoints.UserinfoEndpoint, claims); err != nil {
			return nil, err
		}
	}

	return claims, nil
}

var allowedOIDCSigningMethods = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"PS256", "PS384", "PS512",
}

func countAudienceEntries(aud any) int {
	switch v := aud.(type) {
	case string:
		if v == "" {
			return 0
		}
		return 1
	case []string:
		return len(v)
	case []any:
		return len(v)
	default:
		return 0
	}
}

func (p *HTTPOIDCProvider) verifyIDToken(ctx context.Context, idToken string, endpoints openIDConfiguration) (*OIDCClaims, error) {
	parsedToken, err := jwt.Parse(idToken, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		entry, err := p.getJWKSEntry(ctx, endpoints.JWKSURI, kid)
		if err != nil {
			return nil, err
		}
		alg, _ := token.Header["alg"].(string)
		if entry.alg != "" && alg != entry.alg {
			return nil, fmt.Errorf("id_token alg '%s' does not match JWK alg '%s'", alg, entry.alg)
		}
		return entry.key, nil
	},
		jwt.WithValidMethods(allowedOIDCSigningMethods),
		jwt.WithAudience(p.Config.ClientID),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !parsedToken.Valid {
		return nil, fmt.Errorf("invalid id_token signature or claims: %w", err)
	}

	mapClaims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("unexpected id_token claims type")
	}

	audCount := countAudienceEntries(mapClaims["aud"])
	if azpRaw, hasAzp := mapClaims["azp"]; hasAzp {
		azp, ok := azpRaw.(string)
		if !ok || azp != p.Config.ClientID {
			return nil, errors.New("invalid 'azp' claim in id_token")
		}
	} else if audCount > 1 {
		return nil, errors.New("missing required 'azp' claim for multi-audience id_token")
	}

	iss, _ := mapClaims["iss"].(string)
	expectedIssuer := strings.TrimRight(endpoints.Issuer, "/")
	tokenIssuer := strings.TrimRight(iss, "/")
	configIssuer := strings.TrimRight(p.Config.IssuerURL, "/")
	if tokenIssuer == "" || (tokenIssuer != expectedIssuer && tokenIssuer != configIssuer) {
		return nil, fmt.Errorf("invalid id_token issuer '%s': expected '%s'", iss, endpoints.Issuer)
	}

	sub, _ := mapClaims["sub"].(string)
	if sub == "" {
		return nil, errors.New("missing 'sub' claim in id_token")
	}

	claims := &OIDCClaims{
		Issuer:  iss,
		Subject: sub,
	}
	if pref, ok := mapClaims["preferred_username"].(string); ok {
		claims.PreferredUsername = pref
	}
	if em, ok := mapClaims["email"].(string); ok {
		claims.Email = em
	}
	if nonce, ok := mapClaims["nonce"].(string); ok {
		claims.Nonce = nonce
	}
	return claims, nil
}

func (p *HTTPOIDCProvider) enrichClaimsFromUserinfo(ctx context.Context, accessToken string, userinfoURL string, claims *OIDCClaims) error {
	uiReq, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return nil
	}
	uiReq.Header.Set("Authorization", "Bearer "+accessToken)
	uiResp, err := p.HTTPClient.Do(uiReq)
	if err != nil {
		return nil
	}
	defer uiResp.Body.Close()

	if uiResp.StatusCode != http.StatusOK {
		return nil
	}

	var uiMap map[string]any
	if err := json.NewDecoder(io.LimitReader(uiResp.Body, maxOIDCResponseBytes)).Decode(&uiMap); err != nil {
		return nil
	}

	uiSub, _ := uiMap["sub"].(string)
	if uiSub == "" || uiSub != claims.Subject {
		return fmt.Errorf("userinfo sub claim '%s' does not match id_token subject '%s'", uiSub, claims.Subject)
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
	return nil
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
// @Router /token/oidc [post]
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
	if status, msg := utils.DecodeJSONBody(w, r, &req); status != 0 {
		utils.RespondWithError(w, status, msg)
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "Invalid JSON body: 'code' is required")
		return
	}

	claims, err := h.OIDCProvider.ExchangeCode(ctx, req.Code, req.RedirectURI, req.CodeVerifier)
	if err != nil {
		h.Logger.Warn("OIDC code exchange failed", "error", err)
		utils.RespondWithError(w, http.StatusUnauthorized, "OIDC authentication failed")
		return
	}

	if expectedNonce := strings.TrimSpace(req.Nonce); expectedNonce != "" && claims.Nonce != expectedNonce {
		h.Logger.Warn("OIDC nonce mismatch", "has_nonce", claims.Nonce != "")
		utils.RespondWithError(w, http.StatusUnauthorized, "OIDC authentication failed")
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
		createdUser, createErr := h.Repo.CreateOIDCUser(ctx, repo.CreateOIDCUserParams{
			Issuer:            normIssuer,
			Subject:           claims.Subject,
			PreferredUsername: claims.PreferredUsername,
			Email:             claims.Email,
			DefaultRightsUser: h.OIDCConfig.DefaultUserRights,
		})
		if createErr != nil {
			if errors.Is(createErr, customerrors.ErrConflict) || errors.Is(createErr, customerrors.ErrUserExists) {
				existingUser, lookupErr := h.Repo.GetUserByOIDCIdentity(ctx, normIssuer, claims.Subject)
				if lookupErr == nil {
					user = existingUser
				} else {
					h.Logger.Error("Failed to provision OIDC user after conflict fallback", "error", createErr, "lookup_error", lookupErr)
					utils.RespondWithError(w, http.StatusInternalServerError, "Failed to provision user")
					return
				}
			} else {
				h.Logger.Error("Failed to provision OIDC user", "error", createErr)
				utils.RespondWithError(w, http.StatusInternalServerError, "Failed to provision user")
				return
			}
		} else {
			user = createdUser
			h.Auditor.Log(ctx, "user.jit_provision", "oidc", user.Username, map[string]any{
				"issuer":  normIssuer,
				"subject": claims.Subject,
			})
		}
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
