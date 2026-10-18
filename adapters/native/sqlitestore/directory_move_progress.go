package sqlitestore

import (
	"context"
	"database/sql"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

const directoryMoveProgressSQL = `
create table if not exists directory_move_operations (
  id text primary key,
  channel_id integer not null references channels(id),
  source_path text not null,
  requested_destination text not null,
  destination_path text not null,
  total integer not null,
  moved integer not null default 0,
  failed integer not null default 0,
  remaining integer not null,
  status text not null check(status in ('running', 'completed', 'failed')),
  last_path text not null default '',
  error text not null default '',
  created_at text not null,
  updated_at text not null
);

create index if not exists idx_directory_move_operations_channel
  on directory_move_operations(channel_id, updated_at);
`

// StartDirectoryMove records the resolved destination before remote writes.
func (d *DB) StartDirectoryMove(ctx context.Context, id string, channelID int64, source, requestedDestination, destination string, total int, now string) error {
	_, err := d.sql.ExecContext(ctx, `
		insert into directory_move_operations
		(id,channel_id,source_path,requested_destination,destination_path,total,remaining,status,created_at,updated_at)
		values(?,?,?,?,?,?,?, 'running', ?, ?)`,
		id, channelID, source, requestedDestination, destination, total, total, now, now)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "start directory move progress", err)
	}
	return nil
}

// DirectoryMoveRetryDestination keeps a partial move's destination from being
// interpreted as a newly existing directory on the next invocation.
func (d *DB) DirectoryMoveRetryDestination(ctx context.Context, channelID int64, source, requestedDestination string) (string, bool, error) {
	var destination, status string
	err := d.sql.QueryRowContext(ctx, `
		select destination_path,status from directory_move_operations
		where channel_id=? and source_path=? and requested_destination=?
		order by rowid desc limit 1`, channelID, source, requestedDestination).Scan(&destination, &status)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, apperr.Wrap(apperr.ErrDB, "lookup directory move retry", err)
	}
	return destination, status != "completed", nil
}

func (d *DB) UpdateDirectoryMove(ctx context.Context, id, status, lastPath, detail string, moved, failed, remaining int, now string) error {
	result, err := d.sql.ExecContext(ctx, `
		update directory_move_operations
		set status=?, last_path=?, error=?, moved=?, failed=?, remaining=?, updated_at=?
		where id=?`, status, lastPath, detail, moved, failed, remaining, now, id)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "update directory move progress", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return apperr.Wrap(apperr.ErrDB, "update directory move progress", err)
	} else if n != 1 {
		return apperr.New(apperr.ErrDB, "directory move progress not found: "+id)
	}
	return nil
}

// DirectoryMoveProgress is the local checkpoint of one directory move.
type DirectoryMoveProgress struct {
	ID          string
	ChannelID   int64
	Source      string
	Destination string
	Total       int
	Moved       int
	Failed      int
	Remaining   int
	Status      string
	LastPath    string
	Error       string
}

func (d *DB) DirectoryMoveProgress(ctx context.Context, id string) (DirectoryMoveProgress, error) {
	var p DirectoryMoveProgress
	err := d.sql.QueryRowContext(ctx, `
		select id,channel_id,source_path,destination_path,total,moved,failed,remaining,status,last_path,error
		from directory_move_operations where id=?`, id).Scan(
		&p.ID, &p.ChannelID, &p.Source, &p.Destination, &p.Total, &p.Moved,
		&p.Failed, &p.Remaining, &p.Status, &p.LastPath, &p.Error,
	)
	if err == sql.ErrNoRows {
		return p, apperr.New(apperr.ErrRemoteNotFound, "directory move progress not found: "+id)
	}
	if err != nil {
		return p, apperr.Wrap(apperr.ErrDB, "read directory move progress", err)
	}
	return p, nil
}
