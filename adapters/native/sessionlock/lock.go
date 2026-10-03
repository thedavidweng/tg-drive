// Package sessionlock guards one Telegram session file against concurrent
// use by separate processes (ADR 0034). The Session lock is an exclusive OS
// file lock beside the session file, taken on the first Telegram call and
// released on Close, so commands that only read the local index never wait.
package sessionlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

// DefaultWait is how long Acquire waits for another process by default.
const DefaultWait = 30 * time.Second

const pollInterval = 100 * time.Millisecond

// errHeld reports that another open file holds the OS lock.
var errHeld = errors.New("session lock held")

// Lock is the Session lock of one session file.
type Lock struct {
	path string
	wait time.Duration
	logf func(format string, args ...any)

	mu   sync.Mutex
	file *os.File
}

// New returns the Session lock for sessionPath. wait bounds how long Acquire
// waits for another holder; a non-positive wait means DefaultWait. logf, when
// set, receives a note while Acquire waits.
func New(sessionPath string, wait time.Duration, logf func(format string, args ...any)) *Lock {
	if wait <= 0 {
		wait = DefaultWait
	}
	return &Lock{path: sessionPath + ".lock", wait: wait, logf: logf}
}

// Acquire takes the lock, waiting up to the bound for another process to
// release it. It returns ERR_SESSION_LOCKED when the bound passes. Acquiring a
// lock this Lock already holds is a no-op.
func (l *Lock) Acquire(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(l.wait)
	logged := false
	for {
		err := tryLock(f)
		if err == nil {
			l.file = f
			return nil
		}
		if !errors.Is(err, errHeld) {
			_ = f.Close()
			return fmt.Errorf("session lock: %w", err)
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			msg := fmt.Sprintf("the Telegram session is in use by another td process; waited %s, retry when it finishes", l.wait)
			return apperr.New(apperr.ErrSessionLocked, msg).
				WithDetails(map[string]any{"wait_seconds": int(l.wait / time.Second)})
		}
		if !logged && l.logf != nil {
			l.logf("telegram: session in use by another process; waiting up to %s", l.wait)
			logged = true
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// Release frees the lock if held. The lock file stays in place: removing it
// would let a waiter lock an unlinked file while a newcomer locks a new one.
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := unlock(f)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
