package service

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// InitChoices is what a front end offers before binding a local root with
// InitRoot: an existing channel to bind, or a new one to create.
type InitChoices struct {
	// Channels are the existing channels the root may bind.
	Channels []telegram.Channel
	// DefaultTitle names a created channel when the user gives no title.
	DefaultTitle string
}

// InitChoices lists the channels localRoot may bind and the default title
// for a channel created for it.
func (a *App) InitChoices(ctx context.Context, localRoot string) (*InitChoices, error) {
	chs, err := a.ListChannels(ctx, false)
	if err != nil {
		return nil, err
	}
	return &InitChoices{Channels: chs, DefaultTitle: DefaultChannelTitle(localRoot)}, nil
}

// DefaultChannelTitle is the name of the local root's directory, so "."
// resolves to the real directory name.
func DefaultChannelTitle(localRoot string) string {
	if abs, err := filepath.Abs(localRoot); err == nil {
		return filepath.Base(abs)
	}
	return filepath.Base(strings.TrimRight(localRoot, "/"))
}
