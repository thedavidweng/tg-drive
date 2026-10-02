//go:build gui

// Package frontend embeds the built GUI assets. It lives here because
// embed patterns cannot reach parent directories; build the frontend
// (dist/) before compiling with the gui tag.
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets returns the built frontend rooted at its index.html.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
