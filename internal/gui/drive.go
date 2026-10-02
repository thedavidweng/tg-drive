//go:build gui

package gui

import (
	"context"

	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// Drive browses the bound channel's index.
type Drive struct {
	app *service.App
}

// Entry is one row of a directory listing.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Type is "dir" or "file".
	Type string `json:"type"`
	// Size is in bytes; 0 for directories.
	Size int64 `json:"size"`
}

// List lists the children of a remote directory from the local index,
// directories first. It never contacts Telegram.
func (d *Drive) List(ctx context.Context, path string) ([]Entry, error) {
	rows, err := d.app.ListDir(ctx, path)
	if err != nil {
		return nil, toError(err)
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{Name: r.Name, Path: r.Path, Type: r.Type}
		// A directory implied only by a file below it carries that file's
		// size in the service listing.
		if r.Type == "file" {
			e.Size = r.Size
		}
		out = append(out, e)
	}
	return out, nil
}
