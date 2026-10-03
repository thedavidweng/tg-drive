package service

import (
	"context"
	"database/sql"
	"strconv"
	"sync"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

// An operation (ADR 0030) is one use case's exclusive claim on canonical
// paths of the bound drive channel: it holds their operation locks, renewed
// by heartbeat, for as long as its body runs. Every Telegram write happens
// inside one. The channel context is resolved once and travels with the
// operation, so nested calls (the upload pipeline inside an import) reuse it
// and the locks already held instead of resolving or locking again.

// channelContext is the bound drive channel as one use case sees it.
type channelContext struct {
	rowID   int64
	tgID    int64
	tgIDStr string

	app          *App
	discussionMu sync.Mutex
	discussion   string
}

type channelSelectorKey struct{}

// WithChannel returns ctx selecting a bound drive channel, by title or
// Telegram ID, for the calls made with it. It overrides App.Channel for
// those calls only; an empty selector keeps App.Channel.
func WithChannel(ctx context.Context, selector string) context.Context {
	return context.WithValue(ctx, channelSelectorKey{}, selector)
}

// channelSelector is the channel selector in effect for a call.
func (a *App) channelSelector(ctx context.Context) string {
	if sel, _ := ctx.Value(channelSelectorKey{}).(string); sel != "" {
		return sel
	}
	return a.Channel
}

// channel returns the bound drive channel. Inside an operation it is the
// operation's channel; otherwise it is read from the index.
func (a *App) channel(ctx context.Context) (*channelContext, error) {
	if op := operationFrom(ctx); op != nil {
		return op.ch, nil
	}
	ch := &channelContext{app: a}
	var title string
	var err error
	if sel := a.channelSelector(ctx); sel != "" {
		err = a.DB.Raw().QueryRowContext(ctx, `select id, tg_channel_id, title from channels where title=? or tg_channel_id=? limit 1`,
			sel, sel).Scan(&ch.rowID, &ch.tgIDStr, &title)
		if err == sql.ErrNoRows {
			return nil, apperr.New(apperr.ErrChannelNotFound, "channel not found: "+sel)
		}
	} else {
		err = a.DB.Raw().QueryRowContext(ctx, `select id, tg_channel_id, title from channels limit 1`).Scan(&ch.rowID, &ch.tgIDStr, &title)
		if err == sql.ErrNoRows {
			return nil, apperr.New(apperr.ErrChannelNotFound,
				"no channel bound in this database; run: td init <local-root> --create-channel (new drive) or --bind-channel (existing drive, rebuilds the index)")
		}
	}
	if err != nil {
		return nil, err
	}
	if ch.tgID, err = strconv.ParseInt(ch.tgIDStr, 10, 64); err != nil {
		return nil, apperr.New(apperr.ErrDB, "stored channel id is not numeric: "+ch.tgIDStr)
	}
	return ch, nil
}

// ChannelTelegramID is the Telegram ID of the bound drive channel that calls
// made with ctx work on. It fails with ERR_CHANNEL_NOT_FOUND like those
// calls would.
func (a *App) ChannelTelegramID(ctx context.Context) (string, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return "", err
	}
	return ch.tgIDStr, nil
}

// operation is the running operation carried by its body's context.
type operation struct {
	ch *channelContext
	// held is every lock key the operation and its enclosing operations
	// hold.
	held map[string]bool
}

type operationKey struct{}

func operationFrom(ctx context.Context) *operation {
	op, _ := ctx.Value(operationKey{}).(*operation)
	return op
}

// operate runs fn as an operation on ch holding the operation locks of
// paths. Inside an enclosing operation it acquires only the keys that
// operation does not already hold, and releases just those afterwards; the
// enclosing operation's channel context wins over ch. A lock held elsewhere
// fails with ERR_OPERATION_LOCKED before fn runs. An operation with no paths
// still carries the channel context: it covers writes to messages that no
// indexed path owns.
func (a *App) operate(ctx context.Context, ch *channelContext, paths []string, fn func(ctx context.Context) error) error {
	op := &operation{ch: ch, held: map[string]bool{}}
	if parent := operationFrom(ctx); parent != nil {
		op.ch = parent.ch
		for k := range parent.held {
			op.held[k] = true
		}
	}
	var missing []string
	for _, k := range lockKeysForPaths(op.ch.rowID, paths...) {
		if !op.held[k] {
			op.held[k] = true
			missing = append(missing, k)
		}
	}
	run := func(ctx context.Context) error {
		return fn(context.WithValue(ctx, operationKey{}, op))
	}
	if len(missing) == 0 {
		return run(ctx)
	}
	return a.withLocks(ctx, missing, run)
}

// pathLocked reports whether some live operation holds path's lock.
func (a *App) pathLocked(ctx context.Context, ch *channelContext, path string) (bool, error) {
	return a.DB.LockHeld(ctx, sqlitestore.LockKey(ch.rowID, path))
}
