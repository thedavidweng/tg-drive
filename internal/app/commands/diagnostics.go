package commands

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
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
			data, err := app.Doctor(context.Background())
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
			data, err := app.PathCodecDoctor(context.Background())
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
			cfg, configPath, err := rt.LoadConfig()
			if err != nil {
				return r.Error(err)
			}
			if showSecrets && !confirm {
				if rt.JSON() || !stdinIsInteractive() {
					return r.Error(apperr.New(apperr.ErrConfirmationRequired, "--show-secrets requires --confirm when not running interactively"))
				}
				fmt.Fprint(os.Stderr, "show secrets? [y/N] ")
				var ans string
				_, _ = fmt.Scanln(&ans)
				if strings.ToLower(ans) != "y" {
					showSecrets = false
				}
			}
			if len(args) == 0 {
				all := config.RedactConfigMap(cfg, showSecrets)
				if rt.JSON() {
					return r.Success(all)
				}
				out := cmd.OutOrStdout()
				for _, k := range config.Keys {
					if v, ok := all[k]; ok {
						_, _ = fmt.Fprintf(out, "%s: %v\n", k, humanConfigValue(k, v))
					}
				}
				return nil
			}
			v, err := config.GetValue(cfg, args[0])
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]any{args[0]: config.RedactValue(args[0], v, showSecrets), "config_path": configPath})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%v\n", humanConfigValue(args[0], config.RedactValue(args[0], v, showSecrets)))
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
			cfg, configPath, err := rt.LoadConfig()
			if err != nil {
				return r.Error(err)
			}
			if err := config.SetValue(&cfg, args[0], args[1]); err != nil {
				return r.Error(err)
			}
			if err := config.Save(configPath, cfg); err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]string{"key": args[0], "status": "set"})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "set %s\n", args[0])
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
