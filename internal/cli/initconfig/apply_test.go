package initconfig_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"mediahub_oss/internal/cli/initconfig"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

type mockRepo struct {
	repository.Repository
	users     map[string]repository.User
	databases []repository.Database
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		users:     make(map[string]repository.User),
		databases: make([]repository.Database, 0),
	}
}

func (m *mockRepo) GetDatabases(ctx context.Context) ([]repository.Database, error) {
	return m.databases, nil
}

func (m *mockRepo) CreateDatabase(ctx context.Context, db repository.Database) (repository.Database, error) {
	db.ID = repository.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	m.databases = append(m.databases, db)
	return db, nil
}

func (m *mockRepo) GetUserByUsername(ctx context.Context, username string) (repository.User, error) {
	u, ok := m.users[username]
	if !ok {
		return repository.User{}, customerrors.ErrNotFound
	}
	return u, nil
}

func (m *mockRepo) CreateUser(ctx context.Context, user repository.User) (repository.User, error) {
	user.ID = repository.ULID("01HGFB9Z5W7ABCDEFGHJKMNPQS")
	m.users[user.Username] = user
	return user, nil
}

func (m *mockRepo) SetUserPermissions(ctx context.Context, permissions repository.UserPermissions) error {
	return nil
}

func TestApply_RedactsPasswordsForExistingAndNewUsers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	t.Run("redacts passwords when users already exist", func(t *testing.T) {
		repo := newMockRepo()
		repo.users["alice"] = repository.User{
			ID:       "01HGFB9Z5W7ABCDEFGHJKMNPQT",
			Username: "alice",
		}

		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "init.toml")
		initialTOML := `
[[user]]
name = "alice"
is_admin = false
password = "AliceSecretPassword"
`
		if err := os.WriteFile(configFile, []byte(initialTOML), 0644); err != nil {
			t.Fatalf("failed to write test toml: %v", err)
		}

		cfg, err := initconfig.ParseInitConfig(configFile)
		if err != nil {
			t.Fatalf("failed to parse init config: %v", err)
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, configFile); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// In memory check
		if cfg.Users[0].Password != "" {
			t.Errorf("expected in-memory password to be empty, got %q", cfg.Users[0].Password)
		}

		// On disk check
		content, err := os.ReadFile(configFile)
		if err != nil {
			t.Fatalf("failed to read modified config file: %v", err)
		}
		if strings.Contains(string(content), "AliceSecretPassword") {
			t.Errorf("config file on disk still contains plaintext password: %s", string(content))
		}

		var parsedAfter initconfig.InitConfig
		if _, err := toml.Decode(string(content), &parsedAfter); err != nil {
			t.Fatalf("failed to decode rewritten toml: %v", err)
		}
		if len(parsedAfter.Users) != 1 || parsedAfter.Users[0].Password != "" {
			t.Errorf("expected rewritten config user password to be empty, got %+v", parsedAfter.Users)
		}
	})

	t.Run("redacts passwords in mixed scenario (new and existing users)", func(t *testing.T) {
		repo := newMockRepo()
		repo.users["alice"] = repository.User{
			ID:       "01HGFB9Z5W7ABCDEFGHJKMNPQT",
			Username: "alice",
		}

		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "init.toml")
		initialTOML := `
[[user]]
name = "alice"
is_admin = false
password = "AlicePassword"

[[user]]
name = "bob"
is_admin = true
password = "BobPassword"
`
		if err := os.WriteFile(configFile, []byte(initialTOML), 0644); err != nil {
			t.Fatalf("failed to write test toml: %v", err)
		}

		cfg, err := initconfig.ParseInitConfig(configFile)
		if err != nil {
			t.Fatalf("failed to parse init config: %v", err)
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, configFile); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		for _, u := range cfg.Users {
			if u.Password != "" {
				t.Errorf("expected in-memory password for %s to be empty, got %q", u.Name, u.Password)
			}
		}

		content, err := os.ReadFile(configFile)
		if err != nil {
			t.Fatalf("failed to read modified config file: %v", err)
		}
		if strings.Contains(string(content), "AlicePassword") || strings.Contains(string(content), "BobPassword") {
			t.Errorf("config file on disk still contains plaintext passwords: %s", string(content))
		}
	})
}

func TestParseInitConfig_CreatePreview(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "init_preview.toml")
	tomlData := `
[[database]]
name = "ImageDB"
content_type = "image"
config = { create_preview = true, auto_conversion = "jpeg" }
`
	if err := os.WriteFile(configFile, []byte(tomlData), 0644); err != nil {
		t.Fatalf("failed to write test toml: %v", err)
	}

	cfg, err := initconfig.ParseInitConfig(configFile)
	if err != nil {
		t.Fatalf("ParseInitConfig failed: %v", err)
	}

	if len(cfg.Databases) != 1 {
		t.Fatalf("expected 1 database, got %d", len(cfg.Databases))
	}

	if !cfg.Databases[0].Config.CreatePreview {
		t.Errorf("expected CreatePreview to be true, got false")
	}
}

func TestApply_AccountType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	t.Run("default account_type when omitted is local", func(t *testing.T) {
		repo := newMockRepo()
		cfg := initconfig.InitConfig{
			Users: []initconfig.InitUser{
				{
					Name:     "default_user",
					Password: "ValidPassword123",
				},
			},
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, ""); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		user, exists := repo.users["default_user"]
		if !exists {
			t.Fatalf("expected user default_user to be created")
		}
		if user.AccountType != repository.AccountTypeLocal {
			t.Errorf("expected AccountTypeLocal (0), got %v", user.AccountType)
		}
		if user.PasswordHash == "" || user.PasswordHash == "SERVICE_ACCOUNT_NO_LOGIN" {
			t.Errorf("expected bcrypt password hash, got %q", user.PasswordHash)
		}
	})

	t.Run("explicit local account_type", func(t *testing.T) {
		repo := newMockRepo()
		cfg := initconfig.InitConfig{
			Users: []initconfig.InitUser{
				{
					Name:        "local_user",
					AccountType: "local",
					Password:    "ValidPassword123",
				},
			},
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, ""); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		user, exists := repo.users["local_user"]
		if !exists {
			t.Fatalf("expected user local_user to be created")
		}
		if user.AccountType != repository.AccountTypeLocal {
			t.Errorf("expected AccountTypeLocal (0), got %v", user.AccountType)
		}
	})

	t.Run("service_account without password creates service account with SERVICE_ACCOUNT_NO_LOGIN", func(t *testing.T) {
		repo := newMockRepo()
		cfg := initconfig.InitConfig{
			Users: []initconfig.InitUser{
				{
					Name:        "sa_user",
					AccountType: "service_account",
				},
			},
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, ""); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		user, exists := repo.users["sa_user"]
		if !exists {
			t.Fatalf("expected user sa_user to be created")
		}
		if user.AccountType != repository.AccountTypeService {
			t.Errorf("expected AccountTypeService (1), got %v", user.AccountType)
		}
		if user.PasswordHash != "SERVICE_ACCOUNT_NO_LOGIN" {
			t.Errorf("expected PasswordHash 'SERVICE_ACCOUNT_NO_LOGIN', got %q", user.PasswordHash)
		}
	})

	t.Run("service_account with password sets SERVICE_ACCOUNT_NO_LOGIN and redacts password", func(t *testing.T) {
		repo := newMockRepo()
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "init.toml")
		initialTOML := `
[[user]]
name = "sa_with_pw"
account_type = "service_account"
password = "ShouldBeRedacted"
`
		if err := os.WriteFile(configFile, []byte(initialTOML), 0644); err != nil {
			t.Fatalf("failed to write test toml: %v", err)
		}

		cfg, err := initconfig.ParseInitConfig(configFile)
		if err != nil {
			t.Fatalf("failed to parse init config: %v", err)
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, configFile); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		user, exists := repo.users["sa_with_pw"]
		if !exists {
			t.Fatalf("expected user sa_with_pw to be created")
		}
		if user.AccountType != repository.AccountTypeService {
			t.Errorf("expected AccountTypeService (1), got %v", user.AccountType)
		}
		if user.PasswordHash != "SERVICE_ACCOUNT_NO_LOGIN" {
			t.Errorf("expected PasswordHash 'SERVICE_ACCOUNT_NO_LOGIN', got %q", user.PasswordHash)
		}

		content, err := os.ReadFile(configFile)
		if err != nil {
			t.Fatalf("failed to read config file: %v", err)
		}
		if strings.Contains(string(content), "ShouldBeRedacted") {
			t.Errorf("expected password to be redacted from file, got: %s", string(content))
		}
	})

	t.Run("local account with empty password is not created", func(t *testing.T) {
		repo := newMockRepo()
		cfg := initconfig.InitConfig{
			Users: []initconfig.InitUser{
				{
					Name:        "empty_pw_user",
					AccountType: "local",
					Password:    "",
				},
			},
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, ""); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if _, exists := repo.users["empty_pw_user"]; exists {
			t.Errorf("expected local user with empty password to not be created")
		}
	})

	t.Run("oidc account_type is rejected", func(t *testing.T) {
		repo := newMockRepo()
		cfg := initconfig.InitConfig{
			Users: []initconfig.InitUser{
				{
					Name:        "oidc_user",
					AccountType: "oidc",
				},
			},
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, ""); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if _, exists := repo.users["oidc_user"]; exists {
			t.Errorf("expected oidc user to not be created via init_config")
		}
	})

	t.Run("invalid account_type is rejected", func(t *testing.T) {
		repo := newMockRepo()
		cfg := initconfig.InitConfig{
			Users: []initconfig.InitUser{
				{
					Name:        "invalid_user",
					AccountType: "superuser",
					Password:    "password123",
				},
			},
		}

		if err := initconfig.Apply(ctx, &cfg, repo, logger, ""); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if _, exists := repo.users["invalid_user"]; exists {
			t.Errorf("expected user with invalid account_type to not be created")
		}
	})
}

