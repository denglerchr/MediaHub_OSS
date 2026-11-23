package sqlite

import (
	"context"
	"fmt"
	"time"

	"mediahub_oss/internal/shared/customerrors"
)

// AcquireLock attempts to acquire an in-memory lock for SQLite with TTL expiration and owner tracking.
func (r *SQLiteRepository) AcquireLock(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	if ttl <= 0 {
		return false, fmt.Errorf("%w: lock ttl must be positive", customerrors.ErrValidation)
	}

	r.locksMu.Lock()
	defer r.locksMu.Unlock()

	if r.locks == nil {
		r.locks = make(map[string]lockEntry)
	}

	now := time.Now()
	entry, exists := r.locks[lockName]
	if exists && now.Before(entry.expiresAt) {
		if entry.ownerID == ownerID {
			// Lock held by the same owner; renew expiration
			r.locks[lockName] = lockEntry{
				ownerID:   ownerID,
				expiresAt: now.Add(ttl),
			}
			return true, nil
		}
		// Lock held by a different owner and still active
		return false, nil
	}

	// Lock either does not exist or has expired
	r.locks[lockName] = lockEntry{
		ownerID:   ownerID,
		expiresAt: now.Add(ttl),
	}
	return true, nil
}

// ReleaseLock releases an in-memory lock if the owner matches and it has not expired.
func (r *SQLiteRepository) ReleaseLock(ctx context.Context, lockName string, ownerID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	r.locksMu.Lock()
	defer r.locksMu.Unlock()

	if r.locks == nil {
		return false, nil
	}

	entry, exists := r.locks[lockName]
	if !exists {
		return false, nil
	}

	if entry.ownerID != ownerID {
		return false, nil
	}

	now := time.Now()
	if !now.Before(entry.expiresAt) {
		// Lock expired before release
		delete(r.locks, lockName)
		return false, nil
	}

	delete(r.locks, lockName)
	return true, nil
}
