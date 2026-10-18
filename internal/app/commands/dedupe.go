package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewDedupeCmd reports content-addressed duplicates without deleting or
// rewriting any Telegram messages.
func NewDedupeCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "dedupe [remote-path]",
		Short: "Audit indexed files for duplicate content",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenOfflineApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			path := "/"
			if len(args) == 1 {
				path = args[0]
			}
			result, err := app.FindDuplicates(cmd.Context(), path)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(result)
			}
			out := cmd.OutOrStdout()
			for _, group := range result.Groups {
				_, _ = fmt.Fprintf(out, "%s (%s)\n", group.Hash, humanSize(group.Size))
				for _, file := range group.Files {
					_, _ = fmt.Fprintf(out, "  %s\n", file.Path)
				}
			}
			_, _ = fmt.Fprintf(out, "duplicate groups: %d\nreclaimable: %s\nunhashed files: %d\n",
				len(result.Groups), humanSize(result.ReclaimableBytes), result.UnhashedFiles)
			return nil
		},
	}
}
