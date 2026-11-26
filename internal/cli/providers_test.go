package cli

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mediahub_oss/internal/cli/config"
	"mediahub_oss/internal/storage/localstorage"
)

func TestInitRepository(t *testing.T) {
	t.Run("sqlite initializes cleanly", func(t *testing.T) {
		r, err := initRepository(config.DatabaseConfig{
			Driver: "sqlite",
			Source: ":memory:",
		})
		if err != nil {
			t.Fatalf("expected sqlite init to succeed, got %v", err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("expected repo close to succeed, got %v", err)
		}
	})

	t.Run("unsupported driver returns error", func(t *testing.T) {
		if _, err := initRepository(config.DatabaseConfig{Driver: "mysql"}); err == nil {
			t.Fatal("expected error for unsupported database driver")
		}
	})
}

func TestInitStorage(t *testing.T) {
	t.Run("local storage initializes cleanly", func(t *testing.T) {
		root := t.TempDir()
		sp, err := initStorage(config.StorageConfig{
			Type:  "local",
			Local: config.LocalConfig{Root: root},
		})
		if err != nil {
			t.Fatalf("expected local storage init to succeed, got %v", err)
		}
		local, ok := sp.(*localstorage.LocalStorage)
		if !ok || local.RootPath != root {
			t.Fatalf("unexpected storage provider %#v", sp)
		}
	})

	t.Run("s3 storage with incomplete config fails", func(t *testing.T) {
		if _, err := initStorage(config.StorageConfig{Type: "s3"}); err == nil {
			t.Fatal("expected error for incomplete s3 config")
		}
	})

	t.Run("unsupported storage type returns error", func(t *testing.T) {
		if _, err := initStorage(config.StorageConfig{Type: "gcs"}); err == nil {
			t.Fatal("expected error for unsupported storage type")
		}
	})
}

func TestRunRecovery_ClosesRepoOnStorageInitFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "recovery_leak_test.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := runRecovery(&GlobalOptions{
		Conf: &config.Config{
			Database: config.DatabaseConfig{
				Driver: "sqlite",
				Source: dbPath,
			},
			Storage: config.StorageConfig{
				Type: "invalid-storage",
			},
		},
		Logger: logger,
	}, &RecoveryOptions{DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "unsupported storage type") {
		t.Fatalf("expected unsupported storage type error, got %v", err)
	}

	// On Windows, an unclosed SQLite handle blocks deleting the database file.
	if err := os.Remove(dbPath); err != nil {
		t.Fatalf("expected SQLite file handle to be closed before runRecovery returned: %v", err)
	}
}
