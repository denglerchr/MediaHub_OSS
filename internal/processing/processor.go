package processing

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/storage"
)

type EntryRequest struct {
	Timestamp    int64
	FileName     string
	CustomFields map[string]any
}

type Processor struct {
	Repo           repo.Repository
	Storage        storage.StorageProvider
	MediaConverter media.MediaConverter
	NFfmpegAsync   int
	NFfmpegTotal   int
	Logger         *slog.Logger

	mu          sync.Mutex
	activeAsync int
	activeTotal int
}

func NewProcessor(
	repository repo.Repository,
	store storage.StorageProvider,
	converter media.MediaConverter,
	nFfmpegAsync int,
	nFfmpegTotal int,
	logger *slog.Logger,
) (*Processor, error) {
	return &Processor{
		Repo:           repository,
		Storage:        store,
		MediaConverter: converter,
		NFfmpegAsync:   nFfmpegAsync,
		NFfmpegTotal:   nFfmpegTotal,
		Logger:         logger,
	}, nil
}

// ProcessEntry is the main entry point to evaluate limits and route files for processing.
func (p *Processor) ProcessEntry(
	ctx context.Context,
	db repo.Database,
	req EntryRequest,
	file io.ReadSeeker,
	originalMimeType string,
	originalFileName string,
) (repo.Entry, bool, error) {
	procPlan, err := DetermineConversionPlan(p.MediaConverter, db, originalMimeType, originalFileName, req.FileName)
	if err != nil {
		return repo.Entry{}, false, err
	}

	var isLarge bool
	var diskFile *os.File
	if f, ok := file.(*os.File); ok {
		isLarge = true
		diskFile = f
	}

	if isLarge {
		// Path A: Large File, Asynchronous
		if p.tryReserveAsyncSlot() {
			entry, err := p.handleLargeFileAsync(ctx, diskFile, db, req, procPlan)
			if err != nil {
				p.releaseAsyncSlot()
				return repo.Entry{}, false, err
			}
			return entry, false, nil
		}

		// Limits reached, evaluate queue limit
		queuedCount, err := p.Repo.CountEntriesByStatus(ctx, db.ID, repo.EntryStatusQueued)
		if err != nil {
			return repo.Entry{}, false, fmt.Errorf("failed to count queued entries: %w", err)
		}

		if int(queuedCount) < db.NMaxQueued {
			p.Logger.Debug("Concurrency limit reached, queueing large file", "database_id", db.ID.String(), "active_async", p.activeAsync, "active_total", p.activeTotal, "queued_count", queuedCount, "max_queued", db.NMaxQueued)
			entry, err := p.queueLargeFile(ctx, diskFile, db, req, procPlan)
			if err != nil {
				return repo.Entry{}, false, err
			}
			p.tryAcquireAndSpawn(context.Background(), db, entry)
			return entry, false, nil
		}

		p.Logger.Warn("Upload rejected: Concurrency limit reached and queue is full", "database_id", db.ID.String(), "active_async", p.activeAsync, "active_total", p.activeTotal, "queued_count", queuedCount, "max_queued", db.NMaxQueued)
		return repo.Entry{}, false, customerrors.ErrUnavailable
	}

	// Path B: Small File, Synchronous
	if p.tryReserveSyncSlot() {
		defer func() {
			p.releaseSyncSlot()
			p.TriggerQueueWorkersIfPossible(context.Background())
		}()

		entry, err := p.handleSmallFileSync(ctx, file, db, req, procPlan)
		if err != nil {
			return repo.Entry{}, true, err
		}
		return entry, true, nil
	}

	// Limits reached, evaluate queue limit
	queuedCount, err := p.Repo.CountEntriesByStatus(ctx, db.ID, repo.EntryStatusQueued)
	if err != nil {
		return repo.Entry{}, false, fmt.Errorf("failed to count queued entries: %w", err)
	}

	if int(queuedCount) < db.NMaxQueued {
		p.Logger.Debug("Concurrency limit reached, queueing small file", "database_id", db.ID.String(), "active_total", p.activeTotal, "queued_count", queuedCount, "max_queued", db.NMaxQueued)
		entry, err := p.queueSmallFile(ctx, file, db, req, procPlan)
		if err != nil {
			return repo.Entry{}, false, err
		}
		p.tryAcquireAndSpawn(context.Background(), db, entry)
		return entry, false, nil
	}

	p.Logger.Warn("Upload rejected: Concurrency limit reached and queue is full", "database_id", db.ID.String(), "active_total", p.activeTotal, "queued_count", queuedCount, "max_queued", db.NMaxQueued)
	return repo.Entry{}, false, customerrors.ErrUnavailable
}

// tryReserveAsyncSlot checks limits and reserves a slot for an asynchronous/large conversion.
func (p *Processor) tryReserveAsyncSlot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeAsync >= p.NFfmpegAsync || p.activeTotal >= p.NFfmpegTotal {
		return false
	}
	p.activeAsync++
	p.activeTotal++
	return true
}

// releaseAsyncSlot releases a reserved asynchronous/large conversion slot.
func (p *Processor) releaseAsyncSlot() {
	p.mu.Lock()
	p.activeAsync--
	p.activeTotal--
	p.mu.Unlock()
}

// tryReserveSyncSlot checks limits and reserves a slot for a synchronous/small conversion.
func (p *Processor) tryReserveSyncSlot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeTotal >= p.NFfmpegTotal {
		return false
	}
	p.activeTotal++
	return true
}

// TryReserveSyncSlot checks limits and reserves a slot for a synchronous/small conversion.
func (p *Processor) TryReserveSyncSlot() bool {
	return p.tryReserveSyncSlot()
}

// releaseSyncSlot releases a reserved synchronous/small conversion slot.
func (p *Processor) releaseSyncSlot() {
	p.mu.Lock()
	p.activeTotal--
	p.mu.Unlock()
}

// ReleaseSyncSlot releases a reserved synchronous/small conversion slot.
func (p *Processor) ReleaseSyncSlot() {
	p.releaseSyncSlot()
}
