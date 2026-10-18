package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
	"github.com/thedavidweng/tg-drive/core/telegram"

	"lukechampine.com/blake3"
)

// DuplicateFile is one active indexed file with a content hash.
type DuplicateFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// DuplicateGroup contains active files with identical content hashes.
type DuplicateGroup struct {
	Hash  string          `json:"hash"`
	Size  int64           `json:"size"`
	Files []DuplicateFile `json:"files"`
}

// DedupeResult is a read-only duplicate audit. ReclaimableBytes is an
// estimate: one copy in each group is retained and no Telegram rows change.
type DedupeResult struct {
	Path             string           `json:"path"`
	Groups           []DuplicateGroup `json:"groups"`
	HashedFiles      int              `json:"hashed_files"`
	UnhashedFiles    int              `json:"unhashed_files"`
	ReclaimableBytes int64            `json:"reclaimable_bytes"`
}

// DedupeDeleteOptions controls the destructive duplicate cleanup operation.
type DedupeDeleteOptions struct {
	Confirm bool
}

func (o DedupeDeleteOptions) Validate() error {
	if !o.Confirm {
		return apperr.New(apperr.ErrConfirmationRequired, "deleting duplicate files requires --confirm")
	}
	return nil
}

// DedupeDeleteResult reports a cleanup that retains the lexicographically
// first path in each duplicate group and deletes the remaining paths.
type DedupeDeleteResult struct {
	Path             string   `json:"path"`
	Groups           int      `json:"groups"`
	Retained         int      `json:"retained"`
	Deleted          int      `json:"deleted"`
	DeletedPaths     []string `json:"deleted_paths"`
	ReclaimableBytes int64    `json:"reclaimable_bytes"`
}

// FindDuplicates audits active indexed files under remotePath. It only uses
// durable hashes already in the index; files without hashes are reported but
// never guessed to be duplicates.
func (a *App) FindDuplicates(ctx context.Context, remotePath string) (*DedupeResult, error) {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	prefix := p
	if prefix != "/" {
		prefix += "/"
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select canonical_path, coalesce(size, 0), coalesce(content_hash, '')
		from files
		where channel_id=? and status='active' and (canonical_path=? or canonical_path like ? escape '\')
		order by canonical_path`, ch.rowID, p, escapeLike(prefix)+"%")
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "find duplicates", err)
	}
	defer func() { _ = rows.Close() }()

	result := &DedupeResult{Path: p, Groups: []DuplicateGroup{}}
	groups := map[string]*DuplicateGroup{}
	for rows.Next() {
		var path, hash string
		var size int64
		if err := rows.Scan(&path, &size, &hash); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "read duplicate audit", err)
		}
		if hash == "" {
			result.UnhashedFiles++
			continue
		}
		result.HashedFiles++
		group := groups[hash]
		if group == nil {
			group = &DuplicateGroup{Hash: hash, Size: size}
			groups[hash] = group
		}
		group.Files = append(group.Files, DuplicateFile{Path: path, Size: size})
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "read duplicate audit", err)
	}
	for _, group := range groups {
		if len(group.Files) < 2 {
			continue
		}
		result.Groups = append(result.Groups, *group)
		result.ReclaimableBytes += group.Size * int64(len(group.Files)-1)
	}
	// Hashes are the stable ordering key, so JSON and human output are
	// deterministic even when SQLite's query plan changes.
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i].Hash < result.Groups[j].Hash })
	return result, nil
}

// DeleteDuplicates removes the duplicate members reported by FindDuplicates.
// All group paths are locked and their remote bytes verified before the first
// mutation. The index alone cannot prove a retained copy still exists.
func (a *App) DeleteDuplicates(ctx context.Context, remotePath string, opts DedupeDeleteOptions) (*DedupeDeleteResult, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if err := cancelled(ctx); err != nil {
		return nil, err
	}
	audit, err := a.FindDuplicates(ctx, remotePath)
	if err != nil {
		return nil, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	result := &DedupeDeleteResult{
		Path:             audit.Path,
		Groups:           len(audit.Groups),
		Retained:         len(audit.Groups),
		ReclaimableBytes: audit.ReclaimableBytes,
		DeletedPaths:     []string{},
	}
	var paths []string
	for _, group := range audit.Groups {
		for _, file := range group.Files {
			paths = append(paths, file.Path)
		}
	}
	if len(paths) == 0 {
		return result, nil
	}
	err = a.operate(ctx, ch, paths, func(ctx context.Context) error {
		for _, group := range audit.Groups {
			for _, file := range group.Files {
				row, err := a.duplicateRow(ctx, ch.rowID, file, group.Hash)
				if err != nil {
					return err
				}
				hw := &countingHasher{h: blake3.New(32, nil)}
				if err := a.TG.DownloadMedia(ctx, ch.tgID, int(row.MessageID.Int64), hw); err != nil {
					return telegram.MapError(err)
				}
				if hw.n != file.Size || "blake3:"+hex.EncodeToString(hw.h.Sum(nil)) != group.Hash {
					return apperr.New(apperr.ErrPathConflict, fmt.Sprintf("remote bytes for %q differ from the duplicate audit; resolve the changed content before retrying", file.Path))
				}
			}
		}
		for _, group := range audit.Groups {
			for _, file := range group.Files[1:] {
				// Album retirement can replace a shared manifest pointer on
				// sibling rows, so read the current row for each deletion.
				row, err := a.duplicateRow(ctx, ch.rowID, file, group.Hash)
				if err != nil {
					return err
				}
				deleted, err := a.deleteFileLocked(ctx, ch.rowID, ch.tgID, row, DeleteOptions{Confirm: true})
				if deleted != nil {
					result.Deleted++
					result.DeletedPaths = append(result.DeletedPaths, file.Path)
				}
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return result, withDedupeProgress(err, result)
	}
	return result, nil
}

func (a *App) duplicateRow(ctx context.Context, channelID int64, file DuplicateFile, hash string) (sqlitestore.FileRow, error) {
	if err := cancelled(ctx); err != nil {
		return sqlitestore.FileRow{}, err
	}
	row, found, err := a.DB.ActiveByPath(ctx, channelID, file.Path)
	if err != nil {
		return row, apperr.Wrap(apperr.ErrDB, "lookup duplicate", err)
	}
	if !found || !row.MessageID.Valid {
		return row, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("duplicate path %q disappeared during cleanup", file.Path))
	}
	if row.ContentHash.String != hash || row.Size.Int64 != file.Size {
		return row, apperr.New(apperr.ErrPathConflict, fmt.Sprintf("duplicate path %q changed since the audit", file.Path))
	}
	return row, nil
}

func withDedupeProgress(err error, result *DedupeDeleteResult) error {
	if ae, ok := apperr.As(err); ok {
		details := map[string]any{}
		for key, value := range ae.Details {
			details[key] = value
		}
		details["path"] = result.Path
		details["groups"] = result.Groups
		details["retained"] = result.Retained
		details["deleted"] = result.Deleted
		details["deleted_paths"] = result.DeletedPaths
		return ae.WithDetails(details)
	}
	return err
}
