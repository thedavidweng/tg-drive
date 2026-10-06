package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// File row lifecycle (ADR 0024). Every status transition of a files row goes
// through these methods so each one has a single, atomic definition:
//
//	StagePending -> RecordMessage -> (publish: Index) active
//	pending -> DiscardUpload | MarkOrphaned | MarkInvalid
//	active  -> MarkDeleted | MarkDeletedByMessage | MarkMissing | superseded (Index ReplaceFileID)
//
// A row leaving active always loses its node_id in the same transaction as
// the status change, and a row that can no longer resume loses its upload
// state with it.

// FileRow is one files row as the service layer reads it. Nullable text and
// size columns read as their zero value.
type FileRow struct {
	ID            int64
	CanonicalPath string
	DisplayName   string
	LocalPath     sql.NullString
	Size          sql.NullInt64
	ContentHash   sql.NullString
	MIME          string
	Status        string
	MessageID     sql.NullInt64
	ManifestMsgID sql.NullInt64
	ManifestChat  string
	UpdatedAt     string
}

const fileRowColumns = `id, canonical_path, display_name, original_local_path, size, content_hash,
	coalesce(mime,''), status, message_id, manifest_message_id, coalesce(manifest_chat_tg_id,''), updated_at`

func scanFileRow(row interface{ Scan(...any) error }) (FileRow, error) {
	var f FileRow
	err := row.Scan(&f.ID, &f.CanonicalPath, &f.DisplayName, &f.LocalPath, &f.Size, &f.ContentHash,
		&f.MIME, &f.Status, &f.MessageID, &f.ManifestMsgID, &f.ManifestChat, &f.UpdatedAt)
	return f, err
}

func (d *DB) fileRowAt(ctx context.Context, channelRowID int64, canonicalPath, status string) (FileRow, bool, error) {
	f, err := scanFileRow(d.sql.QueryRowContext(ctx, `select `+fileRowColumns+`
		from files where channel_id=? and canonical_path=? and status=?`, channelRowID, canonicalPath, status))
	if errors.Is(err, sql.ErrNoRows) {
		return FileRow{}, false, nil
	}
	if err != nil {
		return FileRow{}, false, err
	}
	return f, true, nil
}

// ActiveByPath returns the active row at a canonical path (found false when
// none exists).
func (d *DB) ActiveByPath(ctx context.Context, channelRowID int64, canonicalPath string) (FileRow, bool, error) {
	return d.fileRowAt(ctx, channelRowID, canonicalPath, "active")
}

// ActiveByID returns the active files row with this id and the Telegram ID
// of the channel it belongs to (found false when the row does not exist or
// is no longer active).
func (d *DB) ActiveByID(ctx context.Context, id int64) (FileRow, string, bool, error) {
	var tgChannelID string
	var f FileRow
	err := d.sql.QueryRowContext(ctx, `select f.id, f.canonical_path, f.display_name, f.original_local_path, f.size,
		f.content_hash, coalesce(f.mime,''), f.status, f.message_id, f.manifest_message_id,
		coalesce(f.manifest_chat_tg_id,''), f.updated_at, c.tg_channel_id
		from files f join channels c on c.id = f.channel_id
		where f.id=? and f.status='active'`, id).Scan(&f.ID, &f.CanonicalPath, &f.DisplayName, &f.LocalPath,
		&f.Size, &f.ContentHash, &f.MIME, &f.Status, &f.MessageID, &f.ManifestMsgID, &f.ManifestChat,
		&f.UpdatedAt, &tgChannelID)
	if errors.Is(err, sql.ErrNoRows) {
		return FileRow{}, "", false, nil
	}
	if err != nil {
		return FileRow{}, "", false, err
	}
	return f, tgChannelID, true, nil
}

// PendingByPath returns the pending row at a canonical path (found false when
// none exists).
func (d *DB) PendingByPath(ctx context.Context, channelRowID int64, canonicalPath string) (FileRow, bool, error) {
	return d.fileRowAt(ctx, channelRowID, canonicalPath, "pending")
}

// PendingRow is the content identity of an upload attempt staged at a path.
type PendingRow struct {
	ChannelRowID  int64
	CanonicalPath string
	DisplayName   string
	LocalPath     string
	Size          int64
	ContentHash   string
	MIME          string
	Now           string
}

// StagePending records an upload attempt at a path before any bytes move.
// adoptFileID > 0 re-points that existing pending row at this attempt (its
// id, and so its resumable state key, is kept; any recorded message id is
// cleared); otherwise a fresh pending row is inserted.
func (d *DB) StagePending(ctx context.Context, row PendingRow, adoptFileID int64) (int64, error) {
	fileID := adoptFileID
	err := d.WithTx(ctx, func(tx *sql.Tx) error {
		if adoptFileID > 0 {
			_, err := tx.ExecContext(ctx, `
				update files set display_name=?, original_local_path=?, size=?, content_hash=?, mime=?, message_id=null, updated_at=?
				where id=? and status='pending'`,
				row.DisplayName, row.LocalPath, row.Size, row.ContentHash, row.MIME, row.Now, adoptFileID)
			return err
		}
		res, err := tx.ExecContext(ctx, `
			insert into files(channel_id,canonical_path,display_name,original_local_path,size,content_hash,mime,status,updated_at)
			values(?,?,?,?,?,?,?,'pending',?)`,
			row.ChannelRowID, row.CanonicalPath, row.DisplayName, row.LocalPath, row.Size, row.ContentHash, row.MIME, row.Now)
		if err != nil {
			return err
		}
		fileID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return 0, err
	}
	return fileID, nil
}

// RecordMessage stores the Telegram message an upload produced on its
// pending row, so a crash before publish is recognizable as "uploaded".
func (d *DB) RecordMessage(ctx context.Context, fileID int64, messageID int, now string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update files set message_id=?, updated_at=? where id=?`, messageID, now, fileID)
		return err
	})
}

// DiscardUpload removes the row of an upload attempt that never became a
// durable file, with its resumable upload state. The row is usually pending;
// an album chunk rolled back after some members published discards those
// members' fresh active rows too, so the status is not checked.
func (d *DB) DiscardUpload(ctx context.Context, fileID int64) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `delete from upload_progress where file_id=?`, fileID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `delete from files where id=?`, fileID)
		return err
	})
}

// MarkOrphaned records that media reached Telegram at messageID but was never
// published, so repair completes or deletes it instead of uploading again.
func (d *DB) MarkOrphaned(ctx context.Context, fileID int64, messageID int, now string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `update files set message_id=? where id=?`, messageID, fileID); err != nil {
			return err
		}
		return retireTx(ctx, tx, fileID, "orphaned", now)
	})
}

// MarkInvalid retires a row that can neither resume nor be completed.
func (d *DB) MarkInvalid(ctx context.Context, fileID int64, now string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		return retireTx(ctx, tx, fileID, "invalid", now)
	})
}

// MarkDeleted retires a row whose media was deleted or tombstoned.
func (d *DB) MarkDeleted(ctx context.Context, fileID int64, now string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		return retireTx(ctx, tx, fileID, "deleted", now)
	})
}

// MarkDeletedByMessage retires the active or missing row backed by a
// tombstoned message (scan reconciliation). It is a no-op when no such row
// exists.
func (d *DB) MarkDeletedByMessage(ctx context.Context, channelRowID int64, messageID int, now string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		var fileID int64
		err := tx.QueryRowContext(ctx, `
			select id from files where channel_id=? and message_id=? and status in ('active','missing')`,
			channelRowID, messageID).Scan(&fileID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return retireTx(ctx, tx, fileID, "deleted", now)
	})
}

// retireTx moves a row out of active (or pending) into a terminal or
// repair-owned status: node link cleared (storage contract, Directory GC) and
// resumable upload state dropped, in the caller's transaction.
func retireTx(ctx context.Context, tx *sql.Tx, fileID int64, status, now string) error {
	if _, err := tx.ExecContext(ctx, `delete from upload_progress where file_id=?`, fileID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `update files set status=?, node_id=null, updated_at=? where id=?`, status, now, fileID)
	return err
}

// TouchActive records that the active row at a canonical path changed only on
// its Telegram surface (a caption edit), without a status transition.
func (d *DB) TouchActive(ctx context.Context, channelRowID int64, canonicalPath, now string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			update files set updated_at=? where channel_id=? and canonical_path=? and status='active'`,
			now, channelRowID, canonicalPath)
		return err
	})
}

// SetManifest records the machine-record message (and its carrier chat; empty
// means the legacy in-channel reply) on every row backed by messageIDs: one
// row for a per-file manifest, every member for an album inventory.
func (d *DB) SetManifest(ctx context.Context, channelRowID int64, messageIDs []int, manifestMsgID int, manifestChat, now string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	args := make([]any, 0, len(messageIDs)+4)
	args = append(args, manifestMsgID, manifestChat, now, channelRowID)
	for _, id := range messageIDs {
		args = append(args, id)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(messageIDs)), ",")
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			update files set manifest_message_id=?, manifest_chat_tg_id=?, updated_at=?
			where channel_id=? and message_id in (`+placeholders+`)`, args...)
		return err
	})
}

// DetachManifest clears a deleted machine-record message from every row that
// pointed at it.
func (d *DB) DetachManifest(ctx context.Context, channelRowID int64, manifestMsgID int, now string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			update files set manifest_message_id=null, updated_at=? where channel_id=? and manifest_message_id=?`,
			now, channelRowID, manifestMsgID)
		return err
	})
}

// MarkMissing retires an active row a full scan no longer found on
// Telegram, inside the scan finalizer's transaction.
func (d *DB) MarkMissing(ctx context.Context, tx *sql.Tx, fileID int64, now string) error {
	return retireTx(ctx, tx, fileID, "missing", now)
}
