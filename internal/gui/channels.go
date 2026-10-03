//go:build gui

package gui

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// Channels is the facade service for the drives (Telegram channels) bound in
// the shared index: listing them, binding or creating one, switching the
// active one, and reporting a channel's status.
//
// Switching the active channel changes the selector every later facade call
// pins on its ctx (appState.switchChannel); the App, its Telegram client,
// and the Transfers running on other channels are untouched. The selection
// survives Auth's reopen.
type Channels struct {
	state *appState
	drive *Drive

	mu sync.Mutex
	// emit is the typed-event sink SetChannelsEmitter connects.
	emit Emitter
	// last is the channel list last reported to the frontend (by List or a
	// channels-changed event); nil until the first report. Index sync
	// emits only when the list differs from it.
	last []ChannelInfo
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
	app, ctx := c.state.use(ctx)
	bound, err := app.BoundChannels(ctx)
	if err != nil {
		return nil, toError(err)
	}
	// The active channel is the one the App's selector resolves to. Nothing
	// bound yet is an empty list, not an error; a dangling selector is.
	activeID := ""
	if len(bound) > 0 || c.state.activeChannel() != "" {
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
	c.mu.Lock()
	c.last = out
	c.mu.Unlock()
	return out, nil
}

// syncFromIndex reports a channel list another writer changed — a td
// process binding a channel, or this GUI's own Bind — as a
// channels-changed event. Services.StartSync runs it on every index
// commit. Before the frontend's first List there is nothing to diff
// against, so the first read only records the baseline.
func (c *Channels) syncFromIndex(ctx context.Context) {
	c.mu.Lock()
	prev, known := c.last, c.last != nil
	c.mu.Unlock()
	list, err := c.List(ctx)
	if err != nil || !known || slices.Equal(prev, list) {
		return
	}
	c.mu.Lock()
	emit := c.emit
	c.mu.Unlock()
	if emit != nil {
		emit(EventChannelsChanged, ChannelsChanged{Channels: list})
	}
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
	// Capabilities is what the account may do on the channel.
	Capabilities *ChannelCapabilities `json:"capabilities,omitempty"`
	// LastScanAt is the RFC3339 time the index last changed from a scan,
	// empty when the channel was never scanned; LastFullScanAt is the last
	// completed full scan.
	LastScanAt     string `json:"last_scan_at,omitempty"`
	LastFullScanAt string `json:"last_full_scan_at,omitempty"`
}

// ChannelCapabilities are the account's permissions on a channel, from the
// Telegram capability layer td doctor checks. A drive needs all four.
type ChannelCapabilities struct {
	// CanUpload is posting files to the channel.
	CanUpload bool `json:"can_upload"`
	// CanDelete is deleting the channel's messages (td rm).
	CanDelete bool `json:"can_delete"`
	// CanEditCaptions is editing old messages' captions (td mv, repair).
	CanEditCaptions bool `json:"can_edit_captions"`
	// CanInvite is exporting the channel's invite link (td share).
	CanInvite bool `json:"can_invite"`
}

// Status reports the active channel's status. Without a bound channel it
// fails with ERR_CHANNEL_NOT_FOUND.
func (c *Channels) Status(ctx context.Context) (*ChannelStatus, error) {
	app, ctx := c.state.use(ctx)
	return c.statusFor(app, ctx)
}

func (c *Channels) statusFor(app *service.App, ctx context.Context) (*ChannelStatus, error) {
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
	perms, err := app.ChannelPermissions(ctx)
	if err == nil {
		out.Capabilities = &ChannelCapabilities{
			CanUpload: perms.Upload, CanDelete: perms.Delete,
			CanEditCaptions: perms.EditCaptions, CanInvite: perms.InviteLink,
		}
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
	app, ctx := c.state.use(ctx)
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
	app, ctx := c.state.use(ctx)
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

// Select switches the active channel: every later facade call works on
// that channel, and running Transfers keep theirs. The channel must be
// bound in the index; an unknown ID fails with ERR_CHANNEL_NOT_FOUND and keeps the
// current channel.
func (c *Channels) Select(ctx context.Context, channelID string) (*ChannelStatus, error) {
	channelID = strings.TrimSpace(channelID)
	app, scoped := c.state.use(ctx)
	// Read the target before changing the shared selector. A failed status
	// read must not leave the frontend showing the previous drive while
	// subsequent calls operate on the new one.
	status, err := c.statusFor(app, service.WithChannel(scoped, channelID))
	if err != nil {
		return nil, err
	}
	if err := c.selectChannel(ctx, channelID); err != nil {
		return nil, err
	}
	return status, nil
}

// selectChannel makes channelID the active channel after checking it is
// bound. Transfers already running keep the channel they were submitted on.
func (c *Channels) selectChannel(ctx context.Context, channelID string) error {
	app, ctx := c.state.use(ctx)
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
	app, ctx := c.state.use(ctx)
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
