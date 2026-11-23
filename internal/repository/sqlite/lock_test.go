package sqlite_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/shared/customerrors"
)

func TestSQLite_AcquireLock_Validation(t *testing.T) {
	ctx := context.Background()
	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	// Non-positive TTL must return validation error
	_, err = r.AcquireLock(ctx, "test_lock", "owner1", 0)
	if !errors.Is(err, customerrors.ErrValidation) {
		t.Fatalf("expected ErrValidation for 0 TTL, got: %v", err)
	}

	_, err = r.AcquireLock(ctx, "test_lock", "owner1", -1*time.Second)
	if !errors.Is(err, customerrors.ErrValidation) {
		t.Fatalf("expected ErrValidation for negative TTL, got: %v", err)
	}
}

func TestSQLite_AcquireAndReleaseLock(t *testing.T) {
	ctx := context.Background()
	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	lockName := "hk_db_1"
	owner1 := "worker-pod-1"
	owner2 := "worker-pod-2"

	// 1. Owner 1 acquires lock
	acq, err := r.AcquireLock(ctx, lockName, owner1, 5*time.Second)
	if err != nil || !acq {
		t.Fatalf("owner1 failed to acquire lock: acq=%v, err=%v", acq, err)
	}

	// 2. Owner 2 tries to acquire same lock -> fails
	acq, err = r.AcquireLock(ctx, lockName, owner2, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error when owner2 tries to acquire held lock: %v", err)
	}
	if acq {
		t.Fatalf("owner2 should NOT have acquired lock held by owner1")
	}

	// 3. Owner 1 extends/renews lock -> succeeds
	acq, err = r.AcquireLock(ctx, lockName, owner1, 10*time.Second)
	if err != nil || !acq {
		t.Fatalf("owner1 should be able to renew lock: acq=%v, err=%v", acq, err)
	}

	// 4. Owner 2 tries to release owner 1's lock -> fails (returns false)
	rel, err := r.ReleaseLock(ctx, lockName, owner2)
	if err != nil {
		t.Fatalf("unexpected error releasing lock: %v", err)
	}
	if rel {
		t.Fatalf("owner2 should NOT be able to release owner1's lock")
	}

	// 5. Owner 1 releases lock -> succeeds
	rel, err = r.ReleaseLock(ctx, lockName, owner1)
	if err != nil || !rel {
		t.Fatalf("owner1 failed to release lock: rel=%v, err=%v", rel, err)
	}

	// 6. Double release -> returns false
	rel, err = r.ReleaseLock(ctx, lockName, owner1)
	if err != nil {
		t.Fatalf("unexpected error on second release: %v", err)
	}
	if rel {
		t.Fatalf("releasing already-released lock should return false")
	}

	// 7. Owner 2 can now acquire lock
	acq, err = r.AcquireLock(ctx, lockName, owner2, 5*time.Second)
	if err != nil || !acq {
		t.Fatalf("owner2 should be able to acquire released lock: acq=%v, err=%v", acq, err)
	}
}

func TestSQLite_AcquireLock_Expiration(t *testing.T) {
	ctx := context.Background()
	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	lockName := "short_lived_lock"
	owner1 := "pod-1"
	owner2 := "pod-2"

	// Acquire with very short TTL
	acq, err := r.AcquireLock(ctx, lockName, owner1, 30*time.Millisecond)
	if err != nil || !acq {
		t.Fatalf("failed to acquire initial lock: %v", err)
	}

	// Wait for TTL to expire
	time.Sleep(50 * time.Millisecond)

	// Owner 2 should be able to acquire expired lock
	acq, err = r.AcquireLock(ctx, lockName, owner2, 1*time.Second)
	if err != nil || !acq {
		t.Fatalf("owner2 should acquire expired lock: acq=%v, err=%v", acq, err)
	}

	// Owner 1 tries to release -> fails because lock is now owned by owner 2
	rel, err := r.ReleaseLock(ctx, lockName, owner1)
	if err != nil {
		t.Fatalf("unexpected error on release: %v", err)
	}
	if rel {
		t.Fatalf("owner1 should not be able to release lock after being taken over by owner2")
	}

	// Owner 2 releases -> succeeds
	rel, err = r.ReleaseLock(ctx, lockName, owner2)
	if err != nil || !rel {
		t.Fatalf("owner2 failed to release lock: rel=%v, err=%v", rel, err)
	}
}

func TestSQLite_AcquireLock_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	_, err = r.AcquireLock(ctx, "lock1", "owner1", 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	_, err = r.ReleaseLock(ctx, "lock1", "owner1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestSQLite_AcquireLock_Concurrency(t *testing.T) {
	ctx := context.Background()
	r, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer r.Close()

	lockName := "concurrent_lock"
	var successCount int64

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			ownerID := "worker-" + string(rune('A'+id))
			acq, err := r.AcquireLock(ctx, lockName, ownerID, 5*time.Second)
			if err == nil && acq {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 worker to acquire the lock concurrently, got %d", successCount)
	}
}
