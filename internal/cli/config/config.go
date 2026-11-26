package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mediahub_oss/internal/shared/customerrors"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/viper"
)

// LoadConfig leverages Viper to merge TOML, Env Variables, and CLI flags natively.
func LoadConfig(path string) (*Config, error) {
	// 1. Tell Viper where to find the TOML file
	viper.SetConfigFile(path)

	// 2. Configure Environment Variable automation
	// This replaces your manual bindEnvVars logic!
	viper.SetEnvPrefix("MEDIAHUB")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	// Explicitly bind environment variable aliases where concept doc names differ from struct mapstructure keys
	_ = viper.BindEnv("server.max_sync_upload_size", "MEDIAHUB_SERVER_MAX_SYNC_UPLOAD", "MEDIAHUB_SERVER_MAX_SYNC_UPLOAD_SIZE")
	_ = viper.BindEnv("server.cors_allowed_origins", "MEDIAHUB_SERVER_CORS_ORIGINS", "MEDIAHUB_SERVER_CORS_ALLOWED_ORIGINS")
	_ = viper.BindEnv("auth.oidc.disable_login_page", "MEDIAHUB_AUTH_OIDC_DISABLE_LOCAL_LOGIN", "MEDIAHUB_AUTH_OIDC_DISABLE_LOGIN_PAGE")

	// 3. Read the TOML file
	if err := viper.ReadInConfig(); err != nil {
		// If the file simply isn't there, that's fine. But if it's malformed, we should return an error.
		var configFileNotFound viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFound) && !os.IsNotExist(err) {
			return nil, fmt.Errorf("error parsing config file: %w", err)
		}
	}

	// 4. Unmarshal the merged state (TOML + Env + Flags) directly into your struct
	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate settings
	if err := config.ValidateConfig(); err != nil {
		return nil, err
	}

	return &config, nil
}

// EnsureJWTSecret ensures a JWT signing secret is configured before starting the HTTP server.
// PostgreSQL deployments require an explicit secret to prevent multi-instance split-brain;
// SQLite deployments auto-generate and persist a secret to the config file when omitted.
func EnsureJWTSecret(path string, cfg *Config, logger *slog.Logger) error {
	if cfg == nil {
		return fmt.Errorf("config must not be nil")
	}
	if strings.TrimSpace(cfg.Auth.JWT.Secret) != "" {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Database.Driver), "postgres") {
		return fmt.Errorf("invalid configuration: auth.jwt.secret must be explicitly configured (e.g. via MEDIAHUB_AUTH_JWT_SECRET) when using the postgres driver to prevent multi-instance JWT split-brain")
	}

	secret, err := generateRandomSecret(32)
	if err != nil {
		return fmt.Errorf("failed to generate random JWT secret: %w", err)
	}
	cfg.Auth.JWT.Secret = secret
	if strings.TrimSpace(cfg.Auth.JWT.AccessDuration) == "" {
		cfg.Auth.JWT.AccessDuration = "5min"
	}
	if strings.TrimSpace(cfg.Auth.JWT.RefreshDuration) == "" {
		cfg.Auth.JWT.RefreshDuration = "24h"
	}

	if path != "" {
		if logger == nil {
			logger = slog.Default()
		}
		if err := persistJWTSecret(path, cfg.Auth.JWT); err != nil {
			logger.Warn("Failed to persist auto-generated JWT secret to config file; an ephemeral in-memory secret will be used and existing tokens will be invalidated on restart", "path", path, "error", err)
			return nil
		}
		logger.Info("Auto-generated and persisted JWT secret to config file", "path", path)
	}

	return nil
}

func persistJWTSecret(path string, jwtCfg jwtConfigInternal) error {
	var raw map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
			return fmt.Errorf("failed to decode existing config file: %w", err)
		}
	}
	if raw == nil {
		raw = make(map[string]any)
	}

	var authMap map[string]any
	if existingAuth, ok := raw["auth"]; ok && existingAuth != nil {
		castAuth, ok := existingAuth.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected non-table [auth] section in config file %s", path)
		}
		authMap = castAuth
	} else {
		authMap = make(map[string]any)
		raw["auth"] = authMap
	}

	var jwtMap map[string]any
	if existingJWT, ok := authMap["jwt"]; ok && existingJWT != nil {
		castJWT, ok := existingJWT.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected non-table [auth.jwt] section in config file %s", path)
		}
		jwtMap = castJWT
	} else {
		jwtMap = make(map[string]any)
		authMap["jwt"] = jwtMap
	}

	jwtMap["secret"] = jwtCfg.Secret
	if isMissingOrEmptyTOMLString(jwtMap, "access_duration") && jwtCfg.AccessDuration != "" {
		jwtMap["access_duration"] = jwtCfg.AccessDuration
	}
	if isMissingOrEmptyTOMLString(jwtMap, "refresh_duration") && jwtCfg.RefreshDuration != "" {
		jwtMap["refresh_duration"] = jwtCfg.RefreshDuration
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary config file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set permissions on temporary config file: %w", err)
	}
	if err := toml.NewEncoder(tmp).Encode(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to encode config to temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temporary config file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if openErr != nil {
			return fmt.Errorf("failed to replace config file: rename=%w; fallback open=%v", err, openErr)
		}
		_ = f.Chmod(0600)
		encErr := toml.NewEncoder(f).Encode(raw)
		closeErr := f.Close()
		if encErr != nil {
			return fmt.Errorf("failed to encode config on fallback write: %w", encErr)
		}
		if closeErr != nil {
			return fmt.Errorf("failed to close config file on fallback write: %w", closeErr)
		}
		return nil
	}

	removeTmp = false
	return nil
}

func isMissingOrEmptyTOMLString(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// generateRandomSecret generates a cryptographically secure hex-encoded random string.
func generateRandomSecret(numBytes int) (string, error) {
	bytes := make([]byte, numBytes)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ValidateConfig ensures the application doesn't start in an inaccessible state.
func (cfg *Config) ValidateConfig() error {
	// If login page is disabled, but OIDC is also disabled, the user is locked out.
	if cfg.Auth.OIDC.DisableLoginPage && !cfg.Auth.OIDC.Enabled {
		return fmt.Errorf("invalid configuration: login page is disabled but OIDC is not enabled. You must enable at least one authentication method")
	}

	if cfg.Storage.Type == "s3" {
		if cfg.Storage.S3.Endpoint == "" {
			return fmt.Errorf("invalid configuration: storage.s3.endpoint must be specified when storage.type is 's3'")
		}
		if cfg.Storage.S3.Bucket == "" {
			return fmt.Errorf("invalid configuration: storage.s3.bucket must be specified when storage.type is 's3'")
		}
		if cfg.Storage.S3.AccessKey == "" {
			return fmt.Errorf("invalid configuration: storage.s3.access_key must be specified when storage.type is 's3'")
		}
		if cfg.Storage.S3.SecretKey == "" {
			return fmt.Errorf("invalid configuration: storage.s3.secret_key must be specified when storage.type is 's3'")
		}
	} else if cfg.Storage.Type == "local" {
		if cfg.Storage.Local.Root == "" {
			return fmt.Errorf("invalid configuration: storage.local.root must be specified when storage.type is 'local'")
		}
	} else if cfg.Storage.Type != "" {
		return fmt.Errorf("invalid configuration: unsupported storage type '%s', must be 'local' or 's3'", cfg.Storage.Type)
	}

	return nil
}

// SaveConfig writes the current configuration back to a TOML file.
func SaveConfig(path string, cfg *Config) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("trying to save the config: %w", customerrors.ErrorCreateFile)
	}
	defer f.Close()
	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("trying to save the config: %w", customerrors.ErrorEncodeFile)
	}
	return nil
}
