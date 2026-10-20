//go:build linux || darwin

package commands

import (
	"context"
	"errors"
	"io/fs"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive/adapters/native/readfuse"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func NewMountCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{Use: "mount <remote-directory> <local-mountpoint>", Short: "Mount a pinned read-only drive subtree with native FUSE", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		r := rt.Renderer()
		app, cleanup, err := rt.OpenApp(cmd)
		if err != nil {
			return r.Error(err)
		}
		defer cleanup()
		tree, err := app.OpenReadTree(cmd.Context(), args[0])
		if err != nil {
			return r.Error(err)
		}
		err = readfuse.Serve(cmd.Context(), args[1], func(ctx context.Context) fs.FS { return tree.WithContext(ctx) })
		if errors.Is(err, context.Canceled) {
			return r.Error(apperr.New(apperr.ErrCancelled, "FUSE mount stopped"))
		}
		if err != nil {
			return r.Error(err)
		}
		return r.Success(map[string]any{"unmounted": true, "read_only": true})
	}}
}
