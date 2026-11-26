package cli

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	conf "mediahub_oss/internal/cli/config"
	"mediahub_oss/internal/logging"

	"github.com/spf13/cobra"
)

type GlobalOptions struct {
	CfgFilePath string
	LogLevel    string

	Logger *slog.Logger
	Conf   *conf.Config
}

func NewRootCMD(frontendFS fs.FS) *cobra.Command {

	globalOptions := &GlobalOptions{}

	rootCMD := &cobra.Command{
		Use:           "mediahub",
		Short:         "SWCD MediaHub",
		Long:          "A server for a image, audio and file storage with integrated web-ui.",
		SilenceErrors: true,
		SilenceUsage:  true,
		// PersistentPreRunE runs after flags are parsed but before any subcommand's Run
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip configuration and logger initialization if the user is just asking for help
			// or running the root command without subcommands.
			if cmd.Name() == "mediahub" || cmd.Name() == "help" {
				return nil
			}

			// Resolve the Configuration Path manually to allow for environment variable fallback
			cfgPath := globalOptions.CfgFilePath
			// If the user didn't explicitly pass the --config_path flag, check the environment
			if !cmd.Flags().Changed("config_path") {
				if envPath := os.Getenv("MEDIAHUB_CONFIG_PATH"); envPath != "" {
					cfgPath = envPath
				}
			}
			globalOptions.CfgFilePath = cfgPath

			// Load the base configuration from the TOML file
			loadedConfig, err := conf.LoadConfig(cfgPath)
			if err != nil {
				return fmt.Errorf("failed to load configuration from %s: %w", cfgPath, err)
			}
			globalOptions.Conf = loadedConfig

			// Determine final log level (CLI flag > ENV var > TOML config)
			finalLogLevel := globalOptions.Conf.Logging.Level
			if envLogLevel := os.Getenv("MEDIAHUB_LOGGING_LEVEL"); envLogLevel != "" {
				finalLogLevel = envLogLevel
			}
			if globalOptions.LogLevel != "" {
				finalLogLevel = globalOptions.LogLevel
			}

			// Initialize the global logger
			globalOptions.Logger = logging.NewLogger(finalLogLevel)

			return nil
		},
		// Explicitly call the help message if no subcommand is provided
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	}

	// register global flags
	globalOptions.registerFlags(rootCMD)

	// add subcommands
	rootCMD.AddCommand(NewServeCommand(globalOptions, frontendFS))
	rootCMD.AddCommand(NewMigrateCommand(globalOptions))
	rootCMD.AddCommand(NewRecoveryCommand(globalOptions))

	return rootCMD
}

func (options *GlobalOptions) registerFlags(cmd *cobra.Command) {
	// flags that can be used for each command
	cmd.PersistentFlags().StringVar(&options.CfgFilePath, "config_path", "config.toml", "Path to the base configuration file. (Env: MEDIAHUB_CONFIG_PATH)")
	cmd.PersistentFlags().StringVar(&options.LogLevel, "log-level", "", "Logging level (debug, info, warn, error). (Env: MEDIAHUB_LOGGING_LEVEL)")
}

func Execute(frontendFS fs.FS) {

	rootCmd := NewRootCMD(frontendFS)

	// Run the command based on os.Args
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
