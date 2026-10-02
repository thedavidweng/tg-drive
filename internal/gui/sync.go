//go:build gui

package gui

import (
	"context"
	"database/sql"
	"time"
)

// DefaultSyncInterval is how often the GUI polls the shared index for
// changes made by other processes (ADR 0033).
const DefaultSyncInterval = 250 * time.Millisecond

// StartSync launches index sync: every interval it reads PRAGMA
// data_version, and when another writer committed to the index it re-reads
// the directory the frontend is showing and emits EventDirectoryChanged
// with the fresh listing, and diffs the Transfers, emitting the
// transfer-stage, transfer-progress, and transfer-removed events for what
// changed (a td process's Transfers included). It runs until ctx is
// cancelled; the ctx a caller passes should live as long as the
// application. It survives Auth reopening the App (setup/login on a fresh
// machine): the poller resolves the current App every tick and re-pins its
// connection when the pool changes.
func (s *Services) StartSync(ctx context.Context, interval time.Duration) {
	go pollIndexChanges(ctx, s.state, interval, func(ctx context.Context) {
		s.Drive.refreshCurrent(ctx)
		s.Transfers.syncFromIndex(ctx)
	})
}

// pollIndexChanges runs task after every commit another connection makes to
// the index database. PRAGMA data_version is per connection — it advances
// only when a *different* connection commits — so the poller pins one
// connection per App generation and observes every other writer through it:
// a td process as well as this app's own pool (the duplicate refresh after
// the GUI's own writes is harmless). A reopen closes the old pool and its
// pinned connection, so the poller re-pins lazily: a read error or a
// changed pool unpins, and the next tick pins the current App's pool and
// baselines without firing.
func pollIndexChanges(ctx context.Context, state *appState, interval time.Duration, task func(ctx context.Context)) {
	var conn *sql.Conn
	var pinned *sql.DB
	var last int64
	unpin := func() {
		if conn != nil {
			_ = conn.Close()
		}
		conn, pinned = nil, nil
	}
	defer unpin()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		db := state.current().DB.Raw()
		if db != pinned {
			unpin()
			c, err := db.Conn(ctx)
			if err != nil {
				continue
			}
			conn, pinned = c, db
			// A fresh pin baselines — but commits made while unpinned (a
			// reopen swapped the pool) would hide inside the baseline, so
			// every (re)pin runs the task once to cover the gap. The task
			// is idempotent: a redundant refresh.
			if err := conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&last); err != nil {
				unpin()
				continue
			}
			task(ctx)
			continue
		}
		var v int64
		if err := conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v); err != nil {
			// The pinned connection died (a reopen closed the old pool);
			// re-pin on the next tick.
			unpin()
			continue
		}
		if v != last {
			last = v
			task(ctx)
		}
	}
}

// refreshCurrent re-reads the directory the frontend is showing and emits
// it as a directory-changed event. A listing that now fails (for example
// the directory itself was deleted) emits nothing; the frontend keeps its
// last good listing until the user navigates.
func (d *Drive) refreshCurrent(ctx context.Context) {
	d.mu.Lock()
	path := d.current
	d.mu.Unlock()
	if path == "" {
		return
	}
	entries, err := d.list(ctx, path)
	if err != nil {
		return
	}
	d.emitEvent(EventDirectoryChanged, DirectoryChanged{Path: path, Entries: entries})
}
