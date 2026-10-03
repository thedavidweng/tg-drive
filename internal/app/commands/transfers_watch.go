package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive/internal/transfer"
)

// Like list and show, watch reads the index only, through an offline App:
// it follows the Transfers of every process and never waits for the
// Session lock a running Transfer holds. It runs until Ctrl-C, which ends
// it with ERR_CANCELLED (exit 130) like any other command.
func newTransfersWatchCmd(rt Runtime) *cobra.Command {
	var events bool
	c := &cobra.Command{
		Use:   "watch",
		Short: "Follow Transfers live until Ctrl-C",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenOfflineApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			m := transfer.New(app, transfer.Options{})
			var runErr error
			switch {
			case events || rt.JSON():
				runErr = followStages(cmd.Context(), m, func(t transfer.Transfer) error {
					return r.Event("transfer.stage", t)
				})
			case stdoutIsTerminal():
				runErr = watchTable(cmd.Context(), m, cmd.OutOrStdout())
			default:
				out := cmd.OutOrStdout()
				runErr = followStages(cmd.Context(), m, func(t transfer.Transfer) error {
					return printTransferLine(out, t)
				})
			}
			if runErr != nil {
				return r.Error(runErr)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&events, "events", false, "stream NDJSON transfer.stage events")
	return c
}

// followStages runs emit for each stage a Transfer is observed entering,
// until ctx ends. A Transfer already ended when the watch starts is
// history and emits nothing; any other emits its current stage when first
// seen, then each stage it enters after.
func followStages(ctx context.Context, m *transfer.Manager, emit func(transfer.Transfer) error) error {
	seen := map[string]transfer.Stage{}
	first := true
	return m.Watch(ctx, func(list []transfer.Transfer) error {
		for _, t := range list {
			last, known := seen[t.ID]
			if known && t.Stage == last {
				continue
			}
			seen[t.ID] = t.Stage
			if first && !known && t.Stage.Terminal() {
				continue
			}
			if err := emit(t); err != nil {
				return err
			}
		}
		first = false
		return nil
	})
}

// watchTable redraws the live Transfers table on a terminal each time a
// Transfer changes: the active ones and the ones that ended since the
// watch started. Redraws follow index writes, which the Transfer Manager
// throttles, so the terminal is not flooded.
func watchTable(ctx context.Context, m *transfer.Manager, w io.Writer) error {
	started := time.Now()
	return m.Watch(ctx, func(list []transfer.Transfer) error {
		var b strings.Builder
		b.WriteString("\x1b[H\x1b[2JTransfers (live; Ctrl-C to stop)\n\n")
		rows := 0
		for _, t := range list {
			if t.Stage.Terminal() && (t.FinishedAt == nil || t.FinishedAt.Before(started)) {
				continue
			}
			rows++
			if err := printTransferLine(&b, t); err != nil {
				return err
			}
		}
		if rows == 0 {
			b.WriteString("no active transfers\n")
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// stdoutIsTerminal reports whether stdout is a terminal: the watch table
// redraws in place on one and degrades to appended lines on a pipe.
func stdoutIsTerminal() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// printTransferLine writes one Transfer in the transfers list format.
func printTransferLine(w io.Writer, t transfer.Transfer) error {
	_, err := fmt.Fprintf(w, "%s  %s  %s  %s/%s  %s -> %s\n", t.ID, t.Stage, t.Kind,
		humanSize(t.BytesDone), humanSize(t.BytesTotal), t.Source, t.Dest)
	return err
}
