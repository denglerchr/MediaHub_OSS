package config_test

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mediahub_oss/internal/cli/config"

	"github.com/spf13/viper"
)

func TestLoadConfig_ReadOnlyDoesNotGenerateOrModifyFile(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	initialTOML := `
[server]
host = "127.0.0.1"
port = 8080
basepath = "/"
max_sync_upload_size = "4MB"

[database]
driver = "sqlite"
source = "mediahub_test.db"

[storage]
type = "local"

[storage.local]
root = "storage_root"

[logging]
level = "info"

[auth.jwt]
access_duration = "5min"
refresh_duration = "24h"
`
	if err := os.WriteFile(configPath, []byte(initialTOML), 0600); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Auth.JWT.Secret != "" {
		t.Fatalf("expected LoadConfig to be strictly read-only with empty JWT secret, got %q", cfg.Auth.JWT.Secret)
	}

	afterBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file after LoadConfig: %v", err)
	}
	if string(afterBytes) != initialTOML {
		t.Fatalf("LoadConfig modified config file on disk;\ngot:\n%s\nwant:\n%s", string(afterBytes), initialTOML)
	}
}

func TestLoadConfig_PreservesExistingJWTSecret(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	customSecret := "my-custom-super-secret-key-12345"
	initialTOML := `
[server]
host = "127.0.0.1"
port = 8080
basepath = "/"
max_sync_upload_size = "4MB"

[database]
driver = "sqlite"
source = "mediahub_test.db"

[storage]
type = "local"

[storage.local]
root = "storage_root"

[logging]
level = "info"

[auth.jwt]
access_duration = "10min"
refresh_duration = "48h"
secret = "` + customSecret + `"
`
	if err := os.WriteFile(configPath, []byte(initialTOML), 0600); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Auth.JWT.Secret != customSecret {
		t.Errorf("expected '%s', got '%s'", customSecret, cfg.Auth.JWT.Secret)
	}
}

func TestSaveConfig(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "saved_config.toml")

	var cfg config.Config
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 9000
	cfg.Database.Driver = "sqlite"
	cfg.Database.Source = "test.db"
	cfg.Storage.Type = "local"
	cfg.Storage.Local.Root = "data_root"
	cfg.Auth.JWT.Secret = "explicit-secret"
	cfg.Auth.JWT.AccessDuration = "5min"
	cfg.Auth.JWT.RefreshDuration = "24h"

	if err := config.SaveConfig(configPath, &cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatalf("failed to stat saved config: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("expected file mode 0600, got %#o", perm)
		}
	}

	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig after SaveConfig failed: %v", err)
	}

	if loaded.Server.Port != 9000 {
		t.Errorf("expected port 9000, got %d", loaded.Server.Port)
	}
	if loaded.Auth.JWT.Secret != "explicit-secret" {
		t.Errorf("expected secret 'explicit-secret', got '%s'", loaded.Auth.JWT.Secret)
	}
}

func TestLoadConfig_NonExistentFileUsesDefaults(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "does_not_exist.toml")

	cfg, err := config.LoadConfig(nonExistentPath)
	if err != nil {
		t.Fatalf("LoadConfig should not fail when config file is missing, got error: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
	if cfg.Auth.JWT.Secret != "" {
		t.Errorf("expected empty JWT secret from read-only LoadConfig, got %q", cfg.Auth.JWT.Secret)
	}
	if _, statErr := os.Stat(nonExistentPath); !os.IsNotExist(statErr) {
		t.Errorf("expected LoadConfig not to create %s, stat err: %v", nonExistentPath, statErr)
	}
}

func TestEnsureJWTSecret_PostgresRequiresExplicitSecret(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configPath := filepath.Join(t.TempDir(), "config.toml")

	for _, driver := range []string{"postgres", "Postgres"} {
		cfg := &config.Config{
			Database: config.DatabaseConfig{
				Driver: driver,
				Source: "postgres://localhost/mediahub",
			},
		}
		cfg.Auth.JWT.Secret = "   "

		err := config.EnsureJWTSecret(configPath, cfg, logger)
		if err == nil {
			t.Fatalf("driver=%q: expected error when JWT secret is omitted for postgres, got nil", driver)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "postgres") {
			t.Errorf("driver=%q: expected error to mention postgres, got: %v", driver, err)
		}
		if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
			t.Errorf("driver=%q: expected no config file to be written, stat err: %v", driver, statErr)
		}
	}
}

func TestEnsureJWTSecret_PostgresSucceedsWithExplicitSecret(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configPath := filepath.Join(t.TempDir(), "config.toml")

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Driver: "postgres",
			Source: "postgres://localhost/mediahub",
		},
	}
	cfg.Auth.JWT.Secret = "explicit-postgres-jwt-secret"

	if err := config.EnsureJWTSecret(configPath, cfg, logger); err != nil {
		t.Fatalf("expected EnsureJWTSecret to succeed on postgres with explicit secret, got: %v", err)
	}
	if cfg.Auth.JWT.Secret != "explicit-postgres-jwt-secret" {
		t.Errorf("expected explicit secret to be preserved, got %q", cfg.Auth.JWT.Secret)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Errorf("expected no file write when explicit secret already configured, stat err: %v", statErr)
	}
}

func TestEnsureJWTSecret_SQLitePersistsWithoutLeakingEnvSecrets(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	initialTOML := `
[server]
host = "127.0.0.1"
port = 8080
basepath = "/"
max_sync_upload_size = "4MB"

[database]
driver = "sqlite"
source = "mediahub_from_file.db"

[storage]
type = "local"

[storage.local]
root = "storage_root"

[logging]
level = "info"

[auth.jwt]
access_duration = "10min"
`
	if err := os.WriteFile(configPath, []byte(initialTOML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	const (
		envS3Secret   = "super-secret-s3-env-value"
		envOIDCSecret = "super-secret-oidc-env-value"
		envDBSource   = "env-override-db-source.db"
	)
	t.Setenv("MEDIAHUB_STORAGE_S3_SECRET_KEY", envS3Secret)
	t.Setenv("MEDIAHUB_AUTH_OIDC_CLIENT_SECRET", envOIDCSecret)
	t.Setenv("MEDIAHUB_DATABASE_SOURCE", envDBSource)
	// Register keys with Viper just as CLI flag binding does in serve_command.go
	// so AutomaticEnv populates them into the unmarshaled Config struct.
	if err := viper.BindEnv("storage.s3.secret_key", "MEDIAHUB_STORAGE_S3_SECRET_KEY"); err != nil {
		t.Fatalf("BindEnv s3 secret failed: %v", err)
	}
	if err := viper.BindEnv("auth.oidc.client_secret", "MEDIAHUB_AUTH_OIDC_CLIENT_SECRET"); err != nil {
		t.Fatalf("BindEnv oidc secret failed: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Storage.S3.SecretKey != envS3Secret || cfg.Auth.OIDC.ClientSecret != envOIDCSecret || cfg.Database.Source != envDBSource {
		t.Fatalf("expected env overrides to be loaded into memory config")
	}
	if cfg.Auth.JWT.Secret != "" {
		t.Fatalf("expected empty JWT secret after LoadConfig, got %q", cfg.Auth.JWT.Secret)
	}

	if err := config.EnsureJWTSecret(configPath, cfg, logger); err != nil {
		t.Fatalf("EnsureJWTSecret failed: %v", err)
	}

	firstSecret := cfg.Auth.JWT.Secret
	if len(firstSecret) != 64 {
		t.Fatalf("expected 64-char hex secret, got %d chars (%q)", len(firstSecret), firstSecret)
	}
	if cfg.Auth.JWT.AccessDuration != "10min" {
		t.Errorf("expected existing access_duration '10min', got %q", cfg.Auth.JWT.AccessDuration)
	}
	if cfg.Auth.JWT.RefreshDuration != "24h" {
		t.Errorf("expected default refresh_duration '24h', got %q", cfg.Auth.JWT.RefreshDuration)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatalf("failed to stat persisted config: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("expected persisted config mode 0600, got %#o", perm)
		}
	}

	persistedBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read persisted config: %v", err)
	}
	persistedContent := string(persistedBytes)

	if !strings.Contains(persistedContent, firstSecret) {
		t.Errorf("expected persisted config to contain generated JWT secret")
	}
	if !strings.Contains(persistedContent, "mediahub_from_file.db") {
		t.Errorf("expected persisted config to preserve file database.source, got:\n%s", persistedContent)
	}
	for _, leaked := range []string{envS3Secret, envOIDCSecret, envDBSource} {
		if strings.Contains(persistedContent, leaked) {
			t.Errorf("env secret/override %q leaked into persisted config.toml:\n%s", leaked, persistedContent)
		}
	}

	// Reload in a fresh Viper state and verify JWT secret persists across restarts.
	viper.Reset()
	reloaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("reload LoadConfig failed: %v", err)
	}
	if reloaded.Auth.JWT.Secret != firstSecret {
		t.Errorf("expected reloaded JWT secret %q, got %q", firstSecret, reloaded.Auth.JWT.Secret)
	}
	if reloaded.Auth.JWT.AccessDuration != "10min" {
		t.Errorf("expected preserved access_duration '10min' after reload, got %q", reloaded.Auth.JWT.AccessDuration)
	}
	if reloaded.Auth.JWT.RefreshDuration != "24h" {
		t.Errorf("expected persisted refresh_duration '24h' after reload, got %q", reloaded.Auth.JWT.RefreshDuration)
	}
}

func TestEnsureJWTSecret_SQLiteUnwritablePathFallsBackToEphemeral(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	unwritablePath := filepath.Join(t.TempDir(), "nonexistent-dir", "config.toml")
	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Driver: "sqlite",
			Source: "mediahub_test.db",
		},
	}

	if err := config.EnsureJWTSecret(unwritablePath, cfg, logger); err != nil {
		t.Fatalf("expected EnsureJWTSecret to fall back gracefully on unwritable path, got error: %v", err)
	}
	if len(cfg.Auth.JWT.Secret) != 64 {
		t.Errorf("expected 64-char ephemeral JWT secret, got %d chars (%q)", len(cfg.Auth.JWT.Secret), cfg.Auth.JWT.Secret)
	}
	if cfg.Auth.JWT.AccessDuration != "5min" {
		t.Errorf("expected default AccessDuration '5min', got %q", cfg.Auth.JWT.AccessDuration)
	}
	if cfg.Auth.JWT.RefreshDuration != "24h" {
		t.Errorf("expected default RefreshDuration '24h', got %q", cfg.Auth.JWT.RefreshDuration)
	}
	if !strings.Contains(logBuf.String(), "level=WARN") {
		t.Errorf("expected warning log when config file cannot be persisted, got: %q", logBuf.String())
	}
}

