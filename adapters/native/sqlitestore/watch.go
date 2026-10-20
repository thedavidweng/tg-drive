package sqlitestore

import (
	"context"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

const watchCheckpointsSQL = `
create table watch_checkpoints (
 channel_id integer not null references channels(id),
 local_root text not null,
 remote_root text not null,
 relative_path text not null,
 content_hash text not null,
 size integer not null,
 primary key(channel_id,local_root,remote_root,relative_path)
);
`

type WatchCheckpoint struct {
	Hash string
	Size int64
}

func (d *DB) WatchCheckpoints(ctx context.Context, channelID int64, local, remote string) (map[string]WatchCheckpoint, error) {
	rows, err := d.sql.QueryContext(ctx, `select relative_path,content_hash,size from watch_checkpoints where channel_id=? and local_root=? and remote_root=?`, channelID, local, remote)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "read watch checkpoints", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]WatchCheckpoint{}
	for rows.Next() {
		var name string
		var entry WatchCheckpoint
		if err := rows.Scan(&name, &entry.Hash, &entry.Size); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "read watch checkpoint", err)
		}
		out[name] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "read watch checkpoints", err)
	}
	return out, nil
}

func (d *DB) SaveWatchCheckpoint(ctx context.Context, channelID int64, local, remote, name, hash string, size int64) error {
	_, err := d.sql.ExecContext(ctx, `insert into watch_checkpoints(channel_id,local_root,remote_root,relative_path,content_hash,size) values(?,?,?,?,?,?) on conflict(channel_id,local_root,remote_root,relative_path) do update set content_hash=excluded.content_hash,size=excluded.size`, channelID, local, remote, name, hash, size)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "save watch checkpoint", err)
	}
	return nil
}

func (d *DB) DeleteWatchCheckpoint(ctx context.Context, channelID int64, local, remote, name string) error {
	_, err := d.sql.ExecContext(ctx, `delete from watch_checkpoints where channel_id=? and local_root=? and remote_root=? and relative_path=?`, channelID, local, remote, name)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "delete watch checkpoint", err)
	}
	return nil
}
