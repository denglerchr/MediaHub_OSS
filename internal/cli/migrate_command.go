package cli

import (
	"bufio"
	"context"
	"fmt"
	"mediahub_oss/internal/repository"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func NewMigrateCommand(globalOptions *GlobalOptions) *cobra.Command {

	var migrateCmd = &cobra.Command{
		Use:   "migrate",
		Short: "Database migration tools",
		Long:  `Manage database schema versions. Use subcommands 'up', 'down', or 'status'.`,
	}

	var upCmd = &cobra.Command{
		Use:   "up",
		Short: "Migrate the database to the most recent version",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigration("up", globalOptions)
		},
	}

	var downCmd = &cobra.Command{
		Use:   "down",
		Short: "Roll back the database by one version",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigration("down", globalOptions)
		},
	}

	var statusCmd = &cobra.Command{
		Use:   "status",
		Short: "Dump the migration status for the current DB",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigration("status", globalOptions)
		},
	}

	// Add subcommands
	migrateCmd.AddCommand(upCmd)
	migrateCmd.AddCommand(downCmd)
	migrateCmd.AddCommand(statusCmd)

	return migrateCmd
}

func runMigration(command string, globalOptions *GlobalOptions) error {
	ctx := context.Background()
	logger := globalOptions.Logger

	repo, err := initRepository(globalOptions.Conf.Database)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer repo.Close() // Clean up when the migration is done

	// Fetch the current database version
	version, err := repo.GetMigrationVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current database version: %w", err)
	}

	// Unpack the version for human-readable console output
	versionStr := repository.FormatVersion(version)

	// Handle the 'status' command immediately (no backup prompt needed)
	if command == "status" {
		fmt.Printf("Current database schema version: %s (internal: %d)\n", versionStr, version)
		return nil
	}

	// Prompt the user for backup confirmation before proceeding with up/down
	fmt.Printf("Current database version is %s.\n", versionStr)
	fmt.Print("WARNING: Before proceeding, it is highly recommended to create a backup of your database.\nHave you created a backup? (y/N): ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "y" && response != "yes" {
		fmt.Println("Migration aborted by user.")
		return nil
	}

	// 5. Execute the requested migration
	switch command {
	case "up":
		if version != 0 && version < 2000 {
			errorMessage := fmt.Sprintf("\nERROR: Migration from version %s to version 2.x is not supported.\n"+
				"This is due to major changes in the underlying file storage layout.\n"+
				"Please start with a fresh v2 installation and manually re-import your media.", versionStr)

			fmt.Println(errorMessage)
			logger.Warn("Blocked unsupported migration attempt from v1.x to v2.x", "current_version", versionStr)
			return fmt.Errorf("unsupported migration path: v1.x -> v2.x")
		} else {
			logger.Info("Starting database migration (Up)...")
			if err := repo.MigrateUp(ctx); err != nil {
				return fmt.Errorf("migration up failed: %w", err)
			}
			logger.Info("Migration (Up) completed successfully.")
		}

	case "down":
		logger.Info("Starting database rollback (Down)...")
		if err := repo.MigrateDown(ctx); err != nil {
			return fmt.Errorf("migration down failed: %w", err)
		}
		logger.Info("Migration (Down) completed successfully.")

	default:
		return fmt.Errorf("unknown migration command: %s", command)
	}

	return nil
}
