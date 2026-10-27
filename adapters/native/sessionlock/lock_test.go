//go:build unix

package sessionlock

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

// TestWithoutWaitFailsAtOnceOnAHeldLock: a caller that marked its context
// with WithoutWait must not sit out the configured bound. td status reads
// the index and treats its Telegram part as optional, so a session another
// process holds leaves auth unknown instead of stalling the command.
func TestWithoutWaitFailsAtOnceOnAHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	holder := New(path, time.Minute, nil)
	if err := holder.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Release() })

	start := time.Now()
	err := New(path, time.Minute, nil).Acquire(WithoutWait(context.Background()))
	elapsed := time.Since(start)

	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrSessionLocked {
		t.Fatalf("Acquire under WithoutWait = %v, want ERR_SESSION_LOCKED", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Acquire under WithoutWait took %s, want it to give up at once", elapsed)
	}
}

// TestWaitBoundStillApplies: an ordinary caller keeps waiting up to its
// bound, so WithoutWait is an opt-in rather than a change of the default.
func TestWaitBoundStillApplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	holder := New(path, time.Minute, nil)
	if err := holder.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Release() })

	start := time.Now()
	err := New(path, 300*time.Millisecond, nil).Acquire(context.Background())
	elapsed := time.Since(start)

	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrSessionLocked {
		t.Fatalf("Acquire = %v, want ERR_SESSION_LOCKED", err)
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("Acquire gave up after %s, want it to wait its bound", elapsed)
	}
}
