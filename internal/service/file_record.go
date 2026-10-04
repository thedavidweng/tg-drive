package service

import (
	"context"
	"fmt"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
	"github.com/thedavidweng/tg-drive/core/manifest"
	"github.com/thedavidweng/tg-drive/core/publisher"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// fileRecord is the File record of one indexed file: its per-file
// td-manifest:v1 comment (legacy in-channel reply or td:v1 caption on older
// posts) or its entry in the td-album:v1 inventory its media group shares
// (ADR 0028). Callers state an intent; each operation resolves which kind
// of record it is once, by reading the manifest message through the row's
// carrier, because the index stores no album membership.
//
// Operations mutate Telegram and the machine-record pointers on the rows;
// the caller holds the path locks and owns the file row's own status.
type fileRecord struct {
	a         *App
	channelID int64
	tgChID    int64
	row       sqlitestore.FileRow
	carrier   telegram.ManifestCarrier
}

func (a *App) fileRecord(channelID, tgChID int64, row sqlitestore.FileRow) *fileRecord {
	return &fileRecord{a: a, channelID: channelID, tgChID: tgChID, row: row, carrier: a.manifestCarrier(row.ManifestChat)}
}

func (r *fileRecord) messageID() int {
	if !r.row.MessageID.Valid {
		return 0
	}
	return int(r.row.MessageID.Int64)
}

func (r *fileRecord) manifestID() int {
	if !r.row.ManifestMsgID.Valid {
		return 0
	}
	return int(r.row.ManifestMsgID.Int64)
}

func (r *fileRecord) ref() publisher.RecordRef {
	return publisher.RecordRef{
		ChannelRowID:   r.channelID,
		ChannelID:      r.tgChID,
		FileID:         r.row.ID,
		MessageID:      r.messageID(),
		ManifestMsgID:  r.manifestID(),
		ManifestChatID: r.row.ManifestChat,
	}
}

// album resolves membership: ok reports that the row's manifest message is
// a shared album inventory.
func (r *fileRecord) album(ctx context.Context) (manifest.AlbumMeta, bool, error) {
	return r.a.loadAlbumManifest(ctx, r.tgChID, r.carrier, r.manifestID())
}

// recordRetirement reports what Retire did. The media mutation has already
// succeeded whenever Retire returns one.
type recordRetirement struct {
	// mode is the delete mode applied; album members are always deleted.
	mode string
	// stale is a record left unredacted (or an inventory left unrewritten).
	stale error
	// recordErr is an ERR_DB failure to point the sibling rows at an
	// inventory rewrite that reached Telegram.
	recordErr error
	inAlbum   bool
	manID     int
}

// staleErr is the td rm failure for a stale record; nil when nothing is
// stale or the caller accepts stale records. The file is already removed
// when it fails, so the details report that outcome alongside the record.
func (rt recordRetirement) staleErr(path string, allowStale bool) error {
	if rt.stale == nil || allowStale {
		return nil
	}
	msg := fmt.Sprintf("%s was removed, but manifest reply %d could not be redacted: %v; pass --allow-stale-manifest to accept a stale record", path, rt.manID, rt.stale)
	if rt.inAlbum {
		msg = fmt.Sprintf("%s was removed, but album inventory %d could not be updated: %v; pass --allow-stale-manifest to accept a stale record", path, rt.manID, rt.stale)
	}
	return apperr.New(apperr.ErrTelegramRPC, msg).WithDetails(map[string]any{
		"path":                path,
		"mode":                rt.mode,
		"stale_manifest":      true,
		"manifest_message_id": rt.manID,
	})
}

// Retire removes the file from Telegram for td rm. mode "delete" deletes the
// media and its manifest; "tombstone" tombstones them. A failed media
// mutation is an error; a failed record redaction after it is reported as
// stale, because the file is gone either way.
func (r *fileRecord) Retire(ctx context.Context, mode string) (recordRetirement, error) {
	album, inAlbum, err := r.album(ctx)
	if err != nil && !isMessageGone(err) {
		return recordRetirement{}, telegram.MapError(err)
	}
	msgID, manID := r.messageID(), r.manifestID()
	if inAlbum {
		stale, recordErr, err := r.retireAlbumMember(ctx, album)
		if err != nil {
			return recordRetirement{}, err
		}
		return recordRetirement{mode: "delete", stale: stale, recordErr: recordErr, inAlbum: true, manID: manID}, nil
	}
	p := r.row.CanonicalPath
	var stale error
	switch {
	case mode == "delete":
		if msgID > 0 {
			if err := r.a.TG.DeleteMessage(ctx, r.tgChID, msgID); err != nil && !isMessageGone(err) {
				return recordRetirement{}, telegram.MapError(err)
			}
		}
		if manID > 0 {
			stale = r.carrier.Delete(ctx, r.tgChID, manID)
		}
	case manID > 0 && r.carrier.Comment():
		// ADR 0018: the comment carries the tombstone. If the comment edit
		// fails, fall back to a tombstone caption: deletion must stay sticky
		// even when the thread record cannot be redacted, and a caption
		// tombstone outranks a stale live comment during scans.
		stale = r.carrier.Edit(ctx, r.tgChID, manID, manifest.RenderTombstoneManifest(p))
		if stale != nil && !isMessageGone(stale) && msgID > 0 {
			if capErr := r.a.TG.EditCaption(ctx, r.tgChID, msgID, manifest.RenderTombstoneCaption(fsmodel.BaseName(p), p)); capErr == nil || isMessageGone(capErr) {
				stale = nil
			}
		}
	default:
		if msgID > 0 {
			if err := r.a.TG.EditCaption(ctx, r.tgChID, msgID, manifest.RenderTombstoneCaption(fsmodel.BaseName(p), p)); err != nil && !isMessageGone(err) {
				return recordRetirement{}, telegram.MapError(err)
			}
		}
		if manID > 0 {
			stale = r.carrier.Edit(ctx, r.tgChID, manID, manifest.RenderTombstoneManifest(p))
		}
	}
	if isMessageGone(stale) {
		stale = nil
	}
	return recordRetirement{mode: mode, stale: stale, manID: manID}, nil
}

// RetireSuperseded removes the file a --replace upload superseded. It is
// best effort: the replacement is already published and indexed, so
// failures leave stale records for scans rather than failing the upload.
// The row's display name is the one the tombstone caption shows.
func (r *fileRecord) RetireSuperseded(ctx context.Context, mode string) {
	msgID, manID := r.messageID(), r.manifestID()
	album, inAlbum, err := r.album(ctx)
	switch {
	case inAlbum:
		// Album members share one inventory; redacting it would drop every
		// sibling's machine record.
		_, _, _ = r.retireAlbumMember(ctx, album)
		return
	case err != nil && !isMessageGone(err):
		// The record may be a shared album inventory; leave it untouched and
		// retire the media alone.
		manID = 0
	}
	p := r.row.CanonicalPath
	tombstone := func() {
		if msgID > 0 {
			_ = r.a.TG.EditCaption(ctx, r.tgChID, msgID, manifest.RenderTombstoneCaption(r.row.DisplayName, p))
		}
		if manID > 0 {
			_ = r.carrier.Edit(ctx, r.tgChID, manID, manifest.RenderTombstoneManifest(p))
		}
	}
	if mode == "tombstone" {
		// The caption tombstone outranks a live comment during scans, so it
		// also covers a failed comment edit.
		tombstone()
		return
	}
	if msgID > 0 {
		if err := r.a.TG.DeleteMessage(ctx, r.tgChID, msgID); err != nil && !isMessageGone(err) {
			// Retirement must stay sticky even when the media cannot be
			// deleted: tombstone what remains instead.
			tombstone()
			return
		}
	}
	if manID > 0 {
		_ = r.carrier.Delete(ctx, r.tgChID, manID)
	}
}

// Rename rewrites the record, the caption, and the index for the file's new
// canonical path dst, then collects emptied directories.
func (r *fileRecord) Rename(ctx context.Context, dst string) error {
	a, src := r.a, r.row.CanonicalPath
	existingSlugs, err := a.loadExistingSlugs(ctx, r.channelID)
	if err != nil {
		return err
	}
	if !r.row.MessageID.Valid {
		return apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", src))
	}
	msgID, manID := r.messageID(), r.manifestID()
	size, hash, mime := r.row.Size.Int64, r.row.ContentHash.String, r.row.MIME
	album, inAlbum, err := r.album(ctx)
	if err != nil {
		return telegram.MapError(err)
	}
	if inAlbum {
		updated := albumReplacePath(album, msgID, dst)
		if _, err := a.writeAlbumManifest(ctx, r.channelID, r.tgChID, r.carrier, manID, albumFirstMediaID(updated), updated); err != nil {
			return err
		}
		// The group caption belongs to the whole album, so only the index
		// follows the member.
		if _, err := a.publisher().Reindex(ctx, publisher.ReindexRequest{
			ChannelRowID:   r.channelID,
			FileID:         r.row.ID,
			MessageID:      msgID,
			ManifestMsgID:  manID,
			ManifestChatID: r.row.ManifestChat,
			Meta: manifest.ParsedMeta{
				CanonicalPath: dst,
				DisplayName:   fsmodel.BaseName(dst),
				Size:          size,
				Hash:          hash,
				MIME:          mime,
			},
			ExistingSlugs: existingSlugs,
		}); err != nil {
			return err
		}
		return a.DB.RunDirectoryGC(ctx, r.channelID)
	}
	from := manifest.FileMeta{
		CanonicalPath: src,
		DisplayName:   r.row.DisplayName,
		Size:          size,
		Hash:          hash,
		MIME:          mime,
	}
	to := manifest.FileMeta{
		CanonicalPath: dst,
		DisplayName:   fsmodel.BaseName(dst),
		Size:          size,
		Hash:          hash,
		MIME:          mime,
	}
	if err := a.publisher().Move(ctx, r.ref(), from, to, existingSlugs); err != nil {
		return err
	}
	return a.DB.RunDirectoryGC(ctx, r.channelID)
}

// Rewrite re-applies the record for td repair <path>. A per-file record and
// caption are re-rendered from the index row; an album inventory is
// re-rendered as Telegram holds it and re-pointed on every member row.
func (r *fileRecord) Rewrite(ctx context.Context) error {
	a, p := r.a, r.row.CanonicalPath
	manID := r.manifestID()
	album, inAlbum, err := r.album(ctx)
	if err != nil {
		return telegram.MapError(err)
	}
	if inAlbum {
		_, err := a.writeAlbumManifest(ctx, r.channelID, r.tgChID, r.carrier, manID, albumFirstMediaID(album), album)
		return err
	}
	return a.publisher().Repair(ctx, r.ref(), manifest.FileMeta{
		CanonicalPath: p,
		DisplayName:   r.row.DisplayName,
		Size:          r.row.Size.Int64,
		Hash:          r.row.ContentHash.String,
		MIME:          r.row.MIME,
	}, a.loadSlugMap(ctx, r.channelID))
}

// retireAlbumMember removes the member from its group in every delete mode:
// its media message is deleted, because a scan indexes inventory members
// before it reads caption tombstones, and the shared inventory is rewritten
// without it (or deleted with the last member). err is a failed media delete;
// inventoryErr is a failed inventory rewrite after the media is already gone;
// recordErr is an inventory rewrite that reached Telegram but could not be
// recorded on the sibling rows.
func (r *fileRecord) retireAlbumMember(ctx context.Context, album manifest.AlbumMeta) (inventoryErr, recordErr, err error) {
	messageID, manifestID := r.messageID(), r.manifestID()
	if messageID > 0 {
		if err := r.a.TG.DeleteMessage(ctx, r.tgChID, messageID); err != nil && !isMessageGone(err) {
			return nil, nil, telegram.MapError(err)
		}
	}
	remaining := albumWithout(album, messageID)
	if len(remaining.Files) == 0 {
		inventoryErr = r.carrier.Delete(ctx, r.tgChID, manifestID)
	} else {
		_, inventoryErr = r.a.writeAlbumManifest(ctx, r.channelID, r.tgChID, r.carrier, manifestID, albumFirstMediaID(remaining), remaining)
		if ae, ok := apperr.As(inventoryErr); ok && ae.Code == apperr.ErrDB {
			recordErr, inventoryErr = inventoryErr, nil
		}
	}
	if isMessageGone(inventoryErr) {
		inventoryErr = nil
	}
	return inventoryErr, recordErr, nil
}

func albumWithout(meta manifest.AlbumMeta, messageID int) manifest.AlbumMeta {
	out := manifest.AlbumMeta{GroupedID: meta.GroupedID}
	for _, f := range meta.Files {
		if f.MessageID != messageID {
			out.Files = append(out.Files, f)
		}
	}
	return out
}
