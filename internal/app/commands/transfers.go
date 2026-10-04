package commands

import (
	"github.com/spf13/cobra"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/output"
	"github.com/thedavidweng/tg-drive/internal/service"
	"github.com/thedavidweng/tg-drive/internal/transfer"
)

// The transfers commands read Transfers from the index only, through an
// offline App: they never wait for the Session lock another process holds
// while it runs Transfers. retry is the exception: it re-runs the Transfer,
// so it opens the full App like td cp and td get do.

func NewTransfersCmd(rt Runtime) *cobra.Command {
	c := &cobra.Command{
		Use:   "transfers",
		Short: "List, inspect, watch, cancel, and retry Transfers",
	}
	GroupUsage(rt, c)
	c.AddCommand(newTransfersListCmd(rt), newTransfersShowCmd(rt), newTransfersCancelCmd(rt), newTransfersWatchCmd(rt), newTransfersRetryCmd(rt))
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
				if err := printTransferLine(out, t); err != nil {
					return r.Error(err)
				}
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

// newTransfersRetryCmd re-runs a failed, cancelled, or interrupted Transfer
// from its recorded request, in the foreground like td cp and td get: the
// command waits and reports the outcome. The retrying process becomes the
// Transfer's owner; an interrupted upload resumes from its saved parts.
func newTransfersRetryCmd(rt Runtime) *cobra.Command {
	var events bool
	c := &cobra.Command{
		Use:   "retry <id>",
		Short: "Retry a failed, cancelled, or interrupted Transfer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			var observer transfer.Observer
			if events {
				observer = transferEvents(r)
			}
			manager := transfer.New(app, transfer.Options{FrontEnd: transfer.FrontEndCLI, Observer: observer})
			handle, err := manager.Retry(cmd.Context(), args[0], service.Observer{})
			if err != nil {
				return r.Error(err)
			}
			if _, err := handle.Wait(); err != nil {
				return r.Error(err)
			}
			t, err := manager.Get(cmd.Context(), args[0])
			if err != nil {
				return r.Error(err)
			}
			if events {
				return r.Event("transfers.retry", t)
			}
			if rt.JSON() {
				return r.Success(t)
			}
			return printTransferLine(cmd.OutOrStdout(), *t)
		},
	}
	c.Flags().BoolVar(&events, "events", false, "emit NDJSON progress events during the retry")
	return c
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

// transferPart is one confirmed upload part as a transfer.progress event
// reports it. Uploaded and Total are the bytes of the part's own file, which
// for a multi-file Transfer is one member rather than the whole Transfer.
type transferPart struct {
	FileName string `json:"file_name"`
	Index    int    `json:"index"`
	Size     int    `json:"size"`
	Uploaded int64  `json:"uploaded"`
	Total    int64  `json:"total"`
}

// transferProgress is the transfer.progress payload: the Transfer as
// td transfers show returns it, plus the part behind the report.
type transferProgress struct {
	transfer.Transfer
	Part transferPart `json:"part"`
}

// transferEvents is the --events observer (ADR 0045): a transfer.stage line
// each time a Transfer enters a stage and a transfer.progress line for each
// confirmed upload part.
func transferEvents(r *output.Renderer) transfer.Observer {
	return transfer.Observer{
		OnStage: func(t transfer.Transfer) { _ = r.Event("transfer.stage", t) },
		OnPart: func(t transfer.Transfer, p service.Progress) {
			_ = r.Event("transfer.progress", transferProgress{Transfer: t, Part: transferPart{
				FileName: p.Part.FileName,
				Index:    p.Part.Index,
				Size:     p.Part.Size,
				Uploaded: p.Done,
				Total:    p.Total,
			}})
		},
	}
}
