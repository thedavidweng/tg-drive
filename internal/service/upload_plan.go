package service

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Shared planning helpers for the two upload paths: single-file uploadLocked
// and multi-file planAlbumMember. Planning is everything that happens before
// bytes move to Telegram: destination classification, hashing, pending-row
// reconciliation, and caption rendering. Both paths must classify and stage
// identically or a retried album member and a retried single upload would
// diverge in behavior.

// destOccupancy classifies what occupies the destination: an active row
// blocks per conflict policy; a message-less pending row is a failed upload
// that a retry adopts rather than wedging the path.
func (a *App) destOccupancy(ctx context.Context, channelRowID int64, dest string) (activeExists, pendingAdoptable bool, err error) {
	_, activeExists, err = a.DB.ActiveByPath(ctx, channelRowID, dest)
	if err != nil {
		return false, false, apperr.Wrap(apperr.ErrDB, "lookup destination state", err)
	}
	pending, found, err := a.DB.PendingByPath(ctx, channelRowID, dest)
	if err != nil {
		return false, false, apperr.Wrap(apperr.ErrDB, "lookup destination state", err)
	}
	return activeExists, found && !pending.MessageID.Valid, nil
}

// applyUploadPolicy resolves the canonical destination against its active
// occupant. ok is false when the conflict policy dropped the file
// (--skip-existing). allowReplace lets the single-file path supersede the
// occupant (--replace resolves its supersede target separately); the album
// path rejects replace before planning. remedies names the caller's valid
// remedies in the already-exists error.
func applyUploadPolicy(dest string, policy ConflictPolicy, activeExists, allowReplace bool, active []fsmodel.ActivePath, remedies string) (string, bool, error) {
	if !activeExists || (allowReplace && policy == ConflictReplace) {
		return dest, true, nil
	}
	switch policy {
	case ConflictSkip:
		return "", false, nil
	case ConflictRename:
		base := fsmodel.BaseName(dest)
		prefix := fsmodel.ParentPath(dest)
		if prefix != "/" {
			prefix += "/"
		}
		for i := 1; i <= 1000; i++ {
			candidate, err := fsmodel.NormalizeCanonicalPath(prefix + fsmodel.ConflictRenameCandidate(base, i))
			if err != nil {
				return "", false, err
			}
			taken := false
			for _, ap := range active {
				if ap.Canonical == candidate {
					taken = true
					break
				}
			}
			if !taken {
				return candidate, true, nil
			}
		}
		return "", false, apperr.New(apperr.ErrPathExists,
			fmt.Sprintf("no free auto-rename candidate for %q", dest))
	default:
		return "", false, apperr.New(apperr.ErrPathExists,
			fmt.Sprintf("remote file %q already exists (%s)", dest, remedies))
	}
}

// uploadCheckSet drops the destination's file row from the file/dir
// invariant checks when this upload supersedes or adopts whatever occupies
// it. The active slice carries active AND pending rows (ActivePaths feeds
// conflict checks), so both --replace and the adoptable-pending case need the
// exclusion or a retry could never pass its own destination check.
func uploadCheckSet(active []fsmodel.ActivePath, dest string, supersede bool) []fsmodel.ActivePath {
	if !supersede {
		return active
	}
	checkSet := make([]fsmodel.ActivePath, 0, len(active))
	for _, ap := range active {
		if !ap.IsDir && ap.Canonical == dest {
			continue
		}
		checkSet = append(checkSet, ap)
	}
	return checkSet
}

// hashUpload reads the file once to compute its content hash. The hash is
// resume identity, so it must never be skipped for a resumable big file:
// --no-hash is honored only for small files.
func (a *App) hashUpload(ctx context.Context, localPath string, noHash bool, size int64) (string, error) {
	hashEnabled := a.Cfg.Hash.Enabled && !noHash
	if size > telegram.ResumableBigFileBytes {
		hashEnabled = true
	}
	hashReader, err := a.files().Open(ctx, localPath)
	if err != nil {
		return "", apperr.Wrap(apperr.ErrLocalNotFound, "hash file", err)
	}
	contentHash, err := computeHash(hashReader, hashEnabled)
	_ = hashReader.Close()
	if err != nil {
		return "", apperr.Wrap(apperr.ErrLocalNotFound, "hash file", err)
	}
	return contentHash, nil
}

// pendingResolution classifies the pending row occupying a destination
// (rowID 0 when none exists).
type pendingResolution struct {
	rowID int64
	msgID sql.NullInt64
	size  sql.NullInt64
	hash  sql.NullString
}

// lookupPending reads the pending row at dest, if any. The caller holds the
// lock on dest.
func (a *App) lookupPending(ctx context.Context, channelRowID int64, dest string) (pendingResolution, error) {
	row, found, err := a.DB.PendingByPath(ctx, channelRowID, dest)
	if err != nil {
		return pendingResolution{}, apperr.Wrap(apperr.ErrDB, "lookup pending destination", err)
	}
	if !found {
		return pendingResolution{}, nil
	}
	return pendingResolution{rowID: row.ID, msgID: row.MessageID, size: row.Size, hash: row.ContentHash}, nil
}

// matches reports whether the pending row describes the same content as this
// upload (same size; hash equal whenever the row recorded one) and can be
// adopted.
func (p pendingResolution) matches(size int64, contentHash string) bool {
	sizeMatch := p.size.Valid && p.size.Int64 == size
	hashMatch := !p.hash.Valid || p.hash.String == "" || p.hash.String == contentHash
	return sizeMatch && hashMatch
}

// errUnpublishedUpload is the shared crash-window refusal: the media exists
// on Telegram but was never published, so re-uploading would duplicate it.
func errUnpublishedUpload(dest string, msgID int64) error {
	return apperr.New(apperr.ErrPathExists,
		fmt.Sprintf("an unpublished upload of %q is waiting at message %d; run: td repair --orphaned", dest, msgID))
}

// stagePendingRow commits the planning outcome for one destination: adopt
// adoptFileID (re-pointing the interrupted row at this attempt, which reuses
// its resumable state key) or insert a fresh pending row. resumed reports
// whether the adopted row carries confirmed resumable bytes. The caller
// holds the lock on dest.
func (a *App) stagePendingRow(ctx context.Context, channelRowID int64, dest, localPath string, size int64, contentHash, mime, now string, adoptFileID int64) (fileID int64, resumed bool, err error) {
	// Without an adopted row a fresh pending row is inserted. Under --replace
	// the old active row stays untouched until the new upload fully
	// succeeds, so a failed upload can never lose the existing index entry.
	fileID, err = a.DB.StagePending(ctx, sqlitestore.PendingRow{
		ChannelRowID:  channelRowID,
		CanonicalPath: dest,
		DisplayName:   fsmodel.BaseName(dest),
		LocalPath:     localPath,
		Size:          size,
		ContentHash:   contentHash,
		MIME:          mime,
		Now:           now,
	}, adoptFileID)
	if err != nil {
		if adoptFileID > 0 {
			return 0, false, apperr.Wrap(apperr.ErrDB, "adopt pending row", err)
		}
		return 0, false, apperr.Wrap(apperr.ErrDB, "insert pending", err)
	}
	if adoptFileID > 0 {
		if st, lerr := a.DB.LoadUploadState(ctx, fmt.Sprintf("file:%d", adoptFileID)); lerr == nil && st != nil && st.ConfirmedBytes > 0 {
			return fileID, true, nil
		}
	}
	return fileID, false, nil
}

// renderUploadMetaWithCaption builds publication metadata and a human caption
// with the source message's
// own caption kept on top of the rendered block. Imports (td import saved)
// carry the original text into the republished message; ordinary uploads pass
// an empty prefix and render exactly as before.
func (a *App) renderUploadMetaWithCaption(dest, localPath string, size int64, contentHash, now string, existingSlugs map[string]string, humanPrefix string) (meta manifest.FileMeta, capRes manifest.CaptionResult, tags []string, slugMaps []pathcodec.SlugMapping, err error) {
	meta = manifest.FileMeta{
		CanonicalPath: dest,
		DisplayName:   fsmodel.BaseName(dest),
		ParentHuman:   fsmodel.HumanParent(dest),
		Size:          size,
		Hash:          contentHash,
		MIME:          detectMIME(localPath),
		Created:       now,
	}
	tags, slugMaps, err = pathcodec.GenerateChain(dest, existingSlugs)
	if err != nil {
		return meta, capRes, nil, nil, err
	}
	meta.Tags = tags
	capRes, err = manifest.RenderCaptionWithPrefix(meta, humanPrefix, a.Cfg.Caption.SafeMediaCaptionUTF16Units, a.Cfg.Caption.MarginUTF16Units)
	if err != nil {
		return meta, capRes, nil, nil, err
	}
	return meta, capRes, tags, slugMaps, nil
}
