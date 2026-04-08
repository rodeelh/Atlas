package mind

// lock.go serializes all writes to MIND.md across the packages that mutate
// it: reflection (after every turn), dream cycle (nightly), and naps (every
// hour or so once phase 3 ships).
//
// Before this file existed, reflectMu in reflection.go serialized only
// reflection runs. Naps and dreams did not share that lock, so concurrent
// writes could race and lose data. This file introduces a package-level
// mutex mindMu that all MIND.md writers MUST acquire before reading the
// file and hold until after the atomic rename.
//
// Usage:
//
//	mind.WithMindLock(func() error {
//	    current, err := os.ReadFile(mindPath)
//	    if err != nil { return err }
//	    // ... mutate ...
//	    return atomicWrite(mindPath, newContent, 0o600)
//	})
//
// The lock is held for the full read-modify-write cycle, not just the write.
// This prevents a read-reflect-write from a slow AI call getting interleaved
// with another writer. The cost is that a slow reflection call (up to 150s
// in the worst case) blocks naps during that window. That is a feature:
// reflection is the authoritative writer and naps should wait their turn.
//
// TryWithMindLock is also exposed for callers that prefer to skip rather
// than queue — reflection uses TryLock today because it's best-effort per
// turn. Naps should use the full lock because missing a nap has higher
// downstream cost than waiting.

import (
	"context"
	"sync"
	"time"

	"atlas-runtime-go/internal/logstore"
)

// mindMu serializes all MIND.md writers in the process. Package-private —
// callers go through the helper functions below.
var mindMu sync.Mutex

// WithMindLock acquires mindMu, runs fn, and releases the lock. If fn
// returns an error, the error is returned unchanged. Blocks until the lock
// is available.
//
// Use this from writers that MUST run eventually (nap cycle, dream cycle).
// Reflection uses TryWithMindLock instead because dropping a reflection is
// cheaper than queueing behind a long-running dream.
func WithMindLock(fn func() error) error {
	mindMu.Lock()
	defer mindMu.Unlock()
	return fn()
}

// TryWithMindLock acquires mindMu with a non-blocking attempt. If the lock
// is already held, returns (false, nil) immediately without running fn.
// If fn runs and returns an error, that error is returned with true to
// indicate fn did run.
//
// Use this from writers where skipping is acceptable — e.g. reflection,
// which fires after every turn and will get another chance on the next
// turn. Naps and dreams should use WithMindLock instead.
func TryWithMindLock(fn func() error) (ran bool, err error) {
	if !mindMu.TryLock() {
		return false, nil
	}
	defer mindMu.Unlock()
	return true, fn()
}

// WithMindLockTimeout acquires mindMu with a maximum wait time. Returns a
// context.DeadlineExceeded error if the lock cannot be acquired within
// timeout. Useful for writers that want to wait a bit but not forever
// (e.g. naps that shouldn't block a dream cycle).
func WithMindLockTimeout(ctx context.Context, timeout time.Duration, fn func() error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Poll TryLock on a short interval. 50ms interval × 1200 max tries = 60s
	// upper bound, which is more than enough for the timeouts we'll pick in
	// practice.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if mindMu.TryLock() {
			defer mindMu.Unlock()
			return fn()
		}
		select {
		case <-ctx.Done():
			logstore.Write("warn", "MIND.md lock timeout exceeded", map[string]string{
				"timeout": timeout.String(),
			})
			return ctx.Err()
		case <-ticker.C:
			// Loop and try again.
		}
	}
}
