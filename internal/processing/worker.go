package processing

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"time"

	repo "mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// queueMonitorInterval is the interval at which the background queue monitor
// re-scans the databases for queued entries.
const queueMonitorInterval = 15 * time.Second

// dispatchBatchSize is the minimum batch size fetched per database query when
// scanning for queued entries.
const dispatchBatchSize = 50

// StartQueueMonitor periodically checks for queued entries across all databases
// and spawns background workers if concurrency limits allow.
// It runs an initial check at startup and continues on a fixed interval until
// ctx is cancelled. It is intended to run once per node for the lifetime of the
// process and must be called as a goroutine.
func (p *Processor) StartQueueMonitor(ctx context.Context) {
	if !p.tryTrackWorker() {
		return
	}
	defer p.wg.Done()

	workerCtx := p.workerContext()
	monitorCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopAfter := context.AfterFunc(workerCtx, cancel)
	defer stopAfter()

	if monitorCtx.Err() != nil {
		return
	}

	p.Logger.Info("Starting background queue monitor to scan for queued entries...")

	// Initial scan at startup
	p.dispatchQueuedWorkers(monitorCtx)

	ticker := time.NewTicker(queueMonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.Logger.Info("Stopping background queue monitor")
			return
		case <-workerCtx.Done():
			p.Logger.Info("Stopping background queue monitor")
			return
		case <-ticker.C:
			p.dispatchQueuedWorkers(monitorCtx)
		}
	}
}

// claimOutcome describes the result of trying to claim one queued entry.
type claimOutcome int

const (
	// claimWon: the entry was claimed and a worker was spawned for it.
	claimWon claimOutcome = iota
	// claimLost: another node won the claim race; the dispatch pass may continue
	// with the next candidate.
	claimLost
	// claimAborted: no capacity left or a fatal error occurred; the dispatch pass
	// must stop.
	claimAborted
)

// dispatchQueuedWorkers scans all databases with queued entries (tracked via the
// denormalized databases.queued_count column) and spawns background workers in
// parallel up to the available async conversion capacity. It re-syncs the local
// hasQueuedEntries hint from the database on every pass.
//
// A concurrency guard ensures only one dispatch pass runs at a time to prevent
// dispatch stampedes when many slots are released concurrently. Triggers that
// arrive while a pass is running are recorded in needsRescan and cause exactly one
// follow-up pass before the guard is handed off, so no trigger is ever lost.
func (p *Processor) dispatchQueuedWorkers(ctx context.Context) {
	if p.isShuttingDown() || ctx.Err() != nil {
		return
	}

	p.needsRescan.Store(true)
	if !p.isScanningQueue.CompareAndSwap(false, true) {
		return
	}

	// ownsGuard records whether this goroutine still holds the scan guard, so the
	// deferred cleanup never clears a guard that has already been released to —
	// and possibly re-acquired by — another goroutine.
	ownsGuard := true
	defer func() {
		if ownsGuard {
			p.isScanningQueue.Store(false)
		}
	}()

	for {
		if p.isShuttingDown() || ctx.Err() != nil {
			return
		}

		p.needsRescan.Store(false)

		if p.runDispatchPass(ctx) && !p.isShuttingDown() && ctx.Err() == nil {
			// The pass spawned workers and stopped with capacity to spare, so more
			// queued entries may be dispatchable immediately.
			continue
		}

		// End of pass: release the guard, then re-check needsRescan. A trigger that
		// arrived during the pass set the flag after its own guard CAS failed;
		// re-acquiring the guard here makes sure that trigger is never lost.
		p.isScanningQueue.Store(false)
		ownsGuard = false
		if !p.isShuttingDown() && ctx.Err() == nil && p.needsRescan.Load() && p.isScanningQueue.CompareAndSwap(false, true) {
			ownsGuard = true
			continue
		}
		return
	}
}

// runDispatchPass performs a single scan of all databases with queued entries and
// spawns workers up to the available async capacity. It reports whether more work
// is plausibly dispatchable right now: the pass spawned at least one worker and
// stopped with capacity to spare. A false result ends the dispatch loop unless a
// concurrent trigger requested a rescan.
func (p *Processor) runDispatchPass(ctx context.Context) bool {
	if p.isShuttingDown() || ctx.Err() != nil {
		return false
	}

	// Bound the requeue backoff registry before scanning.
	p.retries.prune()

	dbIDs, err := p.Repo.GetDatabaseULIDsWithQueuedEntries(ctx)
	if err != nil {
		p.Logger.Error("Queue dispatch: Failed to query databases with queued entries", "error", err)
		return false
	}

	p.mu.Lock()
	p.hasQueuedEntries = len(dbIDs) > 0
	freeAsync := p.NFfmpegAsync - p.activeAsync
	freeTotal := p.NFfmpegTotal - p.activeTotal
	p.mu.Unlock()

	remaining := min(freeAsync, freeTotal)
	if len(dbIDs) == 0 || remaining <= 0 {
		// Nothing queued or no capacity: queue inserts and slot releases trigger
		// their own dispatch pass, so re-scanning here would be redundant.
		return false
	}

	spawned := 0
	for _, dbID := range dbIDs {
		if remaining <= 0 || p.isShuttingDown() || ctx.Err() != nil {
			break
		}

		dbSpawned, aborted := p.dispatchDatabaseQueue(ctx, dbID, remaining)
		spawned += dbSpawned
		remaining -= dbSpawned
		if aborted {
			return false
		}
	}

	return spawned > 0 && remaining > 0
}

func (p *Processor) dispatchDatabaseQueue(ctx context.Context, dbID repo.ULID, remaining int) (spawned int, aborted bool) {
	var nextIDStart int64
	for remaining > 0 && !p.isShuttingDown() && ctx.Err() == nil {
		limit := max(dispatchBatchSize, remaining)

		entries, err := p.Repo.GetEntries(ctx, dbID, repo.QueryOptions{
			Statuses: []repo.EntryStatus{repo.EntryStatusQueued},
			SortBy:   "id",
			Order:    "asc",
			Limit:    limit,
			IDStart:  nextIDStart,
		})
		if err != nil {
			p.Logger.Error("Queue dispatch: Failed to get queued entries", "database_id", dbID.String(), "error", err)
			return spawned, false
		}
		if len(entries) == 0 {
			return spawned, false
		}

		var lastID int64
		for _, entry := range entries {
			lastID = entry.ID
			if remaining <= 0 || p.isShuttingDown() || ctx.Err() != nil {
				break
			}
			switch p.tryClaimAndSpawn(ctx, dbID, entry) {
			case claimWon:
				spawned++
				remaining--
			case claimLost:
				// Another node claimed the entry or retry backoff deferred it;
				// try the next candidate.
			case claimAborted:
				// No capacity left or a fatal claim error; stop this pass.
				return spawned, true
			}
		}

		if len(entries) < limit || lastID == math.MaxInt64 {
			return spawned, false
		}
		nextIDStart = lastID + 1
	}
	return spawned, false
}

// tryClaimAndSpawn reserves an async slot, atomically claims the queued entry via
// an optimistic concurrency control update, and spawns the background worker for
// it. The slot reservation is rolled back if the claim fails or is lost to
// another node, without triggering cascading dispatch passes.
func (p *Processor) tryClaimAndSpawn(ctx context.Context, dbID repo.ULID, entry repo.Entry) claimOutcome {
	if p.isShuttingDown() || ctx.Err() != nil {
		return claimAborted
	}

	// Skip entries that recently failed and are still within their requeue
	// backoff window; the dispatch pass moves on to the next candidate instead
	// of hot-looping on the same broken entry.
	if p.retries.claimDeferred(dbID, entry.ID) {
		return claimLost
	}

	if !p.tryReserveAsyncSlot() {
		return claimAborted
	}

	claimed, err := p.Repo.ClaimQueuedEntry(ctx, dbID, entry.ID)
	if err != nil {
		p.Logger.Error("Queue dispatch: Failed to claim queued entry", "database_id", dbID.String(), "entry_id", entry.ID, "error", err)
		p.unreserveAsyncSlot()
		return claimAborted
	}
	if !claimed {
		p.unreserveAsyncSlot()
		return claimLost
	}

	if !p.tryTrackWorker() {
		p.unreserveAsyncSlot()
		p.requeueClaimedEntry(ctx, dbID, entry)
		return claimAborted
	}

	p.Logger.Debug("Spawning background worker for claimed entry", "database_id", dbID.String(), "entry_id", entry.ID)
	workerCtx := p.workerContext()
	go func() {
		defer p.wg.Done()
		defer p.releaseAsyncSlot()
		p.runClaimedEntry(workerCtx, dbID, entry)
	}()
	return claimWon
}

// runClaimedEntry executes the background processing of a previously claimed
// queued entry: it fetches the stored original file into a local temp file and
// runs the finalization pipeline on it.
//
// Transient failures (e.g. storage temporarily unavailable) requeue the entry for
// a later attempt; content failures (e.g. a missing file) mark the entry as
// erroneous instead.
func (p *Processor) runClaimedEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) {
	db, err := p.Repo.GetDatabase(ctx, dbID)
	if err != nil {
		p.Logger.Error("Worker: Failed to retrieve database for claimed entry", "database_id", dbID.String(), "entry_id", entry.ID, "error", err)
		p.requeueClaimedEntry(ctx, dbID, entry)
		return
	}

	tempFile, err := os.CreateTemp(os.TempDir(), "mh-worker-queued-*")
	if err != nil {
		p.Logger.Error("Worker: Failed to create temp file for queued entry", "entry", entry.ID, "error", err)
		p.requeueClaimedEntry(ctx, dbID, entry)
		return
	}
	tempFilePath := tempFile.Name()
	// finalizeEntryFile removes the temp file; this is only the failure-path cleanup.
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			os.Remove(tempFilePath)
		}
	}()

	stream, err := p.Storage.Read(ctx, dbID, entry.ID, 0, -1)
	if err != nil {
		tempFile.Close()
		p.Logger.Error("Worker: Failed to read queued file from storage", "entry", entry.ID, "error", err)
		if ctx.Err() != nil || p.isShuttingDown() || !errors.Is(err, customerrors.ErrNotFound) {
			p.requeueClaimedEntry(ctx, dbID, entry)
		} else {
			p.failProcessedEntry(ctx, dbID, entry)
		}
		return
	}

	_, copyErr := io.Copy(tempFile, stream)
	stream.Close()
	closeErr := tempFile.Close()

	if copyErr != nil || closeErr != nil {
		p.Logger.Error("Worker: Failed to copy queued file to temp path", "entry", entry.ID, "copy_error", copyErr, "close_error", closeErr)
		p.requeueClaimedEntry(ctx, dbID, entry)
		return
	}

	if ctx.Err() != nil || p.isShuttingDown() {
		p.requeueClaimedEntry(ctx, dbID, entry)
		return
	}

	plan := DeterminePlanForEntry(p.MediaConverter, db, entry)
	cleanupTemp = false // ownership handed over to the finalization pipeline
	// Whether the entry was processed successfully or marked erroneous, it will
	// not be claimed again — drop any backoff state.
	_ = p.finalizeEntryFile(ctx, db, entry, tempFilePath, plan)
	p.retries.clearClaimRetry(dbID, entry.ID)
}

// requeueClaimedEntry returns a claimed entry to the queue (e.g. after a transient
// failure) so it can be picked up again later. The entry enters a per-node
// backoff window (see retry.go) so repeated failures cannot spin the dispatcher.
func (p *Processor) requeueClaimedEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()

	entry.Status = repo.EntryStatusQueued
	if _, err := p.Repo.UpdateEntry(cleanupCtx, dbID, entry); err != nil {
		p.Logger.Error("Worker: CRITICAL: Failed to requeue claimed entry", "entry", entry.ID, "error", err)
		return
	}
	if p.isShuttingDown() || ctx.Err() != nil {
		return
	}
	p.retries.noteClaimFailure(dbID, entry.ID)
	p.noteQueuedEntry()
}

// failProcessedEntry marks an entry processed by a background worker as erroneous.
// The original file in storage is preserved for inspection and potential recovery;
// only a possibly partially generated preview is removed.
func (p *Processor) failProcessedEntry(ctx context.Context, dbID repo.ULID, entry repo.Entry) {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()

	_ = p.Storage.DeletePreview(cleanupCtx, dbID, entry.ID)
	entry.PreviewSize = 0
	entry.Status = repo.EntryStatusError
	p.retries.clearClaimRetry(dbID, entry.ID)
	if _, err := p.Repo.UpdateEntry(cleanupCtx, dbID, entry); err != nil {
		if !errors.Is(err, customerrors.ErrNotFound) {
			p.Logger.Error("Worker: CRITICAL: Failed to set status error", "entry", entry.ID, "error", err)
		}
	}
}
