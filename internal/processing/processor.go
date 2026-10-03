// Package processing orchestrates the two media processing pipelines of MediaHub:
//
//   - Incoming entries (uploads): ProcessIncomingEntry routes an uploaded file to
//     synchronous in-memory processing (small files), asynchronous background
//     processing (large files spooled to disk by the HTTP layer), or to the
//     database-backed queue when all conversion slots are occupied.
//   - Outgoing entries (downloads): ProcessOutgoingEntry converts a stored entry
//     on the fly, holding a conversion slot until the returned stream is closed.
//
// Conversion concurrency is bounded by two limits: NFfmpegTotal caps all active
// conversions, while NFfmpegAsync additionally caps background conversions.
// Preview generation is exempt from these limits. Queued entries are drained by
// background workers whenever async capacity becomes available (see worker.go).
package processing

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"mediahub_oss/internal/media"
	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/storage"
)

// EntryRequest carries the user-supplied metadata of an incoming entry upload.
type EntryRequest struct {
	Timestamp    int64
	FileName     string
	CustomFields map[string]any
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
	return &Processor{
		Repo:           repository,
		Storage:        store,
		MediaConverter: converter,
		Logger:         logger,
		NFfmpegAsync:   nFfmpegAsync,
		NFfmpegTotal:   nFfmpegTotal,
	}
}
