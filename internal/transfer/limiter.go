package transfer

import (
	"context"
	"sync"
)

// Limiter is the run-slot budget Transfers wait on while queued. Managers
// sharing one Limiter share one budget, and SetLimit changes it while
// Transfers run: a raised limit starts queued Transfers at once, a lowered
// one lets running Transfers finish and holds new ones back until the count
// falls below it.
type Limiter struct {
	mu     sync.Mutex
	limit  int
	active int
	// changed is closed and replaced whenever a slot frees or the limit
	// changes, waking every waiter to recheck.
	changed chan struct{}
}

// NewLimiter returns a Limiter of n slots; n below 1 is the default
// transfers.concurrency.
func NewLimiter(n int) *Limiter {
	if n < 1 {
		n = defaultConcurrency
	}
	return &Limiter{limit: n, changed: make(chan struct{})}
}

// SetLimit changes the number of slots; n below 1 is the default.
func (l *Limiter) SetLimit(n int) {
	if n < 1 {
		n = defaultConcurrency
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = n
	l.wakeLocked()
}

// Limit is the current number of slots.
func (l *Limiter) Limit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

// acquire takes a slot, waiting until one is free or ctx ends.
func (l *Limiter) acquire(ctx context.Context) error {
	for {
		l.mu.Lock()
		if l.active < l.limit {
			l.active++
			l.mu.Unlock()
			return nil
		}
		wait := l.changed
		l.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// release returns a slot acquire took.
func (l *Limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	l.wakeLocked()
}

func (l *Limiter) wakeLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}
