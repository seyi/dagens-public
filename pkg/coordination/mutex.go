package coordination

import (
	"context"
	"sync"
	"time"

	"go.etcd.io/etcd/client/v3/concurrency"
)

// DistributedMutex provides distributed mutual exclusion using etcd.
// It wraps etcd's concurrency.Mutex with local serialization to prevent
// concurrent Lock() calls from the same session (which etcd allows as re-entrant).
// This mutex is reusable - it can be locked/unlocked multiple times.
type DistributedMutex struct {
	session *concurrency.Session
	key     string
	mu      sync.Mutex // Protects local lock state
	mutex   *concurrency.Mutex
	locked  bool
	waitCh  chan struct{}
}

// NewDistributedMutex creates a new distributed mutex.
func NewDistributedMutex(session *concurrency.Session, key string) *DistributedMutex {
	return &DistributedMutex{
		session: session,
		key:     key,
		mutex:   concurrency.NewMutex(session, key),
		locked:  false,
		waitCh:  nil,
	}
}

// Lock acquires the distributed lock.
// Blocks until the lock is available or context is cancelled.
// Uses local serialization to ensure only ONE caller can hold the lock at a time,
// even when using the same etcd session (which normally allows re-entrant locking).
func (dm *DistributedMutex) Lock(ctx context.Context) error {
	for {
		dm.mu.Lock()
		if !dm.locked {
			dm.locked = true
			dm.waitCh = make(chan struct{})
			dm.mu.Unlock()
			break
		}
		waitCh := dm.waitCh
		dm.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-waitCh:
		}
	}

	// We own the local slot; now acquire the distributed lock.
	err := dm.mutex.Lock(ctx)
	if err != nil {
		dm.mu.Lock()
		if dm.locked {
			dm.locked = false
			if dm.waitCh != nil {
				close(dm.waitCh)
				dm.waitCh = nil
			}
		}
		dm.mu.Unlock()
		return err
	}

	return nil
}

// Unlock releases the distributed lock.
// The mutex can be re-acquired after unlocking (reusable).
func (dm *DistributedMutex) Unlock(ctx context.Context) error {
	dm.mu.Lock()
	if !dm.locked {
		dm.mu.Unlock()
		return nil // Don't hold the lock
	}
	dm.mu.Unlock()

	err := dm.mutex.Unlock(ctx)
	if err != nil {
		return err
	}

	dm.mu.Lock()
	if dm.locked {
		dm.locked = false
		if dm.waitCh != nil {
			close(dm.waitCh)
			dm.waitCh = nil
		}
	}
	dm.mu.Unlock()

	return nil
}

// TryLock attempts to acquire the lock without blocking.
// Returns true if lock was acquired, false otherwise.
// Does NOT allow re-entrant locking - returns false if already held.
func (dm *DistributedMutex) TryLock(ctx context.Context) (bool, error) {
	dm.mu.Lock()
	if dm.locked {
		dm.mu.Unlock()
		return false, nil
	}
	dm.locked = true
	dm.waitCh = make(chan struct{})
	dm.mu.Unlock()

	// If we already hold the lock, do NOT allow re-entrant TryLock
	// Try to acquire etcd lock with timeout
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	err := dm.mutex.Lock(shortCtx)
	if err != nil {
		dm.mu.Lock()
		if dm.locked {
			dm.locked = false
			if dm.waitCh != nil {
				close(dm.waitCh)
				dm.waitCh = nil
			}
		}
		dm.mu.Unlock()

		if shortCtx.Err() == context.DeadlineExceeded {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// IsLocked returns whether this instance currently holds the lock.
func (dm *DistributedMutex) IsLocked() bool {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.locked
}

// IsOwner returns whether this instance currently holds the lock.
// Deprecated: Use IsLocked() instead.
func (dm *DistributedMutex) IsOwner() bool {
	return dm.IsLocked()
}
