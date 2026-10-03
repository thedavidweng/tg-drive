package commands

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive/core/errors"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/internal/config"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// Authentication and root initialization commands.

func NewAuthCmd(rt Runtime) *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Authentication commands"}
	GroupUsage(rt, c)
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Configure Telegram API credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			data, err := service.SetupTelegram(rt.Options(), promptTelegram(cmd.Context()))
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "saved Telegram API credentials to %s\nnext: td auth login\n", data.ConfigPath)
			return nil
		},
	}
	var resend bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Login to Telegram",
		Long: "Login to Telegram interactively.\n\n" +
			"Telegram sends a login code to your phone. If you re-run this command\n" +
			"while a code is still pending, the pending code is reused instead of\n" +
			"requesting a new one (repeated code requests get the account\n" +
			"rate-limited for up to 24 hours). Use --resend to request a fresh code.\n" +
			"After a successful login the session is saved and other commands do not\n" +
			"require logging in again.",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			cfg, _, err := service.ConfigureTelegram(rt.Options(), true, promptTelegram(cmd.Context()))
			if err != nil {
				return r.Error(err)
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			codeFn := func(p telegram.CodePrompt) (string, error) {
				switch {
				case p.Attempt > 1:
					fmt.Fprintf(os.Stderr, "invalid code, try again (%d/%d): ", p.Attempt, p.MaxAttempts)
				case p.Reused:
					age := time.Since(p.SentAt).Round(time.Second)
					fmt.Fprintf(os.Stderr, "Reusing the code Telegram sent %s ago (run with --resend for a new one).\ncode: ", age)
				case p.Resent:
					fmt.Fprint(os.Stderr, "The previous code expired; Telegram sent a new code to your phone.\ncode: ")
				default:
					fmt.Fprint(os.Stderr, "Telegram sent a login code to your phone.\ncode: ")
				}
				s, err := readLine(cmd.Context())
				if apperr.IsCancelled(err) {
					return "", err
				}
				return strings.TrimSpace(s), nil
			}
			pwAttempt := 0
			pwFn := func() (string, error) {
				pwAttempt++
				if pwAttempt > 1 {
					fmt.Fprint(os.Stderr, "invalid password, try again: ")
				} else {
					fmt.Fprint(os.Stderr, "2fa password: ")
				}
				s, err := readLine(cmd.Context())
				if apperr.IsCancelled(err) {
					return "", err
				}
				return strings.TrimSpace(s), nil
			}
			data, err := app.AuthLogin(cmd.Context(), codeFn, pwFn, telegram.LoginOptions{ForceNewCode: resend})
			if err != nil {
				if ae, ok := apperr.As(err); ok && ae.Code == apperr.ErrTelegramRateLimited && !rt.JSON() {
					fmt.Fprintln(os.Stderr, "Telegram rate-limits accounts after repeated login code requests.")
					fmt.Fprintln(os.Stderr, "Wait for the shown duration before retrying. Re-running `td auth login`")
					fmt.Fprintln(os.Stderr, "during the wait does not help and may extend the block.")
				}
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			if data.AlreadyAuthenticated {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "already logged in as %v (use `td auth logout` to switch accounts)\n", data.DisplayName)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged in as %v; session saved to %s\n", data.DisplayName, cfg.Storage.SessionPath)
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "other td commands now reuse this session; next: td init <local-root> --create-channel")
			}
			return nil
		},
	}
	login.Flags().BoolVar(&resend, "resend", false, "request a fresh login code instead of reusing a pending one")
	status := &cobra.Command{
		Use:   "status",
		Short: "Show auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.AuthStatus(cmd.Context())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			switch {
			case data.Authenticated:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged in as %v (%v)\n", data.DisplayName, data.Phone)
			case app.Cfg.Telegram.APIID == 0 || app.Cfg.Telegram.APIHash == "":
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "not logged in; run: td auth setup, then td auth login")
			default:
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "not logged in; run: td auth login")
			}
			return nil
		},
	}
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Logout",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if err := app.AuthLogout(cmd.Context()); err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]string{"status": "logged_out"})
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "logged out")
			return nil
		},
	}
	c.AddCommand(setup, login, status, logout)
	return c
}

// createChannelDefault is the NoOptDefVal for a bare --create-channel:
// derive the channel title from --channel or the local root's name.
const (
	createChannelDefault = "auto"
	bindChannelPick      = "?"
)

func NewInitCmd(rt Runtime) *cobra.Command {
	var createCh, bindCh string
	c := &cobra.Command{
		Use:   "init <local-root>",
		Short: "Initialize a local root",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if createCh != "" && bindCh != "" {
				return r.Error(apperr.New(apperr.ErrFlagConflict, "--create-channel and --bind-channel are mutually exclusive"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if createCh == createChannelDefault {
				createCh = rt.Channel()
				if createCh == "" {
					createCh = service.DefaultChannelTitle(args[0])
				}
			}
			ctx := cmd.Context()
			bindChannel := bindCh
			if bindCh == bindChannelPick || (createCh == "" && bindCh == "" && rt.Channel() == "") {
				choices, err := app.InitChoices(ctx, args[0])
				if err != nil {
					return r.Error(err)
				}
				chs := choices.Channels
				if rt.JSON() {
					return r.Error(apperr.New(apperr.ErrChannelNotFound, "select a channel").WithDetails(map[string]any{"channels": channelsToMap(chs)}))
				}
				if len(chs) == 0 {
					return r.Error(apperr.New(apperr.ErrChannelNotFound, "no existing channels to bind; create one with: td init "+args[0]+" --create-channel"))
				}
				selected, err := selectChannelInteractively(ctx, chs)
				if err != nil {
					return r.Error(err)
				}
				if selected == nil {
					return r.Error(apperr.New(apperr.ErrUsage, "channel selection required"))
				}
				bindChannel = selected.Title
			} else if rt.Channel() != "" {
				bindChannel = rt.Channel()
			}
			data, err := app.InitRoot(ctx, args[0], rt.Channel(), createCh, bindChannel)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			if data.AlreadyInitialized {
				_, _ = fmt.Fprintf(out, "already initialized: %v is bound to channel %q (id %v)\n", data.LocalRoot, data.ChannelTitle, data.ChannelID)
				_, _ = fmt.Fprintln(out, "use --bind-channel to rebind, or init a different directory")
			} else {
				_, _ = fmt.Fprintf(out, "initialized %v -> channel %q (id %v)\n", config.DisplayPath(data.LocalRoot), data.ChannelTitle, data.ChannelID)
				if data.ScanError != "" {
					_, _ = fmt.Fprintf(os.Stderr, "warning: initial scan failed (%s); run: td scan --full\n", data.ScanError)
				} else if data.IndexedFiles != nil && *data.IndexedFiles > 0 {
					_, _ = fmt.Fprintf(out, "indexed %d existing files from Telegram; next: td tree /\n", *data.IndexedFiles)
					return nil
				}
				_, _ = fmt.Fprintln(out, "next: td cp <local-file> /<remote-path>")
			}
			return nil
		},
	}
	c.Flags().StringVar(&createCh, "create-channel", "", "create a new channel; bare flag derives the title, or pass --create-channel=<title>")
	c.Flags().Lookup("create-channel").NoOptDefVal = createChannelDefault
	c.Flags().StringVar(&bindCh, "bind-channel", "", "bind an existing channel; bare flag lists and prompts")
	c.Flags().Lookup("bind-channel").NoOptDefVal = bindChannelPick
	return c
}

func selectChannelInteractively(ctx context.Context, chs []telegram.Channel) (*telegram.Channel, error) {
	_, _ = fmt.Fprintln(os.Stderr, "Select a channel:")
	for i, ch := range chs {
		_, _ = fmt.Fprintf(os.Stderr, "  %d. %s (id %d)\n", i+1, ch.Title, ch.ID)
	}
	_, _ = fmt.Fprint(os.Stderr, "Enter number: ")
	s, err := readLine(ctx)
	if apperr.IsCancelled(err) {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > len(chs) {
		return nil, apperr.New(apperr.ErrUsage, "invalid channel selection")
	}
	return &chs[n-1], nil
}

func channelsToMap(chs []telegram.Channel) []map[string]any {
	out := make([]map[string]any, len(chs))
	for i, ch := range chs {
		out[i] = map[string]any{"id": ch.ID, "title": ch.Title, "username": ch.Username, "invite_link": ch.InviteLink}
	}
	return out
}
