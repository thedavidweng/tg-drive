package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/output"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/version"
)

// Runtime is the command runtime contract.
type Runtime interface {
	JSON() bool
	Channel() string
	Renderer() *output.Renderer
	LoadConfig() (config.Config, string, error)
	OpenApp(cmd *cobra.Command) (*service.App, func(), error)
}

func EnsureTelegramConfig(cfg *config.Config, configPath string, requirePhone bool) error {
	reader := bufio.NewReader(os.Stdin)
	if cfg.Telegram.APIID == 0 {
		fmt.Fprintln(os.Stderr, "Create a Telegram app at https://my.telegram.org/apps")
		fmt.Fprint(os.Stderr, "api_id: ")
		s, _ := reader.ReadString('\n')
		id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return apperr.New(apperr.ErrConfigInvalid, "invalid api_id")
		}
		cfg.Telegram.APIID = id
	}
	if cfg.Telegram.APIHash == "" {
		fmt.Fprint(os.Stderr, "api_hash: ")
		s, _ := reader.ReadString('\n')
		cfg.Telegram.APIHash = strings.TrimSpace(s)
	}
	if requirePhone && cfg.Telegram.Phone == "" {
		fmt.Fprint(os.Stderr, "phone (international, e.g. +1234567890): ")
		s, _ := reader.ReadString('\n')
		cfg.Telegram.Phone = strings.TrimSpace(s)
	}
	return config.Save(configPath, *cfg)
}

// groupUsage makes a command group reject unknown subcommands as usage
// errors (exit 2, rendered envelope) instead of printing help with exit 0.
func GroupUsage(rt Runtime, c *cobra.Command) {
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return rt.Renderer().Error(apperr.New(apperr.ErrUsage,
			fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())))
	}
}

// PrintKV writes a sorted key: value view of a result struct for human
// output, keyed by each field's JSON name.
func PrintKV(w io.Writer, result any) {
	v := reflect.Indirect(reflect.ValueOf(result))
	t := v.Type()
	type row struct {
		key string
		val any
	}
	rows := make([]row, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		rows = append(rows, row{name, v.Field(i).Interface()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%s: %v\n", r.key, r.val)
	}
}

func NewCompletionCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return rt.Renderer().Error(apperr.New(apperr.ErrUsage, "valid shells: bash, zsh, fish, powershell"))
			}
		},
	}
}

func NewVersionCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if rt.JSON() {
				return r.Success(map[string]string{
					"version":  version.Version,
					"commit":   version.Commit,
					"date":     version.Date,
					"built_by": version.BuiltBy,
				})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "td %s (%s)\n", version.Version, version.Commit)
			return err
		},
	}
}
