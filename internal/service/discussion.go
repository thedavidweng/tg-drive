package service

import (
	"context"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// discussionChat returns the linked discussion group's Telegram channel id,
// the carrier of new machine records (ADR 0018). It is resolved on first use
// and kept: reads, scans, and deletes work on legacy channels without one,
// while use cases that write records call it before their first write so
// they fail early.
func (c *channelContext) discussionChat(ctx context.Context) (string, error) {
	c.discussionMu.Lock()
	defer c.discussionMu.Unlock()
	if c.discussion != "" {
		return c.discussion, nil
	}
	tgID, _, _, err := c.app.DB.DiscussionGroup(ctx, c.rowID)
	if err != nil {
		return "", err
	}
	if tgID == "" {
		return "", apperr.New(apperr.ErrDiscussionMissing,
			"uploads need a linked discussion group for machine records; run: td channels link-discussion")
	}
	c.discussion = tgID
	return tgID, nil
}

// manifestCarrier builds the machine-record carrier bound to this app's
// Telegram client. chatID is the files.manifest_chat_tg_id value (or
// discussionChat's result); "" selects the legacy in-channel carrier.
func (a *App) manifestCarrier(chatID string) telegram.ManifestCarrier {
	return telegram.NewManifestCarrier(a.TG, chatID)
}
