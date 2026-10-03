package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/config"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// Doctor, doctor path-codec, and config commands.

func NewDoctorCmd(rt Runtime) *cobra.Command {
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Check local and Telegram capabilities",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.Doctor(cmd.Context())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			checks, hints := data.Checks, data.Hints
			names := make([]string, 0, len(checks))
			for k := range checks {
				names = append(names, k)
			}
			sort.Strings(names)
			tally := map[string]int{}
			for _, name := range names {
				tally[checks[name]]++
				line := fmt.Sprintf("%-18s %s", name, checks[name])
				if hint := hints[name]; hint != "" {
					line += " — " + hint
				}
				_, _ = fmt.Fprintln(out, line)
			}
			if data.MaxUploadBytes != nil {
				_, _ = fmt.Fprintf(out, "%-18s %s per file\n", "max_upload", humanSize(*data.MaxUploadBytes))
			}
			summary := fmt.Sprintf("\n%d passed, %s, %d failed", tally["pass"], plural(tally["warn"], "warning"), tally["fail"])
			if n := tally["unknown"]; n > 0 {
				summary += fmt.Sprintf(", %d not checked", n)
			}
			_, _ = fmt.Fprintln(out, summary)
			return nil
		},
	}
	c.AddCommand(NewDoctorPathCodecCmd(rt))
	return c
}

func NewDoctorPathCodecCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "path-codec",
		Short: "Run path codec self-test and verify stored slug mappings",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenOfflineApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.PathCodecDoctor(cmd.Context())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "fixed-vectors        %s\n", data.FixedVectors)
			_, _ = fmt.Fprintf(out, "db-check             %s\n", data.DBCheck)
			_, _ = fmt.Fprintf(out, "db-rows              %v\n", data.DBRows)
			_, _ = fmt.Fprintf(out, "corrupt-rows         %v\n", data.CorruptRows)
			return nil
		},
	}
}

func NewConfigCmd(rt Runtime) *cobra.Command {
	var showSecrets, confirm bool
	c := &cobra.Command{Use: "config", Short: "Manage configuration"}
	GroupUsage(rt, c)
	get := &cobra.Command{
		Use:   "get [key]",
		Short: "Get config value(s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			get := service.ConfigGetOptions{ShowSecrets: showSecrets, Confirmed: confirm}
			if len(args) > 0 {
				get.Key = args[0]
			}
			if showSecrets && !confirm && !rt.JSON() && stdinIsInteractive() {
				fmt.Fprint(os.Stderr, "show secrets? [y/N] ")
				ans, err := readLine(cmd.Context())
				if apperr.IsCancelled(err) {
					return r.Error(err)
				}
				get.Confirmed = strings.ToLower(strings.TrimSpace(ans)) == "y"
				get.ShowSecrets = get.Confirmed
			}
			view, err := service.GetConfig(rt.Options(), get)
			if err != nil {
				return r.Error(err)
			}
			if len(args) == 0 {
				if rt.JSON() {
					all := make(map[string]any, len(view.Entries))
					for _, e := range view.Entries {
						all[e.Key] = e.Value
					}
					return r.Success(all)
				}
				out := cmd.OutOrStdout()
				for _, e := range view.Entries {
					_, _ = fmt.Fprintf(out, "%s: %v\n", e.Key, humanConfigValue(e.Key, e.Value))
				}
				return nil
			}
			e := view.Entries[0]
			if rt.JSON() {
				return r.Success(map[string]any{e.Key: e.Value, "config_path": view.ConfigPath})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%v\n", humanConfigValue(e.Key, e.Value))
			return nil
		},
	}
	get.Flags().BoolVar(&showSecrets, "show-secrets", false, "show secret values")
	get.Flags().BoolVar(&confirm, "confirm", false, "confirm showing secrets without a prompt")
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set config value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			data, err := service.SetConfig(rt.Options(), args[0], args[1])
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "set %s\n", data.Key)
			return nil
		},
	}
	c.AddCommand(get, set)
	return c
}

func humanConfigValue(key string, v any) any {
	if p, ok := v.(string); ok && strings.HasPrefix(key, "storage.") {
		return config.DisplayPath(p)
	}
	return v
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
