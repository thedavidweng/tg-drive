package commands

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive/internal/service"
)

func NewWatchCmd(rt Runtime) *cobra.Command {
	opts := service.WatchOptions{Interval: 2 * time.Second}
	c := &cobra.Command{
		Use:   "watch <local-directory> <remote-directory>",
		Short: "Watch local file content and checkpoint uploads to one drive",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if err := opts.Validate(); err != nil {
				return r.Error(err)
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			err = app.WatchDirectory(cmd.Context(), args[0], args[1], opts, func(result service.WatchResult) error {
				if rt.JSON() {
					return r.Success(result)
				}
				return r.SuccessLine("watch pass: uploaded %d, unchanged %d, deleted %d, retained %d", result.Uploaded, result.Unchanged, result.Deleted, result.Retained)
			})
			if err != nil {
				return r.Error(err)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&opts.Confirm, "confirm", false, "authorize uploads and replacement of checkpointed content")
	c.Flags().BoolVar(&opts.Delete, "delete", false, "also remove checkpointed remote files absent locally")
	c.Flags().BoolVar(&opts.Once, "once", false, "complete one pass and exit")
	c.Flags().DurationVar(&opts.Interval, "interval", 2*time.Second, "delay between completed content scans")
	return c
}
