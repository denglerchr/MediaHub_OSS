package infohandler

import (
	"context"
	"log/slog"
	"time"

	"mediahub_oss/internal/logging/audit"
)

// AuthEndpointProvider abstracts fetching the authorization endpoint for OIDC.
type AuthEndpointProvider interface {
	GetAuthEndpoint(ctx context.Context) (string, error)
}

// OIDCConfig represents the nested OIDC settings in the InfoResponse.
type OIDCConfig struct {
	Enabled           bool   `json:"enabled"`
	LoginPageDisabled bool   `json:"login_page_disabled"`
	IssuerURL         string `json:"oidc_issuer_url"`
	ClientID          string `json:"oidc_client_id"`
	RedirectURL       string `json:"oidc_redirect_url"`
	AuthEndpoint      string `json:"oidc_auth_endpoint,omitempty"` //dynamically filled
}

// FeaturesConfig represents the nested features settings in the InfoResponse.
type FeaturesConfig struct {
	AuditLogs bool `json:"audit_logs"`
}

type InfoHandler struct {
	Logger       *slog.Logger
	Auditor      audit.AuditLogger
	Version      string
	StartTime    time.Time
	ConversionTo map[string][]string
	OIDC         OIDCConfig
	OIDCProvider AuthEndpointProvider
	Features     FeaturesConfig
}

// InfoResponse defines the JSON structure for the /api/info endpoint.
type InfoResponse struct {
	ServiceName  string              `json:"service_name"`
	Version      string              `json:"version"`
	Uptime       string              `json:"uptime"` // Changed to reflect elapsed duration
	ConversionTo map[string][]string `json:"conversion_to"`
	OIDC         OIDCConfig          `json:"oidc"`
	Features     FeaturesConfig      `json:"features"`
}
