package service

import (
	"context"
	"database/sql"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
)

// Per-file renames collect derived folders. Move the remaining local folders
// only after those writes finish, so a failed batch keeps them for its retry.
func (a *App) moveLocalDirectoryNodes(ctx context.Context, channelID int64, source, destination string) error {
	return a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			select canonical_path from nodes where channel_id=? and type='dir'
			and (canonical_path=? or substr(canonical_path,1,length(?)+1)=?||'/')
			order by length(canonical_path), canonical_path`, channelID, source, source, source)
		if err != nil {
			return apperr.Wrap(apperr.ErrDB, "read local directory move", err)
		}
		var paths []string
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				_ = rows.Close()
				return apperr.Wrap(apperr.ErrDB, "read local directory move", err)
			}
			paths = append(paths, path)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return apperr.Wrap(apperr.ErrDB, "read local directory move", err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		for _, path := range paths {
			target := destination + strings.TrimPrefix(path, source)
			for _, dir := range append(fsmodel.AncestorPaths(target), target) {
				if _, err := tx.ExecContext(ctx, `
					insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,ephemeral,created_at,updated_at)
					values(?,?,?,?,'dir',1,1,?,?)`,
					channelID, dir, fsmodel.ParentPath(dir), fsmodel.BaseName(dir), now, now); err != nil {
					return apperr.Wrap(apperr.ErrDB, "write local directory move", err)
				}
			}
			if _, err := tx.ExecContext(ctx, `
				update nodes set ephemeral=1, updated_at=?
				where channel_id=? and canonical_path=?`, now, channelID, target); err != nil {
				return apperr.Wrap(apperr.ErrDB, "preserve moved local directory", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			delete from nodes where channel_id=? and type='dir'
			and (canonical_path=? or substr(canonical_path,1,length(?)+1)=?||'/')`,
			channelID, source, source, source); err != nil {
			return apperr.Wrap(apperr.ErrDB, "remove moved local directories", err)
		}
		return nil
	})
}
