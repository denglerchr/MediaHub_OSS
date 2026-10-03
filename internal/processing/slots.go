package processing

import "context"

// This file contains the conversion slot accounting of the Processor.
//
// Two kinds of slots exist:
//   - Sync slots (HTTP request scope): count only against NFfmpegTotal.
//   - Async slots (background workers): count against both NFfmpegAsync and NFfmpegTotal.
//
// Releasing a slot after completed work triggers a queue dispatch pass if queued
// entries are waiting, so freed capacity is immediately reused. Rolling back a
// reservation (unreserve) never triggers a dispatch, because no work was done and
// no capacity was actually freed up for new work.
//
// Queue workers always consume async slots, regardless of the original file size
// of the queued entry (see AI/Concept/05_EntryService.md).

// hasFreeAsyncCapacityLocked reports whether a new async worker could be started.
// p.mu must be held by the caller.
func (p *Processor) hasFreeAsyncCapacityLocked() bool {
	return p.activeAsync < p.NFfmpegAsync && p.activeTotal < p.NFfmpegTotal
}

// tryReserveSyncSlot reserves a slot for a synchronous (request-scoped) conversion.
// Returns false if the total conversion limit is reached.
func (p *Processor) tryReserveSyncSlot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeTotal >= p.NFfmpegTotal {
		return false
	}
	p.activeTotal++
	return true
}

// releaseSyncSlot releases a synchronous conversion slot and triggers a queue
// dispatch pass if queued entries are waiting and async capacity is available.
func (p *Processor) releaseSyncSlot() {
	p.mu.Lock()
	p.activeTotal--
	shouldDispatch := p.hasQueuedEntries && p.hasFreeAsyncCapacityLocked()
	p.mu.Unlock()

	if shouldDispatch {
		go p.dispatchQueuedWorkers(context.Background())
	}
}

// tryReserveAsyncSlot reserves a slot for a background conversion.
// Returns false if either the async or the total conversion limit is reached.
func (p *Processor) tryReserveAsyncSlot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.hasFreeAsyncCapacityLocked() {
		return false
	}
	p.activeAsync++
	p.activeTotal++
	return true
}

// releaseAsyncSlot releases a background conversion slot after completed work and
// triggers a queue dispatch pass if queued entries are waiting.
func (p *Processor) releaseAsyncSlot() {
	p.mu.Lock()
	p.activeAsync--
	p.activeTotal--
	shouldDispatch := p.hasQueuedEntries && p.hasFreeAsyncCapacityLocked()
	p.mu.Unlock()

	if shouldDispatch {
		go p.dispatchQueuedWorkers(context.Background())
	}
}

// unreserveAsyncSlot rolls back a reservation made by tryReserveAsyncSlot without
// triggering a queue dispatch. It is used when a reserved slot turns out to be
// unusable (e.g. the targeted queued entry was claimed by another node first).
func (p *Processor) unreserveAsyncSlot() {
	p.mu.Lock()
	p.activeAsync--
	p.activeTotal--
	p.mu.Unlock()
}

// noteQueuedEntry records that an entry was placed in the processing queue and
// triggers a queue dispatch pass if async capacity is currently available.
func (p *Processor) noteQueuedEntry() {
	p.mu.Lock()
	p.hasQueuedEntries = true
	shouldDispatch := p.hasFreeAsyncCapacityLocked()
	p.mu.Unlock()

	if shouldDispatch {
		go p.dispatchQueuedWorkers(context.Background())
	}
}
