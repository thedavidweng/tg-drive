package service

import (
	"context"
	"fmt"
	"strconv"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// ListChannels returns channels visible to the logged-in user.
func (a *App) ListChannels(ctx context.Context, onlyDrive bool) ([]telegram.Channel, error) {
	return a.TG.ListChannels(ctx, telegram.ListChannelsOptions{OnlyDrive: onlyDrive})
}

// BoundChannels lists the channels bound to local roots in the index, in
// binding order. It reads only the index, never Telegram.
func (a *App) BoundChannels(ctx context.Context) ([]BoundChannel, error) {
	return a.boundChannels(ctx)
}

// DiscussionGroup returns the discussion group linked to the bound channel,
// or nil when none is linked (ADR 0018). It reads only the index.
func (a *App) DiscussionGroup(ctx context.Context) (*LinkDiscussionResult, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	tgID, _, title, err := a.DB.DiscussionGroup(ctx, ch.rowID)
	if err != nil {
		return nil, err
	}
	if tgID == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(tgID, 10, 64)
	if err != nil {
		return nil, apperr.New(apperr.ErrDB, "stored discussion group id is not numeric: "+tgID)
	}
	return &LinkDiscussionResult{DiscussionChannelID: id, DiscussionTitle: title}, nil
}

// ChannelPermissions is what the account may do on the bound channel, from
// the same Telegram capability check td doctor reports.
type ChannelPermissions struct {
	Upload       bool
	Delete       bool
	EditCaptions bool
	InviteLink   bool
}

// ChannelPermissions checks the account's permissions on the bound channel
// through the Telegram capability layer.
func (a *App) ChannelPermissions(ctx context.Context) (*ChannelPermissions, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	caps, err := a.TG.Doctor(ctx, ch.tgID)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	return &ChannelPermissions{
		Upload:       caps.UploadOK,
		Delete:       caps.DeleteOK,
		EditCaptions: caps.EditOldCaptionOK,
		InviteLink:   caps.InviteLinkOK,
	}, nil
}

// LinkDiscussionResult identifies the discussion group linked to the bound
// channel.
type LinkDiscussionResult struct {
	DiscussionChannelID int64  `json:"discussion_channel_id"`
	DiscussionTitle     string `json:"discussion_title"`
}

// LinkDiscussionGroup ensures the bound channel has a linked discussion
// group (creating one when needed) and records it on the channel row
// (ADR 0018).
func (a *App) LinkDiscussionGroup(ctx context.Context) (*LinkDiscussionResult, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	group, err := a.TG.EnsureDiscussionGroup(ctx, ch.tgID)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	if err := a.DB.SetDiscussionGroup(ctx, ch.rowID, fmt.Sprintf("%d", group.ID), fmt.Sprintf("%d", group.AccessHash), group.Title); err != nil {
		return nil, err
	}
	return &LinkDiscussionResult{DiscussionChannelID: group.ID, DiscussionTitle: group.Title}, nil
}
