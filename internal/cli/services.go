package cli

import (
	"context"
	"fmt"
	"log/slog"
	"mediahub_oss/internal/cli/config"
	"mediahub_oss/internal/housekeeping"
	"mediahub_oss/internal/httpserver/auth"
	eh "mediahub_oss/internal/httpserver/entryhandler"
	"mediahub_oss/internal/logging/audit"
	"mediahub_oss/internal/media/ffmpeg"
	"mediahub_oss/internal/processing"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared"
	"mediahub_oss/internal/storage"
	"time"
)

// backgroundServices holds the initialized instances of all running background components.
type backgroundServices struct {
	houseKeeper    *housekeeping.HouseKeeper
	mediaConverter *ffmpeg.FfmpegConverter
	auditLogger    audit.AuditLogger
	authMiddleware *auth.AuthMiddleware
	processor      *processing.Processor
	importManager  *eh.ImportManager
}

// initServices validates configuration, initializes dependencies, and starts background workers.
func initServices(ctx context.Context, cfg *config.Config, repo repository.Repository, storageProvider storage.StorageProvider, logger *slog.Logger) (*backgroundServices, error) {
	auditRetention, err := shared.ParseDuration(cfg.Logging.Audit.Retention)
	if err != nil {
		return nil, fmt.Errorf("failed to parse audit retention duration: %w", err)
	}

	serverCfg, err := cfg.GetServerConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to parse server config: %w", err)
	}

	converter, err := ffmpeg.NewFFMPEGConverter(cfg.Media.FFmpegPath, cfg.Media.FFprobePath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to start media converter: %w", err)
	}

	hk := housekeeping.NewHouseKeeper(repo, storageProvider, logger, auditRetention)
	hk.StartScheduler(ctx)

	auditLogger := audit.NewAuditLogger(cfg.Logging.Audit.Enabled, cfg.Logging.Audit.Type, logger, repo)
	authMiddleware := auth.NewAuthMiddleware(repo, cfg.Auth.JWT.Secret)

	proc := processing.NewProcessor(repo, storageProvider, converter, serverCfg.NFfmpegAsync, serverCfg.NFfmpegTotal, logger)
	go proc.StartQueueMonitor(ctx)

	importManager := eh.NewImportManager(2)

	return &backgroundServices{
		houseKeeper:    hk,
		mediaConverter: converter,
		auditLogger:    auditLogger,
		authMiddleware: authMiddleware,
		processor:      proc,
		importManager:  importManager,
	}, nil
}

// closeServices gracefully shuts down running background services in dependency order.
func (s *backgroundServices) closeServices() {
	if s == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if s.importManager != nil {
		_ = s.importManager.Shutdown(shutdownCtx)
	}
	if s.processor != nil {
		_ = s.processor.Shutdown(shutdownCtx)
	}
	if s.houseKeeper != nil {
		_ = s.houseKeeper.Shutdown(shutdownCtx)
	}
	if s.mediaConverter != nil {
		_ = s.mediaConverter.Shutdown(shutdownCtx)
	}
	if s.authMiddleware != nil {
		s.authMiddleware.Close()
	}
}
