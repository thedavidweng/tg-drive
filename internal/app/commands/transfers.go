package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/internal/transfer"
)

// The transfers commands read Transfers from the index only, through an
// offline App: they never wait for the Session lock another process holds
// while it runs Transfers.

func NewTransfersCmd(rt Runtime) *cobra.Command {
	c := &cobra.Command{
		Use:   "transfers",
		Short: "List and inspect uploads recorded as Transfers",
	}
	GroupUsage(rt, c)
	c.AddCommand(newTransfersListCmd(rt), newTransfersShowCmd(rt), newTransfersCancelCmd(rt))
	return c
}

func newTransfersListCmd(rt Runtime) *cobra.Command {
	var active, all bool
	var stage string
	c := &cobra.Command{
		Use:   "list",
		Short: "List Transfers, active ones by default",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if active && all {
				return r.Error(apperr.New(apperr.ErrFlagConflict, "--active and --all are mutually exclusive"))
			}
			f := transfer.Filter{All: all}
			if stage != "" {
				s, err := transfer.ParseStage(stage)
				if err != nil {
					return r.Error(err)
				}
				f.Stage = s
			}
			app, cleanup, err := rt.OpenOfflineApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			list, err := transfer.New(app, transfer.Options{}).List(cmd.Context(), f)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				if list == nil {
					list = []transfer.Transfer{}
				}
				return r.Success(map[string]any{"transfers": list})
			}
			out := cmd.OutOrStdout()
			for _, t := range list {
				_, _ = fmt.Fprintf(out, "%s  %s  %s  %s/%s  %s -> %s\n", t.ID, t.Stage, t.Kind,
					humanSize(t.BytesDone), humanSize(t.BytesTotal), t.Source, t.Dest)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&active, "active", false, "list only Transfers that have not ended (the default)")
	c.Flags().BoolVar(&all, "all", false, "list every Transfer, ended ones included")
	c.Flags().StringVar(&stage, "stage", "", "list only Transfers in this stage")
	return c
}

// newTransfersCancelCmd requests a Transfer's cancellation through the
// index; the owning process notices on its lease heartbeat and ends the
// Transfer cancelled.
func newTransfersCancelCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id>",
		Short: "Request cancellation of a running Transfer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenOfflineApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			t, err := transfer.New(app, transfer.Options{}).Cancel(cmd.Context(), args[0])
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(t)
			}
			return r.SuccessLine("cancel requested for transfer %s", t.ID)
		},
	}
}

func newTransfersShowCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one Transfer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenOfflineApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			t, err := transfer.New(app, transfer.Options{}).Get(cmd.Context(), args[0])
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(t)
			}
			PrintKV(cmd.OutOrStdout(), t)
			return nil
		},
	}
}
