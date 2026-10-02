package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/publisher"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// RepairOptions selects one td repair mode. No selection at all repairs
// pending uploads.
type RepairOptions struct {
	// Path is the path argument, nil when none was given. It scopes Captions
	// and Hash; otherwise it names the one file to repair and takes
	// precedence over Pending, Orphaned, and ScanErrors. A given empty path
	// normalizes to "/" like any other.
	Path       *string
	Pending    bool
	Orphaned   bool
	ScanErrors bool
	Hash       bool
	Captions   bool
	// DeleteOrphaned deletes orphaned Telegram messages instead of
	// completing them (Orphaned mode only).
	DeleteOrphaned bool
	// Confirm is the ADR 0003 confirmation DeleteOrphaned requires.
	Confirm bool
	// DryRun and ContinueOnError apply to Captions mode only.
	DryRun          bool
	ContinueOnError bool
	// Observer receives the selected mode's stages and per-item results.
	Observer Observer
}

// Validate rejects conflicting modes, mode-specific flags outside their mode,
// and an unconfirmed DeleteOrphaned. Repair applies it first; front ends may
// call it before opening anything so the gate fails fast.
func (o RepairOptions) Validate() error {
	modes := 0
	for _, selected := range []bool{o.Pending, o.Orphaned, o.ScanErrors, o.Hash, o.Captions} {
		if selected {
			modes++
		}
	}
	if modes > 1 {
		return apperr.New(apperr.ErrUsage, "repair modes are mutually exclusive")
	}
	if o.DeleteOrphaned && !o.Orphaned {
		return apperr.New(apperr.ErrUsage, "--delete-orphaned requires --orphaned")
	}
	if o.DeleteOrphaned && !o.Confirm {
		return apperr.New(apperr.ErrConfirmationRequired, "deleting orphaned Telegram messages requires --confirm")
	}
	if (o.DryRun || o.ContinueOnError) && !o.Captions {
		return apperr.New(apperr.ErrUsage, "--dry-run and --continue-on-error require --captions")
	}
	return nil
}

// Repair runs the repair mode opts selects and returns that mode's result.
func (a *App) Repair(ctx context.Context, opts RepairOptions) (any, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	path := ""
	if opts.Path != nil {
		path = *opts.Path
	}
	switch {
	case opts.Captions:
		return a.RepairCaptions(ctx, path, opts.DryRun, opts.ContinueOnError, opts.Observer)
	case opts.Hash:
		return a.RepairHash(ctx, path, opts.Observer)
	case opts.Path != nil:
		return a.RepairPath(ctx, path, opts.Observer)
	case opts.Orphaned:
		return a.repairOrphaned(ctx, opts.DeleteOrphaned, opts.Observer)
	case opts.ScanErrors:
		return a.RepairScanErrors(ctx, opts.Observer)
	default:
		return a.RepairPending(ctx, opts.Observer)
	}
}

// RepairPendingResult counts how stale pending rows were resolved.
type RepairPendingResult struct {
	Invalid      int   `json:"invalid"`
	LocksCleared int64 `json:"locks_cleared"`
	Orphaned     int   `json:"orphaned"`
	Repaired     int   `json:"repaired"`
	Skipped      int   `json:"skipped"`
}

// RepairPending resolves stale pending rows and expired operation locks.
// Only rows older than the lock TTL whose path is not currently locked are
// touched, so a live in-flight upload is never deleted or duplicated.
func (a *App) RepairPending(ctx context.Context, obs Observer) (*RepairPendingResult, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	channelID := ch.rowID
	now := time.Now().UTC().Format(time.RFC3339)
	ttl := time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 900 * time.Second // same default as withLocks
	}
	staleCutoff := time.Now().UTC().Add(-ttl).Format(time.RFC3339)
	locksCleared := int64(0)
	if res, err := a.DB.Raw().ExecContext(ctx, `delete from operation_locks where expires_at < ?`, now); err == nil {
		locksCleared, _ = res.RowsAffected()
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `select id, canonical_path, original_local_path, message_id from files where channel_id=? and status='pending' and updated_at < ?`, channelID, staleCutoff)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list pending", err)
	}
	type pendingRow struct {
		id    int64
		path  string
		local sql.NullString
		msgID sql.NullInt64
	}
	var pending []pendingRow
	for rows.Next() {
		var r pendingRow
		if err := rows.Scan(&r.id, &r.path, &r.local, &r.msgID); err != nil {
			_ = rows.Close()
			return nil, apperr.Wrap(apperr.ErrDB, "scan pending", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, apperr.Wrap(apperr.ErrDB, "list pending", err)
	}
	_ = rows.Close()
	repaired, invalid, orphaned, skipped := 0, 0, 0, 0
	for _, r := range pending {
		if err := cancelled(ctx); err != nil {
			return nil, err
		}
		it := Item{Source: r.local.String, Path: r.path}
		skip := func() {
			skipped++
			obs.item(ItemResult{Item: it, Status: ItemSkipped})
		}
		// A path lock held right now means an operation is in flight (e.g. an
		// upload adopting this very row); leave it alone.
		if held, err := a.pathLocked(ctx, ch, r.path); err == nil && held {
			skip()
			continue
		}
		markInvalid := func(ctx context.Context) error { return a.DB.MarkInvalid(ctx, r.id, now) }
		// Status flips hold the row's path lock so they cannot race a
		// concurrent upload adopting the same row.
		flip := func(retire func(ctx context.Context) error) bool {
			ok := true
			err := a.operate(ctx, ch, []string{r.path}, func(ctx context.Context) error {
				_ = retire(ctx)
				return nil
			})
			if err != nil {
				ok = false
				skip()
			}
			return ok
		}
		switch {
		case r.msgID.Valid:
			// Upload reached Telegram but was never promoted; hand off to
			// orphan repair which can complete or delete it. The upload
			// finished, so any resumable part state is garbage.
			if flip(func(ctx context.Context) error { return a.DB.MarkOrphaned(ctx, r.id, int(r.msgID.Int64), now) }) {
				orphaned++
				obs.done(it, nil)
			}
		case r.local.Valid && r.local.String != "":
			if _, statErr := a.files().Stat(ctx, r.local.String); statErr == nil {
				// Drop the stale row (and its state) and retry the upload
				// fresh; UploadFile takes the path lock itself.
				err := a.operate(ctx, ch, []string{r.path}, func(ctx context.Context) error {
					_ = a.DB.DiscardUpload(ctx, r.id)
					return nil
				})
				if err != nil {
					skip()
					continue
				}
				_, err = a.uploadFile(ctx, r.local.String, r.path, ConflictSkip, false, Presentation{}, "", UploadOptions{Observer: obs.stages()})
				if err == nil {
					repaired++
				} else {
					invalid++
				}
				obs.done(it, err)
				continue
			}
			if flip(markInvalid) {
				invalid++
				obs.done(it, apperr.New(apperr.ErrLocalNotFound, fmt.Sprintf("local source %q no longer exists", r.local.String)))
			}
		default:
			if flip(markInvalid) {
				invalid++
				obs.done(it, apperr.New(apperr.ErrLocalNotFound, "no local source recorded to retry the upload from"))
			}
		}
	}
	return &RepairPendingResult{Invalid: invalid, LocksCleared: locksCleared, Orphaned: orphaned, Repaired: repaired, Skipped: skipped}, nil
}

// RepairOrphanedResult counts how orphaned uploads were resolved.
type RepairOrphanedResult struct {
	Deleted  int `json:"deleted"`
	Invalid  int `json:"invalid"`
	Repaired int `json:"repaired"`
}

// repairOrphaned completes or removes uploads whose media message exists on
// Telegram but whose index promotion failed.
func (a *App) repairOrphaned(ctx context.Context, deleteOrphans bool, obs Observer) (*RepairOrphanedResult, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	channelID, tgChID := ch.rowID, ch.tgID
	manifestChat, err := ch.discussionChat(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select id, canonical_path, display_name, coalesce(size,0), coalesce(content_hash,''), coalesce(mime,''), message_id
		from files where channel_id=? and status='orphaned'`, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list orphaned", err)
	}
	type orphanRow struct {
		id          int64
		path, name  string
		size        int64
		hash, mimeT string
		msgID       sql.NullInt64
	}
	var orphans []orphanRow
	for rows.Next() {
		var r orphanRow
		if err := rows.Scan(&r.id, &r.path, &r.name, &r.size, &r.hash, &r.mimeT, &r.msgID); err != nil {
			_ = rows.Close()
			return nil, apperr.Wrap(apperr.ErrDB, "scan orphaned", err)
		}
		orphans = append(orphans, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, apperr.Wrap(apperr.ErrDB, "list orphaned", err)
	}
	_ = rows.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	repaired, deleted, invalid := 0, 0, 0
	for _, r := range orphans {
		if err := cancelled(ctx); err != nil {
			return nil, err
		}
		it := Item{Path: r.path, MessageID: int(r.msgID.Int64)}
		if !r.msgID.Valid {
			_ = a.DB.MarkInvalid(ctx, r.id, now)
			invalid++
			obs.done(it, apperr.New(apperr.ErrDB, "orphaned row records no message"))
			continue
		}
		obs.stage(it, StagePublishing)
		// itemErr is why the orphan was not resolved; it stays orphaned or
		// turns invalid, and the repair moves on.
		var itemErr error
		// Each orphan repair holds its path lock, so it cannot race a
		// concurrent move or delete of the same file.
		err := a.operate(ctx, ch, []string{r.path}, func(ctx context.Context) error {
			if deleteOrphans {
				err := a.TG.DeleteMessage(ctx, tgChID, int(r.msgID.Int64))
				if err == nil || isMessageGone(err) {
					if err := a.DB.MarkDeleted(ctx, r.id, now); err != nil {
						// Stays orphaned: the next repair finds the message
						// gone and retires the row then.
						itemErr = apperr.Wrap(apperr.ErrDB, "mark deleted", err)
						return nil
					}
					deleted++
					return nil
				}
				invalid++
				itemErr = telegram.MapError(err)
				return nil
			}
			// Complete the interrupted upload: regenerate metadata and resend the
			// manifest reply, then promote the row.
			pub := a.publisher()
			rendition, err := pub.Render(publisher.RenderRequest{
				Meta: manifest.FileMeta{
					CanonicalPath: r.path,
					DisplayName:   r.name,
					ParentHuman:   fsmodel.HumanParent(r.path),
					Size:          r.size,
					Hash:          r.hash,
					MIME:          r.mimeT,
					Created:       now,
				},
				ManifestChatID: manifestChat,
				ExistingSlugs:  a.loadSlugMap(ctx, channelID),
			})
			if err != nil {
				itemErr = err
				return nil // stays orphaned for a later attempt
			}
			if _, err := pub.PublishFile(ctx, publisher.FileRequest{
				ChannelRowID: channelID,
				ChannelID:    tgChID,
				FileID:       r.id,
				MessageID:    int(r.msgID.Int64),
				Rendition:    rendition,
			}); err != nil {
				itemErr = err
				return nil // stays orphaned for a later attempt
			}
			_ = a.DB.DeleteUploadStateByFile(ctx, r.id)
			repaired++
			return nil
		})
		if err != nil {
			obs.done(it, err)
			return nil, err
		}
		obs.done(it, itemErr)
	}
	return &RepairOrphanedResult{Deleted: deleted, Invalid: invalid, Repaired: repaired}, nil
}

// RepairPathResult names the repaired file.
type RepairPathResult struct {
	Repaired string `json:"repaired"`
}

// RepairPath re-renders and re-applies caption, manifest, and tags for one
// active file, repairing caption/manifest drift.
func (a *App) RepairPath(ctx context.Context, remotePath string, obs Observer) (*RepairPathResult, error) {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	row, found, err := a.DB.ActiveByPath(ctx, ch.rowID, p)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	if !found || !row.MessageID.Valid {
		return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
	}
	it := Item{Path: p, MessageID: int(row.MessageID.Int64)}
	obs.stage(it, StagePublishing)
	lockErr := a.operate(ctx, ch, []string{p}, func(ctx context.Context) error {
		return a.fileRecord(ch.rowID, ch.tgID, row).Rewrite(ctx)
	})
	obs.done(it, lockErr)
	if lockErr != nil {
		return nil, lockErr
	}
	return &RepairPathResult{Repaired: p}, nil
}

// RepairScanErrorsResult counts resolved and still-pending scan errors.
type RepairScanErrorsResult struct {
	Pending  int `json:"pending"`
	Resolved int `json:"resolved"`
}

// RepairScanErrors reprocesses the channel and clears resolved scan errors.
func (a *App) RepairScanErrors(ctx context.Context, obs Observer) (*RepairScanErrorsResult, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	channelID := ch.rowID
	var before int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from scan_errors where channel_id=? and status='pending'`, channelID).Scan(&before)
	if _, err := a.Scan(ctx, ScanOptions{Full: true, Repair: true, Observer: obs}); err != nil {
		return nil, err
	}
	var after int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from scan_errors where channel_id=? and status='pending'`, channelID).Scan(&after)
	return &RepairScanErrorsResult{Pending: after, Resolved: before - after}, nil
}
