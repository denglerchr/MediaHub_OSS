package processing

import (
	"context"
	"fmt"
	"io"
	"os"

	repo "mediahub_oss/internal/repository"
)

func (p *Processor) queueLargeFile(
	ctx context.Context,
	file *os.File,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	httpTempPath := file.Name()

	workerTempFile, err := os.CreateTemp(os.TempDir(), "mh-worker-*")
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to create worker temp file: %w", err)
	}
	workerTempPath := workerTempFile.Name()
	workerTempFile.Close()

	file.Close()
	if err := os.Rename(httpTempPath, workerTempPath); err != nil {
		return repo.Entry{}, fmt.Errorf("failed to claim temp file: %w", err)
	}

	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, repo.EntryStatusQueued, false)
	if err != nil {
		os.Remove(workerTempPath)
		return repo.Entry{}, err
	}

	f, err := os.Open(workerTempPath)
	if err != nil {
		os.Remove(workerTempPath)
		return repo.Entry{}, fmt.Errorf("failed to open claimed file: %w", err)
	}

	fileSize, err := p.Storage.Write(ctx, db.ID.String(), createdEntry.ID, f)
	f.Close()
	os.Remove(workerTempPath)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to write file to storage: %w", err)
	}

	createdEntry.Size = uint64(fileSize)
	finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to update queued entry size: %w", err)
	}

	p.Logger.Debug("Successfully queued large file for async processing", "database_id", db.ID.String(), "entry_id", finalEntry.ID, "filename", finalEntry.FileName)
	return finalEntry, nil
}

func (p *Processor) queueSmallFile(
	ctx context.Context,
	file io.ReadSeeker,
	db repo.Database,
	req EntryRequest,
	plan ProcessingPlan,
) (repo.Entry, error) {
	createdEntry, err := p.createPreliminaryEntry(ctx, db, req, plan, repo.EntryStatusQueued, false)
	if err != nil {
		return repo.Entry{}, err
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return repo.Entry{}, fmt.Errorf("failed to seek file: %w", err)
	}

	fileSize, err := p.Storage.Write(ctx, db.ID.String(), createdEntry.ID, file)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to write file to storage: %w", err)
	}

	createdEntry.Size = uint64(fileSize)
	finalEntry, err := p.Repo.UpdateEntry(ctx, db.ID, createdEntry)
	if err != nil {
		return repo.Entry{}, fmt.Errorf("failed to update queued entry size: %w", err)
	}

	p.Logger.Debug("Successfully queued small file for processing", "database_id", db.ID.String(), "entry_id", finalEntry.ID, "filename", finalEntry.FileName)
	return finalEntry, nil
}

func (p *Processor) findNextQueuedEntry(ctx context.Context) (repo.Entry, repo.Database, bool, error) {
	databases, err := p.Repo.GetDatabases(ctx)
	if err != nil {
		return repo.Entry{}, repo.Database{}, false, err
	}

	for _, db := range databases {
		entries, err := p.Repo.GetEntriesByStatus(ctx, db.ID, repo.EntryStatusQueued, 1)
		if err != nil {
			return repo.Entry{}, repo.Database{}, false, err
		}
		if len(entries) > 0 {
			return entries[0], db, true, nil
		}
	}

	return repo.Entry{}, repo.Database{}, false, nil
}
