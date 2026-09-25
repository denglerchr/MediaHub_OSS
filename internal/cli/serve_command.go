package cli

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"mediahub_oss/docs" // to get the version
	"mediahub_oss/internal/cli/config"
	"mediahub_oss/internal/cli/initconfig"
	"mediahub_oss/internal/housekeeping"
	"mediahub_oss/internal/httpserver"
	ah "mediahub_oss/internal/httpserver/audithandler"
	"mediahub_oss/internal/httpserver/auth"
	dbh "mediahub_oss/internal/httpserver/databasehandler"
	eh "mediahub_oss/internal/httpserver/entryhandler"
	ih "mediahub_oss/internal/httpserver/infohandler"
	th "mediahub_oss/internal/httpserver/tokenhandler"
	uh "mediahub_oss/internal/httpserver/userhandler"
	"mediahub_oss/internal/logging/audit"
	"mediahub_oss/internal/media/ffmpeg"
	"mediahub_oss/internal/processing"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/migrations"
	"mediahub_oss/internal/repository/postgres"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/storage"
	"mediahub_oss/internal/storage/localstorage"
	"mediahub_oss/internal/storage/s3storage"
	"strings"
	"time"

	// Aliased imports for your sub-handlers

	"net/http"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewServeCommand(globalOptions *GlobalOptions, frontendFS fs.FS) *cobra.Command {

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return serve(globalOptions, frontendFS)
		},
	}

	registerFlags(serveCmd)

	return serveCmd
}

func registerFlags(cmd *cobra.Command) {

	// Operational & Startup Flags
	cmd.Flags().String("init_config", "", "Path to a TOML config file for one-time initialization.")
	cmd.Flags().String("password", "", "Password for the 'admin' user.")
	cmd.Flags().Bool("reset_pw", false, "If true, reset admin password on startup.")

	// Server Settings
	cmd.Flags().String("server-host", "0.0.0.0", "The host address to bind to.")
	cmd.Flags().Int("server-port", 8080, "The HTTP port to bind to.")
	cmd.Flags().String("server-basepath", "/", "The base path for reverse proxy.")
	cmd.Flags().String("server-max-sync-upload", "4MB", "RAM threshold for uploads.")
	cmd.Flags().StringSlice("server-cors-origins", []string{}, "Allowed CORS origins.")
	cmd.Flags().String("server-processing-n-ffmpeg-async", "auto", "Limit for asynchronous processors.")
	cmd.Flags().String("server-processing-n-ffmpeg-total", "auto", "Limit for all conversion processors.")

	// Database Settings
	cmd.Flags().String("database-driver", "sqlite", "Database driver (sqlite or postgres).")
	cmd.Flags().String("database-source", "mediahub.db", "Path to DB file or connection string.")
	cmd.Flags().Int("database-max-open-conns", 25, "PostgreSQL max open connections.")
	cmd.Flags().Int("database-max-idle-conns", 25, "PostgreSQL max idle connections.")

	// Storage Settings
	cmd.Flags().String("storage-type", "local", "Storage backend type (local or s3).")
	cmd.Flags().String("storage-local-root", "storage_root", "Root directory for local storage.")
	cmd.Flags().String("storage-s3-endpoint", "", "S3 API endpoint.")
	cmd.Flags().String("storage-s3-region", "", "S3 region.")
	cmd.Flags().String("storage-s3-bucket", "", "S3 bucket name.")
	cmd.Flags().String("storage-s3-access-key", "", "S3 Access Key.")
	cmd.Flags().String("storage-s3-secret-key", "", "S3 Secret Key.")
	cmd.Flags().Bool("storage-s3-use-ssl", true, "Enable HTTPS for S3 connection.")

	// Logging Settings
	cmd.Flags().String("logging-level", "info", "Logging verbosity.")
	cmd.Flags().String("logging-audit-type", "stdio", "Where to store audit logs.")
	cmd.Flags().Bool("logging-audit-enabled", false, "Toggle audit logging.")
	cmd.Flags().String("logging-audit-retention", "31d", "How long to keep audit logs.")

	// Media Settings
	cmd.Flags().String("media-ffmpeg-path", "", "Path to FFmpeg executable.")
	cmd.Flags().String("media-ffprobe-path", "", "Path to FFprobe executable.")

	// Auth Settings
	cmd.Flags().String("auth-jwt-access-duration", "5min", "Validity of the JWT.")
	cmd.Flags().String("auth-jwt-refresh-duration", "24h", "Validity of the refresh token.")
	cmd.Flags().String("auth-jwt-secret", "", "Secret key for signing JWTs.")
	cmd.Flags().Bool("auth-oidc-enabled", false, "Toggle OIDC integration.")
	cmd.Flags().Bool("auth-oidc-disable-login-page", false, "Disable frontend login page and redirect directly to OIDC.")
	cmd.Flags().String("auth-oidc-default-user-rights", "_oidc_user", "Default rights for new OIDC users.")
	cmd.Flags().String("auth-oidc-issuer-url", "", "OIDC Issuer URL.")
	cmd.Flags().String("auth-oidc-client-id", "", "OIDC Client ID.")
	cmd.Flags().String("auth-oidc-client-secret", "", "OIDC Client Secret.")
	cmd.Flags().String("auth-oidc-redirect-url", "", "OIDC Redirect callback URL (must end in /auth/callback). Defaults to '<origin>/auth/callback' if omitted.")

	flagToViperKey := map[string]string{
		"server-host":                      "server.host",
		"server-port":                      "server.port",
		"server-basepath":                  "server.basepath",
		"server-max-sync-upload":           "server.max_sync_upload_size",
		"server-cors-origins":              "server.cors_allowed_origins",
		"server-processing-n-ffmpeg-async": "server.processing.n_ffmpeg_async",
		"server-processing-n-ffmpeg-total": "server.processing.n_ffmpeg_total",
		"database-driver":                  "database.driver",
		"database-source":                  "database.source",
		"database-max-open-conns":          "database.max_open_conns",
		"database-max-idle-conns":          "database.max_idle_conns",
		"storage-type":                     "storage.type",
		"storage-local-root":               "storage.local.root",
		"storage-s3-endpoint":              "storage.s3.endpoint",
		"storage-s3-region":                "storage.s3.region",
		"storage-s3-bucket":                "storage.s3.bucket",
		"storage-s3-access-key":            "storage.s3.access_key",
		"storage-s3-secret-key":            "storage.s3.secret_key",
		"storage-s3-use-ssl":               "storage.s3.use_ssl",
		"logging-level":                    "logging.level",
		"logging-audit-type":               "logging.audit.type",
		"logging-audit-enabled":            "logging.audit.enabled",
		"logging-audit-retention":          "logging.audit.retention",
		"media-ffmpeg-path":                "media.ffmpeg_path",
		"media-ffprobe-path":               "media.ffprobe_path",
		"auth-jwt-access-duration":         "auth.jwt.access_duration",
		"auth-jwt-refresh-duration":        "auth.jwt.refresh_duration",
		"auth-jwt-secret":                  "auth.jwt.secret",
		"auth-oidc-enabled":                "auth.oidc.enabled",
		"auth-oidc-disable-login-page":     "auth.oidc.disable_login_page",
		"auth-oidc-default-user-rights":    "auth.oidc.default_user_rights",
		"auth-oidc-issuer-url":             "auth.oidc.issuer_url",
		"auth-oidc-client-id":              "auth.oidc.client_id",
		"auth-oidc-client-secret":          "auth.oidc.client_secret",
		"auth-oidc-redirect-url":           "auth.oidc.redirect_url",
	}

	for flagName, viperKey := range flagToViperKey {
		if f := cmd.Flags().Lookup(flagName); f != nil {
			_ = viper.BindPFlag(viperKey, f)
		}
	}
}

// backgroundServices holds the initialized instances of all running background components.
type backgroundServices struct {
	houseKeeper    *housekeeping.HouseKeeper
	mediaConverter *ffmpeg.FfmpegConverter
	auditLogger    audit.AuditLogger
	authMiddleware *auth.AuthMiddleware
	processor      *processing.Processor
}

func serve(globalOptions *GlobalOptions, frontendFS fs.FS) error {
	// Capture the start time for the InfoHandler's uptime calculation.
	startTime := time.Now()

	cfg := globalOptions.Conf
	logger := globalOptions.Logger
	ctx := context.Background()

	logger.Info("Bootstrapping MediaHub server...")

	// 1. Initialize repository and database schema.
	repo, err := initDatabaseAndSchema(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	// Because we return errors now, this defer will always execute safely.
	defer repo.Close()

	// 2. Initialize storage provider.
	storageProvider, err := initStorage(cfg.Storage)
	if err != nil {
		return fmt.Errorf("failed to initialize storage provider: %w", err)
	}

	// 3. Process one-time initialization config if present.
	if err := processInitConfig(ctx, repo, logger); err != nil {
		logger.Warn("Initialization config processing failed", "error", err)
	}

	// 4. Initialize core background services.
	svcs, err := initServices(ctx, cfg, repo, storageProvider, logger)
	if err != nil {
		return err
	}

	// 5. Build REST handlers.
	handlers, err := buildHandlers(cfg, repo, storageProvider, svcs, logger, startTime)
	if err != nil {
		return err
	}

	// 6. Setup router and start the HTTP server.
	return startServer(cfg, handlers, svcs.authMiddleware, frontendFS, logger)
}

// initDatabaseAndSchema initializes the repository connection, runs version check or auto-migration,
// and ensures the initial admin user is configured.
func initDatabaseAndSchema(ctx context.Context, dbCfg config.DatabaseConfig, logger *slog.Logger) (repository.Repository, error) {
	repo, err := initRepository(dbCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize repository: %w", err)
	}

	// Verify or apply the database schema.
	if err := handleInitialMigration(ctx, repo, logger); err != nil {
		repo.Close()
		return nil, fmt.Errorf("failed to verify or apply database schema: %w", err)
	}

	// Ensure the admin user setup or password reset logic is performed.
	if err := EnsureAdminUser(ctx, repo, logger); err != nil {
		repo.Close()
		return nil, fmt.Errorf("admin user initialization failed: %w", err)
	}

	return repo, nil
}

// initServices configures and starts up background tasks and media converters.
func initServices(ctx context.Context, cfg *config.Config, repo repository.Repository, storageProvider storage.StorageProvider, logger *slog.Logger) (*backgroundServices, error) {
	auditRetention, err := shared.ParseDuration(cfg.Logging.Audit.Retention)
	if err != nil {
		return nil, fmt.Errorf("failed to parse audit retention duration: %w", err)
	}

	hk := housekeeping.NewHouseKeeper(repo, storageProvider, logger, auditRetention)
	go hk.StartScheduler(ctx)

	converter, err := ffmpeg.NewFFMPEGConverter(cfg.Media.FFmpegPath, cfg.Media.FFprobePath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to start media converter: %w", err)
	}

	auditLogger := audit.NewAuditLogger(cfg.Logging.Audit.Enabled, cfg.Logging.Audit.Type, logger, repo)
	authMiddleware := auth.NewAuthMiddleware(repo, cfg.Auth.JWT.Secret)

	serverCfg, err := cfg.GetServerConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to parse server config: %w", err)
	}

	proc, err := processing.NewProcessor(repo, storageProvider, converter, serverCfg.NFfmpegAsync, serverCfg.NFfmpegTotal, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize processing manager: %w", err)
	}
	go proc.StartQueueChecker(ctx)

	return &backgroundServices{
		houseKeeper:    hk,
		mediaConverter: converter,
		auditLogger:    auditLogger,
		authMiddleware: authMiddleware,
		processor:      proc,
	}, nil
}

// buildHandlers configures the Handler layer with dependency injection.
func buildHandlers(cfg *config.Config, repo repository.Repository, storageProvider storage.StorageProvider, svcs *backgroundServices, logger *slog.Logger, startTime time.Time) (*httpserver.Handlers, error) {
	serverCfg, err := cfg.GetServerConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to parse server config: %w", err)
	}

	jwtCfg, err := cfg.GetJWTConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to parse JWT config: %w", err)
	}

	oidcCfg := th.OIDCConfig{
		Enabled:           cfg.Auth.OIDC.Enabled,
		DisableLoginPage:  cfg.Auth.OIDC.DisableLoginPage,
		DefaultUserRights: cfg.Auth.OIDC.DefaultUserRights,
		IssuerURL:         cfg.Auth.OIDC.IssuerURL,
		ClientID:          cfg.Auth.OIDC.ClientID,
		ClientSecret:      cfg.Auth.OIDC.ClientSecret,
		RedirectURL:       cfg.Auth.OIDC.RedirectURL,
	}
	var oidcProvider th.OIDCProvider
	if oidcCfg.Enabled {
		ValidateOIDCRedirectURL(logger, oidcCfg.RedirectURL)
		oidcProvider = th.NewHTTPOIDCProvider(oidcCfg, logger)
	}

	infoH := ih.NewInfoHandler(
		logger,
		svcs.auditLogger,
		docs.SwaggerInfo.Version,
		svcs.mediaConverter,
		cfg.Auth.OIDC.Enabled,
		cfg.Auth.OIDC.DisableLoginPage,
		cfg.Auth.OIDC.IssuerURL,
		cfg.Auth.OIDC.ClientID,
		cfg.Auth.OIDC.RedirectURL,
		oidcProvider,
		cfg.Logging.Audit.Enabled && cfg.Logging.Audit.Type == "database",
	)
	infoH.StartTime = startTime

	return &httpserver.Handlers{
		InfoHandler: *infoH,
		EntryHandler: eh.EntryHandler{
			Logger:                 logger,
			Auditor:                svcs.auditLogger,
			Repo:                   repo,
			Storage:                storageProvider,
			MaxSyncUploadSizeBytes: int64(serverCfg.MaxSyncUploadSize),
			MediaConverter:         svcs.mediaConverter,
			Processor:              svcs.processor,
		},
		DatabaseHandler: dbh.DatabaseHandler{
			Logger:      logger,
			Auditor:     svcs.auditLogger,
			Repo:        repo,
			Storage:     storageProvider,
			HouseKeeper: *svcs.houseKeeper,
		},
		UserHandler: uh.UserHandler{
			Logger:  logger,
			Auditor: svcs.auditLogger,
			Repo:    repo,
		},
		TokenHandler: th.TokenHandler{
			Logger:          logger,
			Auditor:         svcs.auditLogger,
			Repo:            repo,
			JWTSecret:       []byte(jwtCfg.Secret),
			AccessDuration:  jwtCfg.AccessDuration,
			RefreshDuration: jwtCfg.RefreshDuration,
			OIDCConfig:      oidcCfg,
			OIDCProvider:    oidcProvider,
		},
		AuditHandler: ah.AuditHandler{
			Logger: logger,
			Repo:   repo,
		},
	}, nil
}

// startServer configures the routing engine and binds the HTTP listener.
func startServer(cfg *config.Config, handlers *httpserver.Handlers, authMiddleware *auth.AuthMiddleware, frontendFS fs.FS, logger *slog.Logger) error {
	var fileSystem http.FileSystem
	if frontendFS != nil {
		// TODO: Update <base href> to the MEDIAHUB_SERVER_BASEPATH
		// or should we handle it later in SetupRouter?
		fileSystem = http.FS(frontendFS)
	}

	mux := httpserver.SetupRouter(handlers, fileSystem, authMiddleware, cfg.Server.Basepath, cfg.Server.CorsAllowedOrigins)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.Info("Starting HTTP server", "address", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server failed: %w", err)
	}

	return nil
}

// handleInitialMigration checks the database version and only auto-migrates if it is a completely fresh installation (version 0).
// If the database exists, it verifies that the schema matches the required version.
func handleInitialMigration(ctx context.Context, repo repository.Repository, logger *slog.Logger) error {
	version, err := repo.GetMigrationVersion(ctx)
	if err != nil {
		return fmt.Errorf("could not determine database version: %w", err)
	}

	if version == 0 {
		logger.Info("Fresh database installation detected. Automatically applying initial schema...")
		if err := repo.MigrateUp(ctx); err != nil {
			return fmt.Errorf("initial migration failed: %w", err)
		}
		logger.Info("Initial database schema applied successfully.")
	} else {
		logger.Info("Existing database detected.", "version", repository.FormatVersion(version))
		if err := migrations.CheckVersion(version); err != nil {
			return err
		}
	}

	return nil
}

// initRepository sets up the database connection based on the configuration.
func initRepository(dbCfg config.DatabaseConfig) (repository.Repository, error) {
	switch dbCfg.Driver {
	case "sqlite":
		return sqlite.NewRepository(dbCfg.Source)
	case "postgres":
		return postgres.NewRepository(dbCfg.Source, dbCfg.MaxOpenConns, dbCfg.MaxIdleConns)
	default:
		return nil, fmt.Errorf("unsupported database driver, must be `sqlite` or `postgres`: %s", dbCfg.Driver)
	}
}

// initStorage sets up the file storage provider based on the configuration.
func initStorage(storageCfg config.StorageConfig) (storage.StorageProvider, error) {
	switch storageCfg.Type {
	case "local":
		return &localstorage.LocalStorage{RootPath: storageCfg.Local.Root}, nil
	case "s3":
		s3prov, err := s3storage.NewS3StorageProvider(s3storage.Config{
			Endpoint:  storageCfg.S3.Endpoint,
			Region:    storageCfg.S3.Region,
			Bucket:    storageCfg.S3.Bucket,
			AccessKey: storageCfg.S3.AccessKey,
			SecretKey: storageCfg.S3.SecretKey,
			UseSSL:    storageCfg.S3.UseSSL,
		})
		if err != nil {
			return nil, err
		}
		return s3prov, nil
	default:
		return nil, fmt.Errorf("unsupported storage type, must be `local` or `s3`: %s", storageCfg.Type)
	}
}

// processInitConfig checks for the init_config flag and applies the one-time configuration if present.
func processInitConfig(ctx context.Context, repo repository.Repository, logger *slog.Logger) error {
	initConfPath := viper.GetString("init_config")
	if initConfPath == "" {
		return nil // No init config provided, skip gracefully
	}

	logger.Info("Found init_config flag, attempting to apply initialization data", "path", initConfPath)
	initConfigData, err := initconfig.ParseInitConfig(initConfPath)
	if err != nil {
		return fmt.Errorf("failed to parse init config: %w", err)
	}

	// Apply the configuration to the database
	if err := initconfig.Apply(ctx, &initConfigData, repo, logger, initConfPath); err != nil {
		return fmt.Errorf("failed to apply init config: %w", err)
	}

	return nil
}

// ValidateOIDCRedirectURL checks if the configured OIDC redirect URL ends in /auth/callback and logs a warning if not.
func ValidateOIDCRedirectURL(logger *slog.Logger, redirectURL string) bool {
	if redirectURL != "" && !strings.HasSuffix(strings.TrimRight(redirectURL, "/"), "/auth/callback") {
		if logger != nil {
			logger.Warn("OIDC redirect URL does not end in '/auth/callback', which may cause SSO login or callback handling to fail", "redirect_url", redirectURL)
		}
		return false
	}
	return true
}
