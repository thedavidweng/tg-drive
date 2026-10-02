package sqlitestore

import (
	"context"
	"strings"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

// transfersSQL adds the Transfer table (ADR 0033). Its own migration rather
// than a rewrite of the baseline, so databases already at version 1 gain it.
const transfersSQL = `
create table if not exists transfers (
  id text primary key,
  kind text not null,
  channel_tg_id text not null,
  source text not null,
  dest text not null,
  options text not null default '{}',
  stage text not null,
  bytes_done integer not null default 0,
  bytes_total integer not null default 0,
  items_done integer not null default 0,
  items_total integer not null default 0,
  error_code text not null default '',
  error_message text not null default '',
  front_end text not null,
  owner_token text not null default '',
  lease_expires_at text not null default '',
  cancel_requested integer not null default 0,
  created_at text not null,
  updated_at text not null,
  finished_at text not null default ''
);

create index if not exists idx_transfers_stage on transfers(stage);
create index if not exists idx_transfers_created on transfers(created_at);
`

// transfersFailedItemsSQL adds the failed-item count: multi-item Transfers
// (ADR 0033) record it apart from the done count, so a completed Transfer
// that dropped items shows both. An alter of its own so databases already
// at version 2 gain it.
const transfersFailedItemsSQL = `
alter table transfers add column items_failed integer not null default 0;
`

// TransferRow is one row of the transfers table. Timestamps are RFC3339Nano
// UTC text; empty means unset.
type TransferRow struct {
	ID              string
	Kind            string
	ChannelTGID     string
	Source          string
	Dest            string
	Options         string
	Stage           string
	BytesDone       int64
	BytesTotal      int64
	ItemsDone       int
	ItemsTotal      int
	ItemsFailed     int
	ErrorCode       string
	ErrorMessage    string
	FrontEnd        string
	OwnerToken      string
	LeaseExpiresAt  string
	CancelRequested bool
	CreatedAt       string
	UpdatedAt       string
	FinishedAt      string
}

const transferColumns = `id, kind, channel_tg_id, source, dest, options, stage, bytes_done, bytes_total,
	items_done, items_total, items_failed, error_code, error_message, front_end, owner_token, lease_expires_at,
	cancel_requested, created_at, updated_at, finished_at`

// InsertTransfer records a new Transfer.
func (d *DB) InsertTransfer(ctx context.Context, r TransferRow) error {
	_, err := d.sql.ExecContext(ctx, `insert into transfers(`+transferColumns+`)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Kind, r.ChannelTGID, r.Source, r.Dest, r.Options, r.Stage, r.BytesDone, r.BytesTotal,
		r.ItemsDone, r.ItemsTotal, r.ItemsFailed, r.ErrorCode, r.ErrorMessage, r.FrontEnd, r.OwnerToken, r.LeaseExpiresAt,
		r.CancelRequested, r.CreatedAt, r.UpdatedAt, r.FinishedAt)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "record transfer", err)
	}
	return nil
}

// UpdateTransferState writes a Transfer's progress: its destination, stage,
// byte and item counts, error, and update and finish times. The identity,
// request, and ownership columns are left as they are.
func (d *DB) UpdateTransferState(ctx context.Context, r TransferRow) error {
	_, err := d.sql.ExecContext(ctx, `update transfers set dest=?, stage=?, bytes_done=?, bytes_total=?,
		items_done=?, items_total=?, items_failed=?, error_code=?, error_message=?, updated_at=?, finished_at=?
		where id=?`,
		r.Dest, r.Stage, r.BytesDone, r.BytesTotal, r.ItemsDone, r.ItemsTotal, r.ItemsFailed, r.ErrorCode, r.ErrorMessage,
		r.UpdatedAt, r.FinishedAt, r.ID)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "update transfer", err)
	}
	return nil
}

// DeleteTransfers removes the Transfers with the given ids.
func (d *DB) DeleteTransfers(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err := d.sql.ExecContext(ctx, `delete from transfers where id in (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "delete transfers", err)
	}
	return nil
}

// GetTransfer reads one Transfer; nil when no row has id.
func (d *DB) GetTransfer(ctx context.Context, id string) (*TransferRow, error) {
	rows, err := d.queryTransfers(ctx, `where id=?`, id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// RenewTransferLease moves a running Transfer's lease to expiresAt, done by
// its owner's heartbeat. It reports whether the row was renewed: false when
// the Transfer ended or another owner holds it, which tells the owner to
// stop.
func (d *DB) RenewTransferLease(ctx context.Context, id, owner, expiresAt string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`update transfers set lease_expires_at=? where id=? and owner_token=? and finished_at=''`,
		expiresAt, id, owner)
	if err != nil {
		return false, apperr.Wrap(apperr.ErrDB, "renew transfer lease", err)
	}
	n, err := res.RowsAffected()
	return err == nil && n > 0, nil
}

// TransferCancelRequested reports whether a Transfer's cancellation was
// requested.
func (d *DB) TransferCancelRequested(ctx context.Context, id string) (bool, error) {
	var requested bool
	err := d.sql.QueryRowContext(ctx, `select cancel_requested from transfers where id=?`, id).Scan(&requested)
	if err != nil {
		return false, apperr.Wrap(apperr.ErrDB, "read transfer cancel flag", err)
	}
	return requested, nil
}

// SetTransferCancelRequested marks a Transfer's cancellation requested. It
// reports whether the flag was set: false when no such Transfer exists or
// it already ended.
func (d *DB) SetTransferCancelRequested(ctx context.Context, id string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`update transfers set cancel_requested=1 where id=? and finished_at=''`, id)
	if err != nil {
		return false, apperr.Wrap(apperr.ErrDB, "request transfer cancel", err)
	}
	n, err := res.RowsAffected()
	return err == nil && n > 0, nil
}

// InterruptTransfer marks a non-terminal Transfer interrupted, as read with
// lease leaseExpiresAt. The lease value is part of the condition, so a
// renewal that landed since the read turns the marking into a no-op instead
// of interrupting a live Transfer. It reports whether the row was marked.
func (d *DB) InterruptTransfer(ctx context.Context, id, leaseExpiresAt, now string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`update transfers set stage='interrupted', updated_at=?, finished_at=?
		where id=? and lease_expires_at=? and finished_at=''`,
		now, now, id, leaseExpiresAt)
	if err != nil {
		return false, apperr.Wrap(apperr.ErrDB, "mark transfer interrupted", err)
	}
	n, err := res.RowsAffected()
	return err == nil && n > 0, nil
}

// ListTransfers reads the Transfers in one of stages, every Transfer when
// stages is empty, newest first.
func (d *DB) ListTransfers(ctx context.Context, stages []string) ([]TransferRow, error) {
	where := ""
	args := make([]any, len(stages))
	if len(stages) > 0 {
		where = `where stage in (?` + strings.Repeat(",?", len(stages)-1) + `)`
		for i, s := range stages {
			args[i] = s
		}
	}
	return d.queryTransfers(ctx, where+` order by created_at desc, id`, args...)
}

func (d *DB) queryTransfers(ctx context.Context, tail string, args ...any) ([]TransferRow, error) {
	rows, err := d.sql.QueryContext(ctx, `select `+transferColumns+` from transfers `+tail, args...)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "read transfers", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TransferRow
	for rows.Next() {
		var r TransferRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.ChannelTGID, &r.Source, &r.Dest, &r.Options, &r.Stage,
			&r.BytesDone, &r.BytesTotal, &r.ItemsDone, &r.ItemsTotal, &r.ItemsFailed, &r.ErrorCode, &r.ErrorMessage,
			&r.FrontEnd, &r.OwnerToken, &r.LeaseExpiresAt, &r.CancelRequested, &r.CreatedAt, &r.UpdatedAt,
			&r.FinishedAt); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "read transfers", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "read transfers", err)
	}
	return out, nil
}
