package service

import (
	"context"
	"io"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

// cancelled returns ERR_CANCELLED once ctx is cancelled. Long-running use
// cases call it between items so a cancelled call stops at the next item
// boundary, even under --continue-on-error.
func cancelled(ctx context.Context) error {
	if ctx.Err() != nil {
		return apperr.Cancelled()
	}
	return nil
}

// stopsRun reports whether a per-item error must end a lenient loop:
// cancellation is never a per-item failure to continue past.
func stopsRun(ctx context.Context, err error) bool {
	return ctx.Err() != nil || apperr.IsCancelled(err)
}

// cleanupTimeout bounds rollback work that outlives a cancelled call.
const cleanupTimeout = 30 * time.Second

// cleanupContext is ctx without its cancellation, for rollback that must
// still reach the index and Telegram after the caller cancelled (Ctrl-C);
// otherwise an interrupted upload would leave rows a retry cannot adopt.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// contextReader stops a long local read (hashing a multi-gigabyte file)
// once ctx is cancelled.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
