package processing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

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

	mu               sync.Mutex
	activeAsync      int
	activeTotal      int
	hasQueuedEntries bool
	isScanningQueue  atomic.Bool
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

	// Fast path: No conversion required (copy file + extract metadata/preview without reserving conversion slots)
	if !procPlan.NeedsConversion {
		if isLarge {
			entry, err := p.storeLargeFilePassthroughAsync(ctx, diskFile, db, req, procPlan)
			if err != nil {
				return repo.Entry{}, false, err
			}
			return entry, false, nil
		}
		entry, err := p.storeSmallFilePassthrough(ctx, file, db, req, procPlan)
		if err != nil {
			return repo.Entry{}, true, err
		}
		return entry, true, nil
	}

	var entry repo.Entry
	if isLarge {
		entry, err = p.handleLargeFileAsync(ctx, diskFile, db, req, procPlan)
		if err == nil {
			return entry, false, nil
		}
	} else {
		entry, err = p.handleSmallFileSync(ctx, file, db, req, procPlan)
		if err == nil {
			return entry, true, nil
		}
	}

	if !errors.Is(err, customerrors.ErrResourceExhausted) {
		return repo.Entry{}, !isLarge, err
	}

	// Limits reached, evaluate queue limit
	queuedCount, err := p.Repo.CountEntriesByStatus(ctx, db.ID, repo.EntryStatusQueued)
	if err != nil {
		return repo.Entry{}, false, fmt.Errorf("failed to count queued entries: %w", err)
	}

	if int(queuedCount) < db.NMaxQueued {
		p.Logger.Debug("Concurrency limit reached, queueing file", "database_id", db.ID.String(), "is_large", isLarge, "active_async", p.activeAsync, "active_total", p.activeTotal, "queued_count", queuedCount, "max_queued", db.NMaxQueued)
		entry, err := p.queueFile(ctx, file, db, req, procPlan)
		if err != nil {
			return repo.Entry{}, false, err
		}
		return entry, false, nil
	}

	p.Logger.Warn("Upload rejected: Concurrency limit reached and queue is full", "database_id", db.ID.String(), "is_large", isLarge, "active_async", p.activeAsync, "active_total", p.activeTotal, "queued_count", queuedCount, "max_queued", db.NMaxQueued)
	return repo.Entry{}, false, customerrors.ErrUnavailable
}

// markEntryQueued records that an entry has been placed in the queue and spawns a worker if a slot is currently free.
func (p *Processor) markEntryQueued() {
	p.mu.Lock()
	p.hasQueuedEntries = true
	shouldTrigger := p.activeAsync < p.NFfmpegAsync && p.activeTotal < p.NFfmpegTotal
	p.mu.Unlock()

	if shouldTrigger {
		go p.TriggerQueueWorkersIfPossible(context.Background())
	}
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

// releaseAsyncSlot releases a reserved asynchronous/large conversion slot and triggers a queue worker if entries are waiting.
func (p *Processor) releaseAsyncSlot() {
	p.mu.Lock()
	p.activeAsync--
	p.activeTotal--
	shouldTrigger := p.hasQueuedEntries && p.activeAsync < p.NFfmpegAsync && p.activeTotal < p.NFfmpegTotal
	p.mu.Unlock()

	if shouldTrigger {
		go p.TriggerQueueWorkersIfPossible(context.Background())
	}
}

// releaseAsyncSlotWithoutTrigger releases a reserved asynchronous slot without triggering a queue scan.
// Useful during dispatch when a claim race is lost and the slot is immediately rolled back.
func (p *Processor) releaseAsyncSlotWithoutTrigger() {
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

// releaseSyncSlot releases a reserved synchronous/small conversion slot and triggers a queue worker if entries are waiting.
func (p *Processor) releaseSyncSlot() {
	p.mu.Lock()
	p.activeTotal--
	shouldTrigger := p.hasQueuedEntries && p.activeAsync < p.NFfmpegAsync && p.activeTotal < p.NFfmpegTotal
	p.mu.Unlock()

	if shouldTrigger {
		go p.TriggerQueueWorkersIfPossible(context.Background())
	}
}


