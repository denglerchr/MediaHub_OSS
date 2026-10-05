package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"mediahub_oss/internal/cli/config"
)

func TestLoadConfig_AutoGeneratesAndPersistsJWTSecret(t *testing.T) {
	// Create a temporary directory
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

	// 1. First load: secret is omitted, should be generated and persisted
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Auth.JWT.Secret == "" {
		t.Fatalf("expected generated JWT secret, got empty string")
	}

	firstSecret := cfg.Auth.JWT.Secret
	if len(firstSecret) != 64 { // 32 bytes hex encoded = 64 characters
		t.Errorf("expected 64 character hex secret, got %d chars: %s", len(firstSecret), firstSecret)
	}

	// 2. Read the file back from disk to verify persistence
	savedBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read persisted config file: %v", err)
	}

	savedContent := string(savedBytes)
	if len(savedContent) == 0 {
		t.Fatalf("persisted config file is empty")
	}

	// 3. Second load: should preserve the generated secret from disk across restarts
	cfgReloaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("second LoadConfig failed: %v", err)
	}

	if cfgReloaded.Auth.JWT.Secret != firstSecret {
		t.Errorf("expected preserved secret '%s', got '%s'", firstSecret, cfgReloaded.Auth.JWT.Secret)
	}
}

func TestLoadConfig_PreservesExistingJWTSecret(t *testing.T) {
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
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "does_not_exist.toml")

	cfg, err := config.LoadConfig(nonExistentPath)
	if err != nil {
		t.Fatalf("LoadConfig should not fail when config file is missing, got error: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
	if cfg.Auth.JWT.Secret == "" {
		t.Errorf("expected auto-generated JWT secret")
	}
}

