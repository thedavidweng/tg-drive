package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/internal/app/commands"
	"github.com/thedavidweng/tg-drive-cli/internal/output"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/version"
)

// Execute runs the root command. SIGINT and SIGTERM cancel the command's
// context; the command stops at its next cancellation point and exits with
// ERR_CANCELLED.
func Execute() error {
	if code := run(); code != 0 {
		os.Exit(code)
	}
	return nil
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// Restore default signal handling once cancelled, so a second
		// interrupt terminates a command stuck past its cancellation points.
		<-ctx.Done()
		stop()
	}()
	cmd := NewRootCommand()
	err := cmd.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if _, ok := apperr.As(err); !ok {
		// Cobra usage errors (unknown command/flag, wrong arg count) are
		// not rendered by command handlers; render as a usage error so
		// --json consumers still get an envelope.
		_ = output.New(argvWantsJSON()).Error(apperr.New(apperr.ErrUsage, err.Error()))
		return 2
	}
	if code := apperr.ExitCode(err); code != 0 {
		return code
	}
	return 1
}

// argvWantsJSON detects --json for errors that occur before flag parsing
// completes (e.g. unknown root command).
func argvWantsJSON() bool {
	for _, a := range os.Args[1:] {
		if a == "--" {
			break
		}
		if a == "--json" || a == "--json=true" {
			return true
		}
	}
	return envBool("TD_JSON")
}

// NewRootCommand builds the CLI root.
func NewRootCommand() *cobra.Command {
	opts := &runtimeOpts{}
	cmd := &cobra.Command{
		Use:   "td",
		Short: "Telegram-backed virtual file tree CLI",
		Long: `Telegram-backed virtual file tree CLI.

Environment:
  TD_API_ID, TD_API_HASH  Telegram API credentials (https://my.telegram.org/apps)
  TD_PHONE                account phone number (international format)
  TD_CONFIG               config file path        (default ~/.config/tg-drive-cli/config.toml)
  TD_SESSION              session file path       (default ~/.config/tg-drive-cli/session.json)
  TD_DB                   local cache DB path     (default ~/.local/share/tg-drive-cli/local_cache.db)
  TD_CHANNEL              channel title or ID (same as --channel)
  TD_JSON                 set to 1 for JSON output (same as --json)
  TD_WAIT                 set to 1 to wait through safe flood waits (same as --wait)
  TD_VERBOSE              set to 1 for diagnostics on stderr (same as --verbose)

Get started:
  td auth setup && td auth login
  td init ~/Pictures --create-channel
  td cp ~/Pictures/beach.jpg /2024/beach.jpg
  td tree /`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
	}
	cmd.SetVersionTemplate("td {{.Version}} (" + version.Commit + ")\n")

	cmd.PersistentFlags().BoolVar(&opts.json, "json", false, "write JSON output")
	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "", "config file path")
	cmd.PersistentFlags().StringVar(&opts.dbPath, "db", "", "SQLite database path")
	cmd.PersistentFlags().StringVar(&opts.sessionPath, "session", "", "Telegram session path")
	cmd.PersistentFlags().BoolVar(&opts.quiet, "quiet", false, "suppress non-essential output")
	cmd.PersistentFlags().BoolVar(&opts.verbose, "verbose", false, "write diagnostics (paths, Telegram RPC timing, retries) to stderr")
	cmd.PersistentFlags().StringVar(&opts.channel, "channel", "", "channel title or ID")
	cmd.PersistentFlags().BoolVar(&opts.wait, "wait", false, "wait through safe Telegram flood waits")
	cmd.PersistentFlags().BoolVar(&opts.noWait, "no-wait", false, "fail immediately on Telegram flood waits")
	cmd.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		opts.ctx = c.Context()
		opts.start = time.Now()
		opts.requestID = uuid.NewString()
		opts.command = c.CommandPath()
		if !c.Flags().Changed("json") && envBool("TD_JSON") {
			opts.json = true
		}
		if !c.Flags().Changed("verbose") && envBool("TD_VERBOSE") {
			opts.verbose = true
		}
		if opts.wait && opts.noWait {
			// Render here: errors returned from PersistentPreRunE bypass the
			// command handlers and would otherwise exit silently.
			return opts.Renderer().Error(apperr.New(apperr.ErrFlagConflict, "--wait and --no-wait are mutually exclusive"))
		}
		if opts.channel == "" {
			opts.channel = os.Getenv("TD_CHANNEL")
		}
		return nil
	}

	cmd.AddGroup(&cobra.Group{ID: "core", Title: "Core"})
	cmd.AddGroup(&cobra.Group{ID: "auth", Title: "Authentication"})
	cmd.AddGroup(&cobra.Group{ID: "channels", Title: "Channels"})
	cmd.AddGroup(&cobra.Group{ID: "files", Title: "Files"})
	cmd.AddGroup(&cobra.Group{ID: "maintenance", Title: "Maintenance"})

	add := func(c *cobra.Command, group string) *cobra.Command {
		c.GroupID = group
		cmd.AddCommand(c)
		return c
	}

	add(commands.NewVersionCmd(opts), "core")
	add(commands.NewDoctorCmd(opts), "core")
	add(commands.NewConfigCmd(opts), "core")
	add(commands.NewAuthCmd(opts), "auth")
	add(commands.NewChannelsCmd(opts), "channels")
	add(commands.NewInitCmd(opts), "files")
	add(commands.NewStatusCmd(opts), "core")
	add(commands.NewScanCmd(opts), "maintenance")
	add(commands.NewLsCmd(opts), "files")
	add(commands.NewTreeCmd(opts), "files")
	add(commands.NewCpCmd(opts), "files")
	add(commands.NewGetCmd(opts), "files")
	add(commands.NewMvCmd(opts), "files")
	add(commands.NewRmCmd(opts), "files")
	add(commands.NewShareCmd(opts), "maintenance")
	add(commands.NewAdoptCmd(opts), "files")
	add(commands.NewImportCmd(opts), "files")
	add(commands.NewRepairCmd(opts), "maintenance")
	add(commands.NewCompletionCmd(opts), "core")

	return cmd
}

type runtimeOpts struct {
	json        bool
	quiet       bool
	verbose     bool
	configPath  string
	dbPath      string
	sessionPath string
	channel     string
	wait        bool
	noWait      bool
	command     string
	requestID   string
	start       time.Time
	// ctx is the command context; renderers map errors that follow its
	// cancellation to ERR_CANCELLED.
	ctx context.Context
}

func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// waitOverride resolves the flood-wait override: flags > TD_WAIT. Nil leaves
// the config default in place.
func (o *runtimeOpts) waitOverride() *bool {
	var wait *bool
	if os.Getenv("TD_WAIT") != "" {
		v := envBool("TD_WAIT")
		wait = &v
	}
	if o.wait {
		v := true
		wait = &v
	}
	if o.noWait {
		v := false
		wait = &v
	}
	return wait
}

func (o *runtimeOpts) Renderer() *output.Renderer {
	r := output.New(o.json)
	r.Quiet = o.quiet
	r.Command = o.command
	r.RequestID = o.requestID
	r.Start = o.start
	r.Ctx = o.ctx
	return r
}

func (o *runtimeOpts) JSON() bool { return o.json }

func (o *runtimeOpts) Channel() string { return o.channel }
func (o *runtimeOpts) serviceOptions() service.Options {
	opts := service.Options{
		ConfigPath:  o.configPath,
		DBPath:      o.dbPath,
		SessionPath: o.sessionPath,
		Channel:     o.channel,
		Wait:        o.waitOverride(),
	}
	if o.verbose {
		opts.Debugf = debugf
	}
	return opts
}

// debugf writes a --verbose diagnostic to stderr. Callers must not pass
// secrets; paths go through config.DisplayPath.
func debugf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "debug: "+format+"\n", args...)
}

func (o *runtimeOpts) Options() service.Options { return o.serviceOptions() }

func (o *runtimeOpts) OpenApp(cmd *cobra.Command) (*service.App, func(), error) {
	return o.open(cmd, false)
}

func (o *runtimeOpts) OpenOfflineApp(cmd *cobra.Command) (*service.App, func(), error) {
	return o.open(cmd, true)
}

func (o *runtimeOpts) open(cmd *cobra.Command, offline bool) (*service.App, func(), error) {
	if isLightweight(cmd) {
		return nil, func() {}, apperr.New(apperr.ErrUsage, "command does not use app context")
	}
	opts := o.serviceOptions()
	opts.Offline = offline
	return service.Open(opts)
}

func isLightweight(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "version", "completion":
			return true
		}
	}
	return false
}
