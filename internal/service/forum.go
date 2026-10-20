package service

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// InitForumRoot binds an existing topic, never creates a forum or topic, and
// scans only that root. Forum records are replies in the same topic; a forum
// is not a broadcast channel with a linked discussion group.
func (a *App) InitForumRoot(ctx context.Context, localRoot, peer string, topicID int) (*InitRootResult, error) {
	if topicID <= 0 {
		return nil, apperr.New(apperr.ErrUsage, "--topic must be positive")
	}
	root, err := filepath.Abs(localRoot)
	if err != nil {
		return nil, err
	}
	user, ok, err := a.TG.Status(ctx)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	if !ok {
		return nil, apperr.New(apperr.ErrAuthRequired, "not logged in; run: td auth login")
	}
	ch, err := a.TG.ResolveChannel(ctx, peer)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	topic, err := a.TG.ResolveForumTopic(ctx, ch.ID, topicID)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	if topic.Closed || topic.Hidden {
		return nil, apperr.New(apperr.ErrChannelPermission, "forum topic is closed or hidden")
	}
	caps, err := a.TG.Doctor(telegram.WithForumTopic(ctx, ch.ID, topicID), ch.ID)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	if !caps.ChannelOK || !caps.UploadOK || !caps.EditOldCaptionOK || !caps.DeleteOK {
		return nil, apperr.New(apperr.ErrChannelPermission, "forum binding requires visible topic, upload, edit and delete permissions")
	}
	if _, err := a.TG.History(telegram.WithForumTopic(ctx, ch.ID, topicID), ch.ID, 0, 1); err != nil {
		return nil, telegram.MapError(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.DB.Raw().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var accountID int64
	err = tx.QueryRowContext(ctx, `insert into accounts(tg_user_id,phone,display_name,created_at,updated_at) values(?,?,?,?,?)
 on conflict(tg_user_id) do update set updated_at=excluded.updated_at returning id`, fmt.Sprint(user.ID), user.Phone, user.DisplayName, now, now).Scan(&accountID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "bind forum account", err)
	}
	var conflicts int
	err = tx.QueryRowContext(ctx, `select count(*) from channels where account_id=? and ((root_local_path=? and (tg_channel_id<>? or topic_id<>?)) or (tg_channel_id=? and topic_id=0))`, accountID, root, fmt.Sprint(ch.ID), topicID, fmt.Sprint(ch.ID)).Scan(&conflicts)
	if err != nil {
		return nil, err
	}
	if conflicts != 0 {
		return nil, apperr.New(apperr.ErrUsage, "root is already bound or forum has an unscoped legacy binding; use a distinct root and resolve the legacy binding first")
	}
	_, err = tx.ExecContext(ctx, `insert into channels(account_id,tg_channel_id,topic_id,access_hash,title,root_local_path,root_remote_path,strategy,created_at,updated_at)
 values(?,?,?,?,?,?,'/','single',?,?) on conflict(account_id,tg_channel_id,topic_id) do update set title=excluded.title,access_hash=excluded.access_hash,root_local_path=excluded.root_local_path,updated_at=excluded.updated_at`, accountID, fmt.Sprint(ch.ID), topicID, fmt.Sprint(ch.AccessHash), ch.Title, root, now, now)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "bind forum topic", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := &InitRootResult{ChannelID: ch.ID, ChannelTitle: ch.Title, LocalRoot: root, TopicID: topicID}
	scanned, err := a.Scan(WithLocalRoot(ctx, root), ScanOptions{Full: true})
	if err != nil {
		out.ScanError = err.Error()
	} else {
		out.IndexedFiles = &scanned.Active
	}
	return out, nil
}
