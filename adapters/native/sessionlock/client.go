package sessionlock

import (
	"context"
	"io"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Client wraps a Telegram client so every call first holds the Session
// lock. The inner client is a field, not embedded, so a method added to
// telegram.Client fails to compile here instead of bypassing the lock.
type Client struct {
	inner telegram.Client
	lock  *Lock
}

var _ telegram.Client = (*Client)(nil)

// Wrap guards inner with lock.
func Wrap(inner telegram.Client, lock *Lock) *Client {
	return &Client{inner: inner, lock: lock}
}

// Close disconnects the inner client, then releases the Session lock, so the
// next holder never overlaps a live connection.
func (c *Client) Close() error {
	var err error
	if closer, ok := c.inner.(interface{ Close() error }); ok {
		err = closer.Close()
	}
	if relErr := c.lock.Release(); err == nil {
		err = relErr
	}
	return err
}

func (c *Client) Login(ctx context.Context, apiID int64, apiHash, phone string, codeFn telegram.CodeFunc, passwordFn telegram.PasswordFunc, opts telegram.LoginOptions) (*telegram.LoginResult, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.Login(ctx, apiID, apiHash, phone, codeFn, passwordFn, opts)
}

func (c *Client) Status(ctx context.Context) (*telegram.User, bool, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, false, err
	}
	return c.inner.Status(ctx)
}

func (c *Client) Logout(ctx context.Context) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.Logout(ctx)
}

func (c *Client) CreateChannel(ctx context.Context, title string) (*telegram.Channel, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.CreateChannel(ctx, title)
}

func (c *Client) ResolveChannel(ctx context.Context, titleOrID string) (*telegram.Channel, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.ResolveChannel(ctx, titleOrID)
}

func (c *Client) GetInviteLink(ctx context.Context, channelID int64) (string, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return "", err
	}
	return c.inner.GetInviteLink(ctx, channelID)
}

func (c *Client) ListChannels(ctx context.Context, opts telegram.ListChannelsOptions) ([]telegram.Channel, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.ListChannels(ctx, opts)
}

func (c *Client) UploadMedia(ctx context.Context, req telegram.UploadRequest) (*telegram.UploadResult, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.UploadMedia(ctx, req)
}

func (c *Client) UploadMediaGroup(ctx context.Context, reqs []telegram.UploadRequest) ([]telegram.UploadResult, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.UploadMediaGroup(ctx, reqs)
}

func (c *Client) SendTextReply(ctx context.Context, channelID int64, replyTo int, text string) (int, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return 0, err
	}
	return c.inner.SendTextReply(ctx, channelID, replyTo, text)
}

func (c *Client) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.EditCaption(ctx, channelID, messageID, caption)
}

func (c *Client) EditText(ctx context.Context, channelID int64, messageID int, text string) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.EditText(ctx, channelID, messageID, text)
}

func (c *Client) DeleteMessage(ctx context.Context, channelID int64, messageID int) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.DeleteMessage(ctx, channelID, messageID)
}

func (c *Client) DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.DownloadMedia(ctx, channelID, messageID, dst)
}

func (c *Client) Doctor(ctx context.Context, channelID int64) (*telegram.Capabilities, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.Doctor(ctx, channelID)
}

func (c *Client) History(ctx context.Context, channelID int64, afterID, limit int) ([]telegram.Message, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.History(ctx, channelID, afterID, limit)
}

func (c *Client) StreamHistory(ctx context.Context, channelID int64, afterID int, fn func(telegram.Message) error) (telegram.HistoryMeta, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return telegram.HistoryMeta{}, err
	}
	return c.inner.StreamHistory(ctx, channelID, afterID, fn)
}

func (c *Client) GetMessage(ctx context.Context, channelID int64, messageID int) (telegram.Message, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return telegram.Message{}, err
	}
	return c.inner.GetMessage(ctx, channelID, messageID)
}

func (c *Client) EnsureDiscussionGroup(ctx context.Context, channelID int64) (*telegram.Channel, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, err
	}
	return c.inner.EnsureDiscussionGroup(ctx, channelID)
}

func (c *Client) LinkedDiscussionGroup(ctx context.Context, channelID int64) (*telegram.Channel, bool, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return nil, false, err
	}
	return c.inner.LinkedDiscussionGroup(ctx, channelID)
}

func (c *Client) SendThreadReply(ctx context.Context, channelID int64, postMsgID int, text string) (int, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return 0, err
	}
	return c.inner.SendThreadReply(ctx, channelID, postMsgID, text)
}

func (c *Client) EditThreadMessage(ctx context.Context, channelID int64, msgID int, text string) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.EditThreadMessage(ctx, channelID, msgID, text)
}

func (c *Client) DeleteThreadMessage(ctx context.Context, channelID int64, msgID int) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.DeleteThreadMessage(ctx, channelID, msgID)
}

func (c *Client) StreamThreadHistory(ctx context.Context, channelID int64, afterID int, fn func(telegram.ThreadMessage) error) (telegram.HistoryMeta, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return telegram.HistoryMeta{}, err
	}
	return c.inner.StreamThreadHistory(ctx, channelID, afterID, fn)
}

func (c *Client) StreamSavedHistory(ctx context.Context, afterID int, fn func(telegram.Message) error) (telegram.HistoryMeta, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return telegram.HistoryMeta{}, err
	}
	return c.inner.StreamSavedHistory(ctx, afterID, fn)
}

func (c *Client) GetSavedMessage(ctx context.Context, messageID int) (telegram.Message, error) {
	if err := c.lock.Acquire(ctx); err != nil {
		return telegram.Message{}, err
	}
	return c.inner.GetSavedMessage(ctx, messageID)
}

func (c *Client) DownloadSavedMedia(ctx context.Context, messageID int, dst io.Writer) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.DownloadSavedMedia(ctx, messageID, dst)
}

func (c *Client) DeleteSavedMessage(ctx context.Context, messageID int) error {
	if err := c.lock.Acquire(ctx); err != nil {
		return err
	}
	return c.inner.DeleteSavedMessage(ctx, messageID)
}
