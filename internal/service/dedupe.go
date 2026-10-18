package service

import (
	"context"
	"sort"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
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
