//go:build gui

package gui

import (
	"context"
	"sync"
	"time"

	"github.com/thedavidweng/tg-drive/internal/service"
)

// Drive browses the bound channel's index.
type Drive struct {
	state *appState
	emit  Emitter
	media *media

	// current is the directory the frontend is showing (its last successful
	// List), which index sync re-reads when another process changes the
	// index.
	mu      sync.Mutex
	current string
}

// Entry is one row of a directory listing.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Type is "dir" or "file".
	Type string `json:"type"`
	// Size is in bytes; 0 for directories.
	Size int64 `json:"size"`
	// Date is the RFC3339 time the file or directory last changed.
	Date string `json:"date"`
}

// List lists the children of a remote directory from the local index,
// directories first. It never contacts Telegram. The listed directory
// becomes the one index sync re-reads and emits on EventDirectoryChanged.
func (d *Drive) List(ctx context.Context, path string) ([]Entry, error) {
	out, err := d.list(ctx, path)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.current = path
	d.mu.Unlock()
	return out, nil
}

func (d *Drive) list(ctx context.Context, path string) ([]Entry, error) {
	rows, err := d.state.current().ListDir(d.state.scoped(ctx), path)
	if err != nil {
		return nil, toError(err)
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{Name: r.Name, Path: r.Path, Type: r.Type, Date: r.UpdatedAt}
		// A directory implied only by a file below it carries that file's
		// size in the service listing.
		if r.Type == "file" {
			e.Size = r.Size
		}
		out = append(out, e)
	}
	return out, nil
}

func (d *Drive) emitEvent(name string, data any) {
	if d.emit != nil {
		d.emit(name, data)
	}
}

// resetView forgets the directory the frontend is showing. Channels calls it
// when switching channels: the path belongs to the old channel's tree, and
// index sync must not re-read it against the new one.
func (d *Drive) resetView() {
	d.mu.Lock()
	d.current = ""
	d.mu.Unlock()
}

// Mkdir creates an empty directory in the virtual tree. Telegram cannot
// store empty directories, so it is local-only until a file lands in it.
func (d *Drive) Mkdir(ctx context.Context, path string) error {
	return toError(d.state.current().Mkdir(d.state.scoped(ctx), path))
}

// MoveOptions carries the move's typed confirmation (ADR 0003). An
// unconfirmed move is rejected with ERR_CONFIRMATION_REQUIRED.
type MoveOptions struct {
	Confirm bool `json:"confirm"`
}

// Move renames a remote file or moves it into another directory of the
// bound channel. Directories cannot move (file-level moves only).
func (d *Drive) Move(ctx context.Context, from, to string, opts MoveOptions) error {
	return toError(d.state.current().MoveFile(d.state.scoped(ctx), from, to, service.MoveOptions{Confirm: opts.Confirm}))
}

// DeleteOptions carries the delete's typed confirmation (ADR 0003). An
// unconfirmed delete is rejected with ERR_CONFIRMATION_REQUIRED.
type DeleteOptions struct {
	Confirm bool `json:"confirm"`
}

// DeleteOutcome reports how a file was removed.
type DeleteOutcome struct {
	// Mode is the configured delete mode that ran (e.g. "delete" or
	// "tombstone").
	Mode string `json:"mode"`
	Path string `json:"path"`
}

// Delete removes a remote file from Telegram and the index. Directories
// cannot be deleted (file-level deletes only).
func (d *Drive) Delete(ctx context.Context, path string, opts DeleteOptions) (*DeleteOutcome, error) {
	res, err := d.state.current().DeleteFile(d.state.scoped(ctx), path, service.DeleteOptions{Confirm: opts.Confirm})
	if err != nil {
		return nil, toError(err)
	}
	return &DeleteOutcome{Mode: res.Mode, Path: res.Path}, nil
}

// ShareLink is what a person needs to open a path on Telegram.
type ShareLink struct {
	// URL is the bound channel's invite link.
	URL string `json:"url"`
	// Hashtag locates the file inside the channel; empty when unknown.
	Hashtag string `json:"hashtag,omitempty"`
	Path    string `json:"path"`
	Channel string `json:"channel"`
}

// Share returns the invite link and legacy hashtag for a remote path.
func (d *Drive) Share(ctx context.Context, path string) (*ShareLink, error) {
	res, err := d.state.current().Share(d.state.scoped(ctx), path)
	if err != nil {
		return nil, toError(err)
	}
	return &ShareLink{URL: res.InviteLink, Hashtag: res.Hashtag, Path: res.Path, Channel: res.Channel}, nil
}

// TreeNode is one node of a directory tree.
type TreeNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Type     string     `json:"type"`
	Children []TreeNode `json:"children,omitempty"`
}

// Tree returns the directory tree below a remote path, up to maxDepth
// levels (0 for the service default).
func (d *Drive) Tree(ctx context.Context, path string, maxDepth int) ([]TreeNode, error) {
	nodes, err := d.state.current().Tree(d.state.scoped(ctx), path, maxDepth)
	if err != nil {
		return nil, toError(err)
	}
	return treeNodes(nodes), nil
}

// ScanOutcome reports a completed rescan.
type ScanOutcome struct {
	Mode    string `json:"mode"`
	Active  int    `json:"active"`
	Deleted int    `json:"deleted"`
	Invalid int    `json:"invalid"`
	Missing int    `json:"missing"`
}

// Scan rescans the bound channel from Telegram and rebuilds the local
// index. While it runs it emits ScanProgress events; the final event
// carries the completed counts.
func (d *Drive) Scan(ctx context.Context) (*ScanOutcome, error) {
	prog := &scanProgress{emit: d.emitEvent}
	res, err := d.state.current().Scan(d.state.scoped(ctx), service.ScanOptions{Full: true, Observer: prog.observer()})
	if err != nil {
		return nil, toError(err)
	}
	prog.flush()
	return &ScanOutcome{
		Mode:    res.Mode,
		Active:  res.Active,
		Deleted: res.Deleted,
		Invalid: res.Invalid,
		Missing: res.Missing,
	}, nil
}

// scanProgressThrottle bounds how often per-item scan progress reaches the
// frontend; a channel can hold thousands of messages.
const scanProgressThrottle = 100 * time.Millisecond

// scanProgress turns the scan's observer callbacks into ScanProgress
// events. Observer callbacks may run on several goroutines at once.
type scanProgress struct {
	emit func(name string, data any)

	mu      sync.Mutex
	stage   string
	indexed int
	failed  int
	last    time.Time
}

func (p *scanProgress) observer() service.Observer {
	return service.Observer{
		OnStage: func(it service.Item, st service.Stage) {
			if it != (service.Item{}) {
				return
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			p.stage = string(st)
			p.emitLocked(true)
		},
		OnItem: func(r service.ItemResult) {
			p.mu.Lock()
			defer p.mu.Unlock()
			switch r.Status {
			case service.ItemCompleted:
				p.indexed++
			case service.ItemFailed:
				p.failed++
			case service.ItemSkipped:
			}
			p.emitLocked(false)
		},
	}
}

// flush emits the final counts even inside the throttle window.
func (p *scanProgress) flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emitLocked(true)
}

func (p *scanProgress) emitLocked(force bool) {
	if !force && time.Since(p.last) < scanProgressThrottle {
		return
	}
	p.last = time.Now()
	p.emit(EventScanProgress, ScanProgress{Stage: p.stage, Indexed: p.indexed, Failed: p.failed})
}

func treeNodes(nodes []service.TreeNode) []TreeNode {
	out := make([]TreeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, TreeNode{
			Name:     n.Name,
			Path:     n.Path,
			Type:     n.Type,
			Children: treeNodes(n.Children),
		})
	}
	return out
}
