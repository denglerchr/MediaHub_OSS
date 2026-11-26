package cli

import (
	"context"
	"fmt"

	"mediahub_oss/internal/cli/recovery"
	"mediahub_oss/internal/repository/migrations"

	"github.com/spf13/cobra"
)

type RecoveryOptions struct {
	DryRun bool // If true, report only without editing
}

func NewRecoveryCommand(globalOptions *GlobalOptions) *cobra.Command {

	recoveryOptions := &RecoveryOptions{DryRun: false}

	recoveryCommand := &cobra.Command{
		Use:   "recovery",
		Short: "Run maintenance tasks to fix database inconsistencies",
		Long: `Scans the database for entries stuck in 'processing' or 'deleting' state. 
		Also checks for orphan files or database entries and cleans them up.
		This does not start the HTTP server.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecovery(globalOptions, recoveryOptions)
		},
	}

	recoveryOptions.registerFlags(recoveryCommand)

	return recoveryCommand
}

func (opt *RecoveryOptions) registerFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&opt.DryRun, "dryrun", false, "If true, report only without editing.")
}

func runRecovery(globalOptions *GlobalOptions, recoveryOptions *RecoveryOptions) error {
	logger := globalOptions.Logger
	ctx := context.Background()

	logger.Info("Starting recovery maintenance...", "dryRun", recoveryOptions.DryRun)

	// 1. Initialize repository and storage provider before constructing the recovery service
	// so any storage initialization error still cleanly closes the opened database pool.
	repo, err := initRepository(globalOptions.Conf.Database)
	if err != nil {
		return fmt.Errorf("failed to initialize recovery service: %w", err)
	}
	defer repo.Close()

	storageProvider, err := initStorage(globalOptions.Conf.Storage)
	if err != nil {
		return fmt.Errorf("failed to initialize recovery service: %w", err)
	}

	recoverySvc := recovery.NewRecoveryService(repo, storageProvider, logger, recoveryOptions.DryRun)

	// 2. Verify database schema version compatibility
	version, err := recoverySvc.GetMigrationVersion(ctx)
	if err != nil {
		return fmt.Errorf("could not determine database version: %w", err)
	}
	if err := migrations.CheckVersion(version); err != nil {
		return fmt.Errorf("database schema check failed: %w", err)
	}

	// 3. Execute Phase 1: Zombie Fix
	// Scans for entries stuck in "processing" or "deleting"
	logger.Info("Phase 1: Running Zombie Fix...")
	if err := recoverySvc.EntryStatusCorrection(ctx); err != nil {
		return fmt.Errorf("Entry status correction failed: %w", err)
	}

	// 4. Execute Phase 2: Integrity Check
	// Verifies file/DB parity.
	logger.Info("Phase 2: Running Integrity Check...")
	if err := recoverySvc.IntegrityCheck(ctx); err != nil {
		return fmt.Errorf("integrity check failed: %w", err)
	}

	logger.Info("Recovery process completed successfully.")
	return nil
}
