package service

import (
	"context"
	"database/sql"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
)

// Mkdir creates an empty directory in the virtual tree, creating missing
// ancestors the same way. Telegram cannot store empty directories
// (ERR_EMPTY_DIRS_UNSUPPORTED), so the directory is an ephemeral node: it
// lives only in the local index, survives directory GC, and disappears on
// the next full scan unless a file was uploaded into it (DECISIONS.md).
func (a *App) Mkdir(ctx context.Context, remotePath string) error {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return err
	}
	if p == "/" {
		return apperr.New(apperr.ErrPathExists, "path already exists: /")
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return err
	}
	active, err := a.activePaths(ctx, ch.rowID)
	if err != nil {
		return err
	}
	for _, ap := range active {
		switch {
		case ap.Canonical == p:
			return apperr.New(apperr.ErrPathExists, "path already exists: "+p)
		case !ap.IsDir && strings.HasPrefix(p+"/", ap.Canonical+"/"):
			return apperr.New(apperr.ErrPathAncestorIsFile, "ancestor path is a file: "+ap.Canonical)
		}
	}
	return a.operate(ctx, ch, []string{p}, func(ctx context.Context) error {
		return a.DB.WithTx(ctx, func(tx *sql.Tx) error {
			now := time.Now().UTC().Format(time.RFC3339)
			for _, dir := range append(fsmodel.AncestorPaths(p), p) {
				if _, err := tx.ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,ephemeral,created_at,updated_at) values(?,?,?,?,'dir',1,1,?,?)`,
					ch.rowID, dir, fsmodel.ParentPath(dir), fsmodel.BaseName(dir), now, now); err != nil {
					return apperr.Wrap(apperr.ErrDB, "create directory", err)
				}
			}
			return nil
		})
	})
}
