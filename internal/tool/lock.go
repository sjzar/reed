package tool

import (
	"context"
	"sort"
	"sync"
)

// LockScheduler provides keyed read/write locking with deadlock prevention.
type LockScheduler struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	readers int
	writer  bool
	waiters []waiter
}

type waiter struct {
	mode LockMode
	ch   chan struct{}
}

// NewLockScheduler creates a new LockScheduler.
func NewLockScheduler() *LockScheduler {
	return &LockScheduler{locks: make(map[string]*keyLock)}
}

// Acquire acquires all requested locks atomically.
// Deadlock prevention: locks are sorted by key, duplicates merged, read+write promoted to write.
// Returns a release function. If ctx is canceled while waiting, returns ctx.Err().
func (ls *LockScheduler) Acquire(ctx context.Context, locks []ResourceLock) (release func(), err error) {
	if len(locks) == 0 {
		return func() {}, nil
	}

	// Normalize: sort by key, merge duplicates, promote read+write to write
	normalized := normalizeLocks(locks)

	// Acquire each lock in order
	var acquired []ResourceLock
	for _, lock := range normalized {
		if err := ls.acquireOne(ctx, lock); err != nil {
			// Release already acquired locks on failure
			for _, a := range acquired {
				ls.releaseOne(a)
			}
			return nil, err
		}
		acquired = append(acquired, lock)
	}

	released := false
	return func() {
		if released {
			return
		}
		released = true
		for _, lock := range normalized {
			ls.releaseOne(lock)
		}
	}, nil
}

func (ls *LockScheduler) acquireOne(ctx context.Context, lock ResourceLock) error {
	ls.mu.Lock()
	kl, ok := ls.locks[lock.Key]
	if !ok {
		kl = &keyLock{}
		ls.locks[lock.Key] = kl
	}

	if ls.canAcquire(kl, lock.Mode) {
		ls.grant(kl, lock.Mode)
		ls.mu.Unlock()
		return nil
	}

	// Must wait
	ch := make(chan struct{}, 1)
	kl.waiters = append(kl.waiters, waiter{mode: lock.Mode, ch: ch})
	ls.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		ls.mu.Lock()
		found := false
		for i, w := range kl.waiters {
			if w.ch == ch {
				kl.waiters = append(kl.waiters[:i], kl.waiters[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			// wakeWaiters already granted us the lock — undo it
			if lock.Mode == LockRead {
				kl.readers--
			} else {
				kl.writer = false
			}
			ls.wakeWaiters(kl)
		}
		// Clean up idle key
		if kl.readers == 0 && !kl.writer && len(kl.waiters) == 0 {
			delete(ls.locks, lock.Key)
		}
		ls.mu.Unlock()
		return ctx.Err()
	}
}

func (ls *LockScheduler) canAcquire(kl *keyLock, mode LockMode) bool {
	if mode == LockRead {
		return !kl.writer && !hasWriteWaiter(kl)
	}
	return !kl.writer && kl.readers == 0
}

func hasWriteWaiter(kl *keyLock) bool {
	for _, w := range kl.waiters {
		if w.mode == LockWrite {
			return true
		}
	}
	return false
}

func (ls *LockScheduler) grant(kl *keyLock, mode LockMode) {
	if mode == LockRead {
		kl.readers++
	} else {
		kl.writer = true
	}
}

func (ls *LockScheduler) releaseOne(lock ResourceLock) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	kl, ok := ls.locks[lock.Key]
	if !ok {
		return
	}

	if lock.Mode == LockRead {
		kl.readers--
	} else {
		kl.writer = false
	}

	// Wake eligible waiters
	ls.wakeWaiters(kl)

	// Clean up idle key
	if kl.readers == 0 && !kl.writer && len(kl.waiters) == 0 {
		delete(ls.locks, lock.Key)
	}
}

func (ls *LockScheduler) wakeWaiters(kl *keyLock) {
	remaining := kl.waiters[:0]
	for _, w := range kl.waiters {
		if ls.canAcquire(kl, w.mode) {
			ls.grant(kl, w.mode)
			close(w.ch)
		} else {
			remaining = append(remaining, w)
		}
	}
	kl.waiters = remaining
}

// normalizeLocks sorts by key, merges duplicates, promotes read+write to write.
func normalizeLocks(locks []ResourceLock) []ResourceLock {
	// Group by key
	byKey := make(map[string]LockMode)
	for _, l := range locks {
		existing, ok := byKey[l.Key]
		if !ok {
			byKey[l.Key] = l.Mode
		} else if existing == LockRead && l.Mode == LockWrite {
			byKey[l.Key] = LockWrite // promote
		}
	}

	// Sort by key
	result := make([]ResourceLock, 0, len(byKey))
	for k, m := range byKey {
		result = append(result, ResourceLock{Key: k, Mode: m})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Key < result[j].Key
	})
	return result
}
