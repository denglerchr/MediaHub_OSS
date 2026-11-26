package recovery

import (
	"context"
	"fmt"
	"log/slog"

	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/storage"
)

// RecoveryService handles maintenance tasks to fix data inconsistencies.
type RecoveryService struct {
	repo    repository.Repository
	storage storage.StorageProvider
	logger  *slog.Logger
	dryRun  bool
}

// NewRecoveryService constructs a RecoveryService with pre-initialized dependencies.
func NewRecoveryService(repo repository.Repository, storage storage.StorageProvider, logger *slog.Logger, dryRun bool) *RecoveryService {
	return &RecoveryService{
		repo:    repo,
		storage: storage,
		logger:  logger,
		dryRun:  dryRun,
	}
}

// GetMigrationVersion retrieves the current database schema version.
func (s *RecoveryService) GetMigrationVersion(ctx context.Context) (int, error) {
	if s.repo == nil {
		return 0, fmt.Errorf("repository is not initialized")
	}
	return s.repo.GetMigrationVersion(ctx)
}

const recoveryDeleteBatchSize = 500

func chunkIDs(ids []int64, size int) [][]int64 {
	if len(ids) == 0 {
		return nil
	}
	if size <= 0 {
		size = recoveryDeleteBatchSize
	}
	chunks := make([][]int64, 0, (len(ids)+size-1)/size)
	for start := 0; start < len(ids); start += size {
		end := min(start+size, len(ids))
		chunks = append(chunks, ids[start:end])
	}
	return chunks
}
