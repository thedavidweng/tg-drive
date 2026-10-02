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
)

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
func (a *App) RepairPending(ctx context.Context) (*RepairPendingResult, error) {
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
		// A path lock held right now means an operation is in flight (e.g. an
		// upload adopting this very row); leave it alone.
		if held, err := a.pathLocked(ctx, ch, r.path); err == nil && held {
			skipped++
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
				skipped++
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
					skipped++
					continue
				}
				if _, err := a.UploadFile(ctx, r.local.String, r.path, ConflictSkip, false); err == nil {
					repaired++
					continue
				}
				invalid++
				continue
			}
			if flip(markInvalid) {
				invalid++
			}
		default:
			if flip(markInvalid) {
				invalid++
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

// RepairOrphaned completes or removes uploads whose media message exists on
// Telegram but whose index promotion failed.
func (a *App) RepairOrphaned(ctx context.Context, deleteOrphans bool) (*RepairOrphanedResult, error) {
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
		if !r.msgID.Valid {
			_ = a.DB.MarkInvalid(ctx, r.id, now)
			invalid++
			continue
		}
		// Each orphan repair holds its path lock, so it cannot race a
		// concurrent move or delete of the same file.
		err := a.operate(ctx, ch, []string{r.path}, func(ctx context.Context) error {
			if deleteOrphans {
				err := a.TG.DeleteMessage(ctx, tgChID, int(r.msgID.Int64))
				if err == nil || isMessageGone(err) {
					if err := a.DB.MarkDeleted(ctx, r.id, now); err != nil {
						// Stays orphaned: the next repair finds the message
						// gone and retires the row then.
						return nil
					}
					deleted++
					return nil
				}
				invalid++
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
				return nil // stays orphaned for a later attempt
			}
			if _, err := pub.PublishFile(ctx, publisher.FileRequest{
				ChannelRowID: channelID,
				ChannelID:    tgChID,
				FileID:       r.id,
				MessageID:    int(r.msgID.Int64),
				Rendition:    rendition,
			}); err != nil {
				return nil // stays orphaned for a later attempt
			}
			_ = a.DB.DeleteUploadStateByFile(ctx, r.id)
			repaired++
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return &RepairOrphanedResult{Deleted: deleted, Invalid: invalid, Repaired: repaired}, nil
}

// RepairPathResult names the repaired file.
type RepairPathResult struct {
	Repaired string `json:"repaired"`
}

// RepairPath re-renders and re-applies caption, manifest, and tags for one
// active file, repairing caption/manifest drift.
func (a *App) RepairPath(ctx context.Context, remotePath string) (*RepairPathResult, error) {
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
	lockErr := a.operate(ctx, ch, []string{p}, func(ctx context.Context) error {
		return a.fileRecord(ch.rowID, ch.tgID, row).Rewrite(ctx)
	})
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
func (a *App) RepairScanErrors(ctx context.Context) (*RepairScanErrorsResult, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	channelID := ch.rowID
	var before int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from scan_errors where channel_id=? and status='pending'`, channelID).Scan(&before)
	if _, err := a.Scan(ctx, ScanOptions{Full: true, Repair: true}); err != nil {
		return nil, err
	}
	var after int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from scan_errors where channel_id=? and status='pending'`, channelID).Scan(&after)
	return &RepairScanErrorsResult{Pending: after, Resolved: before - after}, nil
}
