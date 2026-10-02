package processing

import (
	"context"
	"fmt"
	"os"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// handleLargeFileAsync processes a large file asynchronously using a reserved FFmpeg async slot.
func (p *Processor) handleLargeFileAsync(
	ctx context.Context,
	file *os.File,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	if !p.tryReserveAsyncSlot() {
		return repo.Entry{}, customerrors.ErrResourceExhausted
	}

	workerTempPath, err := claimTempFile(file)
	if err != nil {
		p.releaseAsyncSlot()
		return repo.Entry{}, err
	}

	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, repo.EntryStatusProcessing, false)
	if err != nil {
		p.releaseAsyncSlot()
		os.Remove(workerTempPath)
		return repo.Entry{}, err
	}

	go func() {
		defer p.releaseAsyncSlot()

		p.runConversionAndFinalize(context.Background(), db, createdEntry, workerTempPath, plan)
	}()

	return createdEntry, nil
}

// storeLargeFilePassthroughAsync copies a large file to storage asynchronously without reserving an FFmpeg conversion slot.
func (p *Processor) storeLargeFilePassthroughAsync(
	ctx context.Context,
	file *os.File,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	workerTempPath, err := claimTempFile(file)
	if err != nil {
		return repo.Entry{}, err
	}

	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, repo.EntryStatusProcessing, false)
	if err != nil {
		os.Remove(workerTempPath)
		return repo.Entry{}, err
	}

	go func() {
		p.runConversionAndFinalize(context.Background(), db, createdEntry, workerTempPath, plan)
	}()

	return createdEntry, nil
}

func claimTempFile(file *os.File) (string, error) {
	httpTempPath := file.Name()

	workerTempFile, err := os.CreateTemp(os.TempDir(), "mh-worker-*")
	if err != nil {
		return "", fmt.Errorf("failed to create worker temp file: %w", err)
	}
	workerTempPath := workerTempFile.Name()
	workerTempFile.Close()

	file.Close()
	if err := os.Rename(httpTempPath, workerTempPath); err != nil {
		os.Remove(workerTempPath)
		return "", fmt.Errorf("failed to claim temp file: %w", err)
	}
	return workerTempPath, nil
}
