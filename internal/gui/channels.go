//go:build gui

package gui

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// Channels is the facade service for the drives (Telegram channels) bound in
// the shared index: listing them, binding or creating one, switching the
// active one, and reporting a channel's status.
//
// Each bound channel has its own App over the one database and Telegram
// client, and switching only selects which App the views read
// (appState.switchChannel); Transfers already running keep their channel.
// The selection survives Auth's reopen.
type Channels struct {
	state *appState
	drive *Drive

	mu   sync.Mutex
	emit Emitter
	// last is the list last announced on EventChannelsChanged.
	last []ChannelInfo
}

// syncFromIndex announces the bound channels when they differ from the
// last announcement: a td init in a terminal, or this GUI's own bind or
// switch.
func (c *Channels) syncFromIndex(ctx context.Context) {
	list, err := c.List(ctx)
	if err != nil {
		return
	}
	c.mu.Lock()
	if slices.Equal(list, c.last) {
		c.mu.Unlock()
		return
	}
	c.last = list
	emit := c.emit
	c.mu.Unlock()
	if emit != nil {
		emit(EventChannelsChanged, ChannelsChanged{Channels: list})
	}
}

// ChannelInfo is one channel bound in the shared index (td init or a GUI
// bind), as the header switcher lists it.
type ChannelInfo struct {
	// ChannelID is the channel's Telegram ID, and the selector Select takes.
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	// LocalRoot is the local root the channel is bound to. GUI bindings use
	// a synthetic root per channel under the data directory; it is a label,
	// never created on disk.
	LocalRoot string `json:"local_root"`
	// Active marks the channel every facade call currently works on.
	Active bool `json:"active"`
}

// List returns the channels bound in the shared index, marking the active
// one. It reads only the index, never Telegram, so it also works offline.
func (c *Channels) List(ctx context.Context) ([]ChannelInfo, error) {
	app := c.state.current()
	bound, err := app.BoundChannels(ctx)
	if err != nil {
		return nil, toError(err)
	}
	// The active channel is the one the App's selector resolves to. Nothing
	// bound yet is an empty list, not an error; a dangling selector is.
	activeID := ""
	if len(bound) > 0 || app.Channel != "" {
		id, err := app.ChannelTelegramID(ctx)
		if err != nil {
			return nil, toError(err)
		}
		activeID = id
	}
	out := make([]ChannelInfo, 0, len(bound))
	for _, ch := range bound {
		out = append(out, ChannelInfo{
			ChannelID: ch.ChannelID,
			Title:     ch.Title,
			LocalRoot: ch.LocalRoot,
			Active:    ch.ChannelID == activeID,
		})
	}
	return out, nil
}

// ChannelStatus is the active channel's health: its linked discussion group,
// its capabilities, and the index's freshness (ADR 0018, `td status`).
type ChannelStatus struct {
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	LocalRoot string `json:"local_root"`
	// Files is how many active files the index holds for the channel.
	Files int `json:"files"`
	// DiscussionLinked reports whether a discussion group carries the
	// channel's machine records; DiscussionTitle names it when linked.
	DiscussionLinked bool   `json:"discussion_linked"`
	DiscussionTitle  string `json:"discussion_title,omitempty"`
	// UploadLimitBytes is the account's per-file upload limit on Telegram.
	UploadLimitBytes int64 `json:"upload_limit_bytes"`
	// LastScanAt is the RFC3339 time the index last changed from a scan,
	// empty when the channel was never scanned; LastFullScanAt is the last
	// completed full scan.
	LastScanAt     string `json:"last_scan_at,omitempty"`
	LastFullScanAt string `json:"last_full_scan_at,omitempty"`
}

// Status reports the active channel's status. Without a bound channel it
// fails with ERR_CHANNEL_NOT_FOUND.
func (c *Channels) Status(ctx context.Context) (*ChannelStatus, error) {
	app := c.state.current()
	if app.TG == nil {
		return nil, toError(apperr.New(apperr.ErrAuthRequired, "not logged in"))
	}
	st, err := app.Status(ctx)
	if err != nil {
		return nil, toError(err)
	}
	if !st.Initialized || st.Channel == nil {
		return nil, toError(apperr.New(apperr.ErrChannelNotFound, "no channel bound"))
	}
	out := &ChannelStatus{
		ChannelID: st.Channel.ChannelID,
		Title:     st.Channel.Title,
		LocalRoot: st.Channel.LocalRoot,
		Files:     st.Files["active"],
	}
	if st.UploadLimitBytes != nil {
		out.UploadLimitBytes = *st.UploadLimitBytes
	}
	if st.LastScanAt != nil {
		out.LastScanAt = *st.LastScanAt
	}
	if st.LastFullScanAt != nil {
		out.LastFullScanAt = *st.LastFullScanAt
	}
	discussion, err := app.DiscussionGroup(ctx)
	if err != nil {
		return nil, toError(err)
	}
	if discussion != nil {
		out.DiscussionLinked = true
		out.DiscussionTitle = discussion.DiscussionTitle
	}
	return out, nil
}

// ChannelChoice is one Telegram channel the bind dialog offers, marked when
// it is already bound as a drive.
type ChannelChoice struct {
	// ChannelID is the channel's Telegram ID.
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	Bound     bool   `json:"bound"`
}

// BindChoices is what the bind dialog offers: the user's Telegram channels,
// and the default title for a created one.
type BindChoices struct {
	Channels     []ChannelChoice `json:"channels"`
	DefaultTitle string          `json:"default_title"`
}

// Choices lists the user's Telegram channels with the ones already bound as
// drives marked, plus the default title for a created channel.
func (c *Channels) Choices(ctx context.Context) (*BindChoices, error) {
	app := c.state.current()
	if app.TG == nil {
		return nil, toError(apperr.New(apperr.ErrAuthRequired, "not logged in"))
	}
	choices, err := app.InitChoices(ctx, guiLocalRoot(app, ""))
	if err != nil {
		return nil, toError(err)
	}
	bound, err := app.BoundChannels(ctx)
	if err != nil {
		return nil, toError(err)
	}
	boundIDs := make(map[string]bool, len(bound))
	for _, ch := range bound {
		boundIDs[ch.ChannelID] = true
	}
	out := &BindChoices{Channels: make([]ChannelChoice, 0, len(choices.Channels)), DefaultTitle: choices.DefaultTitle}
	for _, ch := range choices.Channels {
		id := strconv.FormatInt(ch.ID, 10)
		out.Channels = append(out.Channels, ChannelChoice{ChannelID: id, Title: ch.Title, Bound: boundIDs[id]})
	}
	return out, nil
}

// BindRequest is one bind-or-create decision from the bind dialog. ChannelID
// binds that existing Telegram channel; with ChannelID empty a new drive
// channel is created, titled Title, or the default title when Title is
// empty.
type BindRequest struct {
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
}

// BindResult reports a finished bind or creation. The bound channel becomes
// the active one.
type BindResult struct {
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	// Created is true when a new Telegram channel was created; false when
	// an existing one was bound.
	Created bool `json:"created"`
	// AlreadyInitialized is true when this GUI already bound the channel's
	// local root; nothing was created or changed.
	AlreadyInitialized bool `json:"already_initialized"`
	// IndexedFiles is how many existing files binding the channel indexed;
	// absent when the initial scan did not run or failed.
	IndexedFiles *int `json:"indexed_files,omitempty"`
	// ScanError carries the initial scan's failure; the binding is usable
	// and a rescan retries it.
	ScanError string `json:"scan_error,omitempty"`
}

// Bind binds an existing Telegram channel as a drive, or creates a new drive
// channel, through the service's init use case, and makes it the active
// channel. GUI bindings use a synthetic per-channel local root under the
// data directory.
func (c *Channels) Bind(ctx context.Context, req BindRequest) (*BindResult, error) {
	app := c.state.current()
	if app.TG == nil {
		return nil, toError(apperr.New(apperr.ErrAuthRequired, "not logged in"))
	}
	req.ChannelID = strings.TrimSpace(req.ChannelID)
	req.Title = strings.TrimSpace(req.Title)
	created := req.ChannelID == ""
	root := guiLocalRoot(app, req.Title)
	res, err := func() (*service.InitRootResult, error) {
		if created {
			title := req.Title
			if title == "" {
				title = service.DefaultChannelTitle(root)
				root = guiLocalRoot(app, title)
			}
			return app.InitRoot(ctx, root, "", title, "")
		}
		// Resolve once for the root label; InitRoot resolves the same ID
		// again to bind, so a duplicate title can never mis-bind.
		ch, err := app.TG.ResolveChannel(ctx, req.ChannelID)
		if err != nil {
			return nil, err
		}
		return app.InitRoot(ctx, guiLocalRoot(app, ch.Title), "", "", req.ChannelID)
	}()
	if err != nil {
		return nil, toError(err)
	}
	// The bound channel becomes the active one — also when the create turned
	// out to be an existing GUI binding: activating it is the least
	// surprising outcome of asking for it again.
	if err := c.selectChannel(ctx, strconv.FormatInt(res.ChannelID, 10)); err != nil {
		return nil, err
	}
	return &BindResult{
		ChannelID:          strconv.FormatInt(res.ChannelID, 10),
		Title:              res.ChannelTitle,
		Created:            created,
		AlreadyInitialized: res.AlreadyInitialized,
		IndexedFiles:       res.IndexedFiles,
		ScanError:          res.ScanError,
	}, nil
}

// Select switches the active channel: the App reopens bound to it, and every
// facade call then works on that channel. The channel must be bound in the
// index; an unknown ID fails with ERR_CHANNEL_NOT_FOUND and keeps the
// current channel.
func (c *Channels) Select(ctx context.Context, channelID string) (*ChannelStatus, error) {
	if err := c.selectChannel(ctx, strings.TrimSpace(channelID)); err != nil {
		return nil, err
	}
	return c.Status(ctx)
}

// selectChannel makes the App of channelID the active one after checking
// the channel is bound.
func (c *Channels) selectChannel(ctx context.Context, channelID string) error {
	app := c.state.current()
	bound, err := app.BoundChannels(ctx)
	if err != nil {
		return toError(err)
	}
	known := false
	for _, ch := range bound {
		if ch.ChannelID == channelID {
			known = true
			break
		}
	}
	if !known {
		return toError(apperr.New(apperr.ErrChannelNotFound, "channel not bound: "+channelID))
	}
	// Forget the shown directory before the switch: it belongs to the old
	// channel's tree, and index sync must not re-read it against the new
	// one.
	c.drive.resetView()
	c.state.switchChannel(channelID)
	// A switch writes nothing to the index, so index sync would not
	// announce the new active channel.
	c.syncFromIndex(ctx)
	return nil
}

// DiscussionLink identifies the discussion group a LinkDiscussion call
// linked to the active channel.
type DiscussionLink struct {
	DiscussionChannelID string `json:"discussion_channel_id"`
	DiscussionTitle     string `json:"discussion_title"`
}

// LinkDiscussion creates and links a discussion group for the active
// channel when it has none (the service keeps an existing link), so the
// channel can carry machine records (ADR 0018).
func (c *Channels) LinkDiscussion(ctx context.Context) (*DiscussionLink, error) {
	app := c.state.current()
	if app.TG == nil {
		return nil, toError(apperr.New(apperr.ErrAuthRequired, "not logged in"))
	}
	res, err := app.LinkDiscussionGroup(ctx)
	if err != nil {
		return nil, toError(err)
	}
	return &DiscussionLink{
		DiscussionChannelID: strconv.FormatInt(res.DiscussionChannelID, 10),
		DiscussionTitle:     res.DiscussionTitle,
	}, nil
}

// guiLocalRoot is the synthetic local root a GUI binding is recorded with:
// one path per channel under the data directory, used for display and for
// the init use case's same-root re-bind detection. The directory is never
// created; the GUI mirrors nothing locally. An empty name yields the root
// whose basename is the default channel title.
func guiLocalRoot(app *service.App, name string) string {
	if name == "" {
		name = "Drive"
	}
	// The root is a label, but keep it one path segment on every OS.
	name = strings.NewReplacer("/", "-", "\\", "-").Replace(name)
	return filepath.Join(filepath.Dir(app.Cfg.Storage.DBPath), "drives", name)
}
