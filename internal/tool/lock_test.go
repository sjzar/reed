package tool

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLockScheduler_ConcurrentReads(t *testing.T) {
	ls := NewLockScheduler()
	ctx := context.Background()

	r1, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockRead}})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockRead}})
	if err != nil {
		t.Fatal(err)
	}
	r1()
	r2()
}

func TestLockScheduler_WriteBlocksRead(t *testing.T) {
	ls := NewLockScheduler()
	ctx := context.Background()

	release, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockWrite}})
	if err != nil {
		t.Fatal(err)
	}

	var acquired int32
	go func() {
		r, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockRead}})
		if err == nil {
			atomic.StoreInt32(&acquired, 1)
			r()
		}
	}()

	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&acquired) != 0 {
		t.Fatal("read should be blocked by write")
	}
	release()
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&acquired) != 1 {
		t.Fatal("read should have acquired after write release")
	}
}

func TestLockScheduler_WriteBlocksWrite(t *testing.T) {
	ls := NewLockScheduler()
	ctx := context.Background()

	release, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockWrite}})
	if err != nil {
		t.Fatal(err)
	}

	var acquired int32
	go func() {
		r, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockWrite}})
		if err == nil {
			atomic.StoreInt32(&acquired, 1)
			r()
		}
	}()

	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&acquired) != 0 {
		t.Fatal("write should be blocked by write")
	}
	release()
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&acquired) != 1 {
		t.Fatal("write should have acquired after release")
	}
}

func TestLockScheduler_CtxCancel(t *testing.T) {
	ls := NewLockScheduler()
	ctx := context.Background()

	release, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockWrite}})
	if err != nil {
		t.Fatal(err)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	var acquireErr error
	go func() {
		defer wg.Done()
		_, acquireErr = ls.Acquire(cancelCtx, []ResourceLock{{Key: "a", Mode: LockRead}})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
	if acquireErr == nil {
		t.Fatal("expected error from canceled context")
	}
	release()
}

func TestLockScheduler_KeyMerge(t *testing.T) {
	locks := normalizeLocks([]ResourceLock{
		{Key: "a", Mode: LockRead},
		{Key: "a", Mode: LockRead},
	})
	if len(locks) != 1 {
		t.Fatalf("expected 1 lock, got %d", len(locks))
	}
	if locks[0].Mode != LockRead {
		t.Fatal("expected read mode")
	}
}

func TestLockScheduler_ReadWritePromotion(t *testing.T) {
	locks := normalizeLocks([]ResourceLock{
		{Key: "a", Mode: LockRead},
		{Key: "a", Mode: LockWrite},
	})
	if len(locks) != 1 {
		t.Fatalf("expected 1 lock, got %d", len(locks))
	}
	if locks[0].Mode != LockWrite {
		t.Fatal("expected write mode after promotion")
	}
}

func TestLockScheduler_DifferentKeysConcurrent(t *testing.T) {
	ls := NewLockScheduler()
	ctx := context.Background()

	r1, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockWrite}})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ls.Acquire(ctx, []ResourceLock{{Key: "b", Mode: LockWrite}})
	if err != nil {
		t.Fatal(err)
	}
	r1()
	r2()
}

func TestLockScheduler_EmptyLocks(t *testing.T) {
	ls := NewLockScheduler()
	release, err := ls.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	release() // should not panic
}

func TestLockScheduler_CtxCancel_RaceWithGrant(t *testing.T) {
	ls := NewLockScheduler()
	ctx := context.Background()

	// Hold write lock on "a"
	release, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockWrite}})
	if err != nil {
		t.Fatal(err)
	}

	// Start goroutine waiting for write lock on "a" with cancelable ctx
	cancelCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = ls.Acquire(cancelCtx, []ResourceLock{{Key: "a", Mode: LockWrite}})
	}()

	time.Sleep(50 * time.Millisecond)

	// Simultaneously cancel ctx and release the held lock to trigger the race
	cancel()
	release()
	wg.Wait()

	// Verify a subsequent Acquire on "a" succeeds within 500ms (no permanent leak)
	done := make(chan struct{})
	go func() {
		r, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockWrite}})
		if err != nil {
			t.Errorf("subsequent acquire failed: %v", err)
		} else {
			r()
		}
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subsequent acquire blocked — lock leaked")
	}
}

func TestLockScheduler_IdleKeyCleanup(t *testing.T) {
	ls := NewLockScheduler()
	ctx := context.Background()

	release, err := ls.Acquire(ctx, []ResourceLock{{Key: "a", Mode: LockWrite}})
	if err != nil {
		t.Fatal(err)
	}

	ls.mu.Lock()
	if _, ok := ls.locks["a"]; !ok {
		ls.mu.Unlock()
		t.Fatal("expected key 'a' to exist while held")
	}
	ls.mu.Unlock()

	release()

	ls.mu.Lock()
	_, exists := ls.locks["a"]
	ls.mu.Unlock()
	if exists {
		t.Fatal("expected key 'a' to be cleaned up after release")
	}
}
