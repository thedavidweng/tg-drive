package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// NewDedupeCmd audits content-addressed duplicates and optionally removes
// duplicate members after an explicit confirmation.
func NewDedupeCmd(rt Runtime) *cobra.Command {
	var deleteDuplicates, confirm bool
	c := &cobra.Command{
		Use:   "dedupe [remote-path]",
		Short: "Audit or delete indexed duplicate content",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			path := "/"
			if len(args) == 1 {
				path = args[0]
			}
			if deleteDuplicates {
				opts := service.DedupeDeleteOptions{Confirm: confirm}
				if err := opts.Validate(); err != nil {
					return r.Error(err)
				}
				app, cleanup, err := rt.OpenApp(cmd)
				if err != nil {
					return r.Error(err)
				}
				defer cleanup()
				result, err := app.DeleteDuplicates(cmd.Context(), path, opts)
				if err != nil {
					return r.Error(err)
				}
				if rt.JSON() {
					return r.Success(result)
				}
				return r.SuccessLine("deleted %d duplicate files across %d groups (retained %d; estimated reclaimable %s)",
					result.Deleted, result.Groups, result.Retained, humanSize(result.ReclaimableBytes))
			}
			app, cleanup, err := rt.OpenOfflineApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
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
	c.Flags().BoolVar(&deleteDuplicates, "delete", false, "delete duplicate members, retaining the first path in each group")
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm deleting duplicate members")
	return c
}
