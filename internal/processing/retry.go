package processing

import (
	"sync"
	"time"

	repo "mediahub_oss/internal/repository"
)

// This file implements per-entry requeue backoff for claimed queue entries.
//
// A claimed entry that fails transiently (e.g. storage briefly unavailable) is
// returned to the queue. Without backoff the dispatcher would immediately
// re-claim it, fail again, requeue again — a hot loop hammering storage and the
// database as fast as the failures return. The Processor therefore remembers
// when a failed entry may be claimed again and the dispatcher skips it until
// then; the periodic queue monitor retries afterwards.
//
// The backoff state is intentionally in-memory and per-node: it is a retry
// heuristic, not a correctness mechanism (the database remains the source of
// truth for entry status). Attempts grow exponentially up to
// retryMaxClaimDelay, and records are pruned once their delay has passed.

const (
	retryBaseClaimDelay = 30 * time.Second
	retryMaxClaimDelay  = 5 * time.Minute
)

// retryEntryKey uniquely identifies an entry across all databases.
type retryEntryKey struct {
	dbID    repo.ULID
	entryID int64
}

// claimRetry tracks how often an entry failed and when it may be claimed again.
type claimRetry struct {
	attempts int
	nextTry  time.Time
}

// retryState is guarded by mu; it is separate from the slot counters to keep
// the hot slot paths free of map bookkeeping.
type retryRegistry struct {
	mu     sync.Mutex
	claims map[retryEntryKey]claimRetry
}

func newRetryRegistry() *retryRegistry {
	return &retryRegistry{claims: make(map[retryEntryKey]claimRetry)}
}

// noteClaimFailure records a failed attempt for (dbID, entryID) and pushes its
// next claim time out exponentially (30s, 60s, 120s ... capped at 5 minutes).
func (rr *retryRegistry) noteClaimFailure(dbID repo.ULID, entryID int64) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	key := retryEntryKey{dbID: dbID, entryID: entryID}
	info := rr.claims[key]
	info.attempts++

	delay := retryBaseClaimDelay << (info.attempts - 1)
	if delay > retryMaxClaimDelay || delay <= 0 {
		delay = retryMaxClaimDelay
	}
	info.nextTry = time.Now().Add(delay)
	rr.claims[key] = info
}

// clearClaimRetry forgets the retry state of an entry (after successful
// processing or when it will never be retried).
func (rr *retryRegistry) clearClaimRetry(dbID repo.ULID, entryID int64) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	delete(rr.claims, retryEntryKey{dbID: dbID, entryID: entryID})
}

// claimDeferred reports whether the entry is still within its backoff window.
func (rr *retryRegistry) claimDeferred(dbID repo.ULID, entryID int64) bool {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	info, ok := rr.claims[retryEntryKey{dbID: dbID, entryID: entryID}]
	return ok && time.Now().Before(info.nextTry)
}

// prune drops records whose backoff window has passed, bounding memory use for
// entries that disappear without ever being claimed again.
func (rr *retryRegistry) prune() {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	now := time.Now()
	for id, info := range rr.claims {
		if !now.Before(info.nextTry) {
			delete(rr.claims, id)
		}
	}
}
