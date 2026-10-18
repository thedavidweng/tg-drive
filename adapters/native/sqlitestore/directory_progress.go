package sqlitestore

import (
	"context"
	"database/sql"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

const directoryDeleteProgressSQL = `
create table if not exists directory_delete_operations (
  id text primary key,
  channel_id integer not null references channels(id),
  path text not null,
  mode text not null,
  total integer not null,
  deleted integer not null default 0,
  failed integer not null default 0,
  remaining integer not null,
  status text not null check(status in ('running', 'completed', 'failed')),
  last_path text not null default '',
  error text not null default '',
  created_at text not null,
  updated_at text not null
);

create index if not exists idx_directory_delete_operations_channel
  on directory_delete_operations(channel_id, updated_at);
`

// StartDirectoryDelete records a directory mutation before its first remote
// write, so a process interruption still leaves a durable operation trail.
func (d *DB) StartDirectoryDelete(ctx context.Context, id string, channelID int64, path, mode string, total int, now string) error {
	_, err := d.sql.ExecContext(ctx, `
		insert into directory_delete_operations
		(id,channel_id,path,mode,total,remaining,status,created_at,updated_at)
		values(?,?,?,?,?,?, 'running', ?, ?)`,
		id, channelID, path, mode, total, total, now, now)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "start directory delete progress", err)
	}
	return nil
}

// UpdateDirectoryDelete persists the latest durable batch counters. The
// operation id is immutable, so retries create a separate history record.
func (d *DB) UpdateDirectoryDelete(ctx context.Context, id, status, lastPath, detail string, deleted, failed, remaining int, now string) error {
	result, err := d.sql.ExecContext(ctx, `
		update directory_delete_operations
		set status=?, last_path=?, error=?, deleted=?, failed=?, remaining=?, updated_at=?
		where id=?`, status, lastPath, detail, deleted, failed, remaining, now, id)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "update directory delete progress", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return apperr.Wrap(apperr.ErrDB, "update directory delete progress", err)
	} else if n != 1 {
		return apperr.New(apperr.ErrDB, "directory delete progress not found: "+id)
	}
	return nil
}

// DirectoryDeleteProgress is the durable state of one directory mutation.
type DirectoryDeleteProgress struct {
	ID        string
	ChannelID int64
	Path      string
	Mode      string
	Total     int
	Deleted   int
	Failed    int
	Remaining int
	Status    string
	LastPath  string
	Error     string
}

// DirectoryDeleteProgress returns one persisted mutation by id.
func (d *DB) DirectoryDeleteProgress(ctx context.Context, id string) (DirectoryDeleteProgress, error) {
	var p DirectoryDeleteProgress
	err := d.sql.QueryRowContext(ctx, `
		select id,channel_id,path,mode,total,deleted,failed,remaining,status,last_path,error
		from directory_delete_operations where id=?`, id).Scan(
		&p.ID, &p.ChannelID, &p.Path, &p.Mode, &p.Total, &p.Deleted,
		&p.Failed, &p.Remaining, &p.Status, &p.LastPath, &p.Error,
	)
	if err == sql.ErrNoRows {
		return p, apperr.New(apperr.ErrRemoteNotFound, "directory delete progress not found: "+id)
	}
	if err != nil {
		return p, apperr.Wrap(apperr.ErrDB, "read directory delete progress", err)
	}
	return p, nil
}
