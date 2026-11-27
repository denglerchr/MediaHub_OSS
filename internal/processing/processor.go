// Package processing orchestrates the two media processing pipelines of MediaHub:
//
//   - Incoming entries (uploads): ProcessIncomingEntry routes an uploaded file to
//     synchronous in-memory processing (small files), asynchronous background
//     processing (large files spooled to disk by the HTTP layer), or to the
//     database-backed queue when all conversion slots are occupied.
//   - Outgoing entries (downloads): ProcessOutgoingEntry converts a stored entry
//     on the fly, holding a conversion slot until the returned stream is closed.
//
// Conversion and preview generation concurrency is bounded by two limits:
// NFfmpegTotal caps all active processing operations, while NFfmpegAsync
// additionally caps background operations. Pure passthrough uploads (neither
// conversion nor preview generation) are exempt from these limits. Queued
// entries are drained by background workers whenever async capacity becomes
// available (see worker.go).
package processing

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/storage"
)

// EntryRequest carries the user-supplied metadata of an incoming entry upload.
type EntryRequest struct {
	Timestamp     int64
	FileName      string
	CustomFields  map[string]any
	CustomPreview []byte
}

// Processor coordinates media conversions, enforces the configured concurrency
// limits, and manages the database-backed processing queue.
type Processor struct {
	Repo           repo.Repository
	Storage        storage.StorageProvider
	MediaConverter media.MediaConverter
	Logger         *slog.Logger

	// NFfmpegAsync limits the number of concurrently running background conversions.
	NFfmpegAsync int
	// NFfmpegTotal limits the total number of concurrently running conversions
	// (synchronous and background combined).
	NFfmpegTotal int

	// mu guards the slot counters and the hasQueuedEntries hint below.
	mu               sync.Mutex
	activeAsync      int
	activeTotal      int
	hasQueuedEntries bool // local hint, re-synced from the database on every dispatch pass

	// isScanningQueue ensures only one queue dispatch pass runs at a time.
	isScanningQueue atomic.Bool
	// needsRescan indicates another trigger occurred while a dispatch pass was in flight.
	needsRescan atomic.Bool

	// retries tracks per-entry backoff for queued entries that failed during
	// processing, so transient failures cannot cause a hot claim/requeue loop.
	retries *retryRegistry

	workerCtx    context.Context
	workerCancel context.CancelFunc
	wg           sync.WaitGroup
	shuttingDown atomic.Bool
}

// NewProcessor creates a Processor enforcing the given conversion concurrency limits.
func NewProcessor(
	repository repo.Repository,
	store storage.StorageProvider,
	converter media.MediaConverter,
	nFfmpegAsync int,
	nFfmpegTotal int,
	logger *slog.Logger,
) *Processor {
	workerCtx, workerCancel := context.WithCancel(context.Background())
	return &Processor{
		Repo:           repository,
		Storage:        store,
		MediaConverter: converter,
		Logger:         logger,
		NFfmpegAsync:   nFfmpegAsync,
		NFfmpegTotal:   nFfmpegTotal,
		retries:        newRetryRegistry(),
		workerCtx:      workerCtx,
		workerCancel:   workerCancel,
	}
}

func (p *Processor) workerContext() context.Context {
	if p.workerCtx != nil {
		return p.workerCtx
	}
	return context.Background()
}

func (p *Processor) isShuttingDown() bool {
	return p.shuttingDown.Load()
}

// tryTrackWorker registers a background goroutine in p.wg if the processor is
// not shutting down. Holding p.mu around the check and wg.Add prevents racing
// with Shutdown's wg.Wait.
func (p *Processor) tryTrackWorker() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shuttingDown.Load() {
		return false
	}
	p.wg.Add(1)
	return true
}

// Shutdown stops dispatching new background work, cancels the worker context,
// and waits for active background goroutines to exit or until ctx expires.
func (p *Processor) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.shuttingDown.Store(true)
	p.mu.Unlock()

	if p.workerCancel != nil {
		p.workerCancel()
	}

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
