package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// Channel selection, listing, and bound-status commands.

func NewChannelsCmd(rt Runtime) *cobra.Command {
	var onlyDrive bool
	c := &cobra.Command{Use: "channels", Short: "List Telegram channels"}
	GroupUsage(rt, c)
	list := &cobra.Command{
		Use:   "list",
		Short: "List channels",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			chs, err := app.ListChannels(context.Background(), onlyDrive)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]any{"channels": channelsToMap(chs)})
			}
			out := cmd.OutOrStdout()
			if len(chs) == 0 {
				_, _ = fmt.Fprintln(out, "no channels")
				return nil
			}
			for i, ch := range chs {
				_, _ = fmt.Fprintf(out, "%3d. %s (id %d)\n", i+1, ch.Title, ch.ID)
			}
			return nil
		},
	}
	list.Flags().BoolVar(&onlyDrive, "only-drive", false, "show only [TD] channels")
	c.AddCommand(list)
	link := &cobra.Command{
		Use:   "link-discussion",
		Short: "Create and link a discussion group for the bound channel (carries machine records, ADR 0018)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			res, err := app.LinkDiscussionGroup(context.Background())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(res)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "linked discussion group %q (id %d)\n", res["discussion_title"], res["discussion_channel_id"])
			return nil
		},
	}
	c.AddCommand(link)
	return c
}

func NewStatusCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show index status",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.Status(context.Background())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			printStatus(cmd.OutOrStdout(), data)
			return nil
		},
	}
}

func printStatus(w io.Writer, data map[string]any) {
	row := func(label, format string, args ...any) {
		_, _ = fmt.Fprintf(w, "%-14s "+format+"\n", append([]any{label}, args...)...)
	}
	switch data["authenticated"] {
	case true:
		row("account", "logged in as %v (user %v)", data["display_name"], data["user_id"])
	case false:
		row("account", "not logged in; run: td auth login")
	default:
		row("account", "unknown (Telegram unreachable)")
	}
	if data["initialized"] != true {
		row("channel", "none bound; run: td init <local-root> --create-channel (new drive)")
		row("", "or: td init <local-root> --bind-channel (existing drive)")
		row("database", "%s", config.DisplayPath(fmt.Sprint(data["db_path"])))
		return
	}
	if ch, ok := data["channel"].(service.BoundChannel); ok {
		row("channel", "%s (id %s)", ch.Title, ch.ChannelID)
		row("local root", "%s", config.DisplayPath(ch.LocalRoot))
	}
	if chans, ok := data["channels"].([]service.BoundChannel); ok && len(chans) > 1 {
		row("other drives", "%d more; select with --channel <title-or-id>", len(chans)-1)
	}
	counts, _ := data["files"].(map[string]int)
	files := fmt.Sprintf("%d active", counts["active"])
	for _, st := range []string{"deleted", "missing", "pending", "orphaned"} {
		if counts[st] > 0 {
			files += fmt.Sprintf(", %d %s", counts[st], st)
		}
	}
	row("files", "%s", files)
	if last, _ := data["last_scan_at"].(string); last != "" {
		row("last scan", "%s (last full: %v)", last, orNever(data["last_full_scan_at"]))
	} else {
		row("last scan", "never; run: td scan --full")
	}
	if limit, ok := data["upload_limit_bytes"].(int64); ok && limit > 0 {
		row("upload limit", "%s per file", humanSize(limit))
	}
	row("database", "%s", config.DisplayPath(fmt.Sprint(data["db_path"])))

	var problems []string
	for _, p := range []struct{ key, label, fix string }{
		{"scan_errors_pending", "scan errors", "td repair --scan-errors"},
		{"orphaned", "orphaned uploads", "td repair --orphaned"},
		{"stale_pending", "stale pending uploads", "td repair --pending"},
		{"stale_locks", "stale locks", "td repair --pending"},
	} {
		if n, _ := data[p.key].(int); n > 0 {
			problems = append(problems, fmt.Sprintf("%d %s (%s)", n, p.label, p.fix))
		}
	}
	if len(problems) == 0 {
		row("health", "ok")
	} else {
		row("health", "%s", strings.Join(problems, "; "))
	}
}

func orNever(v any) any {
	if s, _ := v.(string); s == "" {
		return "never"
	}
	return v
}
