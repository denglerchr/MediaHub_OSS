package cli_test

import (
	"testing"

	"mediahub_oss/internal/cli"
	"mediahub_oss/internal/cli/config"

	"github.com/spf13/viper"
)

func TestServeCommand_FlagBinding(t *testing.T) {
	viper.Reset()

	globalOptions := &cli.GlobalOptions{}
	cmd := cli.NewServeCommand(globalOptions, nil)

	// Set test arguments simulating compound CLI flags
	args := []string{
		"--server-max-sync-upload=16MB",
		"--server-cors-origins=https://example.com,https://api.example.com",
		"--server-processing-n-ffmpeg-async=4",
		"--server-processing-n-ffmpeg-total=8",
		"--database-max-open-conns=50",
		"--database-max-idle-conns=30",
		"--storage-s3-access-key=my-access-key",
		"--storage-s3-secret-key=my-secret-key",
		"--storage-s3-use-ssl=false",
		"--media-ffmpeg-path=/usr/bin/ffmpeg",
		"--media-ffprobe-path=/usr/bin/ffprobe",
		"--auth-jwt-access-duration=15min",
		"--auth-jwt-refresh-duration=72h",
		"--auth-oidc-disable-login-page=true",
		"--auth-oidc-default-user-rights=custom_role",
		"--auth-oidc-issuer-url=https://auth.example.com",
		"--auth-oidc-client-id=my-client",
		"--auth-oidc-client-secret=my-client-secret",
		"--auth-oidc-redirect-url=https://app.example.com/callback",
	}

	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	var cfg config.Config
	if err := viper.Unmarshal(&cfg); err != nil {
		t.Fatalf("viper.Unmarshal failed: %v", err)
	}

	// Verify that compound/hyphenated CLI flags successfully populated cfg struct fields
	if cfg.Server.MaxSyncUploadSize != "16MB" {
		t.Errorf("expected Server.MaxSyncUploadSize '16MB', got %q", cfg.Server.MaxSyncUploadSize)
	}
	if len(cfg.Server.CorsAllowedOrigins) != 2 || cfg.Server.CorsAllowedOrigins[0] != "https://example.com" {
		t.Errorf("expected Server.CorsAllowedOrigins ['https://example.com', 'https://api.example.com'], got %v", cfg.Server.CorsAllowedOrigins)
	}
	if cfg.Server.Processing.NFfmpegAsync != "4" {
		t.Errorf("expected Server.Processing.NFfmpegAsync '4', got %q", cfg.Server.Processing.NFfmpegAsync)
	}
	if cfg.Server.Processing.NFfmpegTotal != "8" {
		t.Errorf("expected Server.Processing.NFfmpegTotal '8', got %q", cfg.Server.Processing.NFfmpegTotal)
	}
	if cfg.Database.MaxOpenConns != 50 {
		t.Errorf("expected Database.MaxOpenConns 50, got %d", cfg.Database.MaxOpenConns)
	}
	if cfg.Database.MaxIdleConns != 30 {
		t.Errorf("expected Database.MaxIdleConns 30, got %d", cfg.Database.MaxIdleConns)
	}
	if cfg.Storage.S3.AccessKey != "my-access-key" {
		t.Errorf("expected Storage.S3.AccessKey 'my-access-key', got %q", cfg.Storage.S3.AccessKey)
	}
	if cfg.Storage.S3.SecretKey != "my-secret-key" {
		t.Errorf("expected Storage.S3.SecretKey 'my-secret-key', got %q", cfg.Storage.S3.SecretKey)
	}
	if cfg.Storage.S3.UseSSL != false {
		t.Errorf("expected Storage.S3.UseSSL false, got true")
	}
	if cfg.Media.FFmpegPath != "/usr/bin/ffmpeg" {
		t.Errorf("expected Media.FFmpegPath '/usr/bin/ffmpeg', got %q", cfg.Media.FFmpegPath)
	}
	if cfg.Media.FFprobePath != "/usr/bin/ffprobe" {
		t.Errorf("expected Media.FFprobePath '/usr/bin/ffprobe', got %q", cfg.Media.FFprobePath)
	}
	if cfg.Auth.JWT.AccessDuration != "15min" {
		t.Errorf("expected Auth.JWT.AccessDuration '15min', got %q", cfg.Auth.JWT.AccessDuration)
	}
	if cfg.Auth.JWT.RefreshDuration != "72h" {
		t.Errorf("expected Auth.JWT.RefreshDuration '72h', got %q", cfg.Auth.JWT.RefreshDuration)
	}
	if cfg.Auth.OIDC.DisableLoginPage != true {
		t.Errorf("expected Auth.OIDC.DisableLoginPage true, got false")
	}
	if cfg.Auth.OIDC.DefaultUserRights != "custom_role" {
		t.Errorf("expected Auth.OIDC.DefaultUserRights 'custom_role', got %q", cfg.Auth.OIDC.DefaultUserRights)
	}
	if cfg.Auth.OIDC.IssuerURL != "https://auth.example.com" {
		t.Errorf("expected Auth.OIDC.IssuerURL 'https://auth.example.com', got %q", cfg.Auth.OIDC.IssuerURL)
	}
	if cfg.Auth.OIDC.ClientID != "my-client" {
		t.Errorf("expected Auth.OIDC.ClientID 'my-client', got %q", cfg.Auth.OIDC.ClientID)
	}
	if cfg.Auth.OIDC.ClientSecret != "my-client-secret" {
		t.Errorf("expected Auth.OIDC.ClientSecret 'my-client-secret', got %q", cfg.Auth.OIDC.ClientSecret)
	}
	if cfg.Auth.OIDC.RedirectURL != "https://app.example.com/callback" {
		t.Errorf("expected Auth.OIDC.RedirectURL 'https://app.example.com/callback', got %q", cfg.Auth.OIDC.RedirectURL)
	}
}

func TestServeCommand_DisableLoginPageFlag(t *testing.T) {
	viper.Reset()

	globalOptions := &cli.GlobalOptions{}
	cmd := cli.NewServeCommand(globalOptions, nil)

	args := []string{
		"--auth-oidc-disable-login-page=true",
	}

	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	var cfg config.Config
	if err := viper.Unmarshal(&cfg); err != nil {
		t.Fatalf("viper.Unmarshal failed: %v", err)
	}

	if cfg.Auth.OIDC.DisableLoginPage != true {
		t.Errorf("expected Auth.OIDC.DisableLoginPage true when using --auth-oidc-disable-login-page, got false")
	}
}

