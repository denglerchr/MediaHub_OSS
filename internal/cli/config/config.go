package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mediahub_oss/internal/shared/customerrors"
	"os"
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

	// 5. If JWT Secret is omitted, generate a cryptographically secure random secret and persist it
	if strings.TrimSpace(config.Auth.JWT.Secret) == "" {
		secret, err := generateRandomSecret(32)
		if err != nil {
			return nil, fmt.Errorf("failed to generate random JWT secret: %w", err)
		}
		config.Auth.JWT.Secret = secret
		if config.Auth.JWT.AccessDuration == "" {
			config.Auth.JWT.AccessDuration = "5min"
		}
		if config.Auth.JWT.RefreshDuration == "" {
			config.Auth.JWT.RefreshDuration = "24h"
		}

		if path != "" {
			if err := SaveConfig(path, &config); err != nil {
				return nil, fmt.Errorf("failed to save generated JWT secret: %w", err)
			}
			slog.Info("Auto-generated and persisted JWT secret to config file", "path", path)
		}
	}

	return &config, nil
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
// Used to persist the auto-generated JWT secret.
func SaveConfig(path string, cfg *Config) error {
	f, err := os.Create(path)
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
