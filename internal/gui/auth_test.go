//go:build gui

package gui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// promptRecorder wires the facade's prompt emitter the way cmd/td-gui does,
// answering each prompt through the AnswerPrompt binding as the frontend
// would.
type promptRecorder struct {
	svc *gui.Services

	mu      sync.Mutex
	prompts []gui.AuthPrompt
	// answers supplies the value answered per prompt kind ("code",
	// "password"); a missing kind cancels the prompt.
	answers map[string]string
}

func newPromptRecorder(svc *gui.Services, answers map[string]string) *promptRecorder {
	r := &promptRecorder{svc: svc, answers: answers}
	svc.SetPromptEmitter(r.emit)
	return r
}

func (r *promptRecorder) emit(p gui.AuthPrompt) {
	r.mu.Lock()
	r.prompts = append(r.prompts, p)
	r.mu.Unlock()
	go func() {
		value, ok := r.answers[p.Kind]
		if ok {
			_ = r.svc.Auth.AnswerPrompt(context.Background(), p.ID, value)
		} else {
			_ = r.svc.Auth.CancelPrompt(context.Background(), p.ID)
		}
	}()
}

func (r *promptRecorder) got() []gui.AuthPrompt {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]gui.AuthPrompt(nil), r.prompts...)
}

// freshMachine points td at an empty config, database, and session set
// against the persistent fake Telegram, with no API credentials: the state
// the GUI sees on a first run.
func freshMachine(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TD_FAKE_TELEGRAM", "1")
	t.Setenv("TD_FAKE_TELEGRAM_STATE", filepath.Join(dir, "fake.json"))
	t.Setenv("TD_CONFIG", filepath.Join(dir, "config.toml"))
	t.Setenv("TD_DB", filepath.Join(dir, "td.db"))
	t.Setenv("TD_SESSION", filepath.Join(dir, "session.json"))
	t.Setenv("TD_API_ID", "")
	t.Setenv("TD_API_HASH", "")
	t.Setenv("TD_PHONE", "")
	return dir
}

func TestAuthStatusOnFreshMachine(t *testing.T) {
	freshMachine(t)
	svc := openGUI(t)

	st, err := svc.Auth.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Configured || st.HasPhone || st.Authenticated {
		t.Fatalf("Status = %+v, want an unconfigured, unauthenticated machine", st)
	}
}

func TestAuthSetupSavesCredentials(t *testing.T) {
	dir := freshMachine(t)
	svc := openGUI(t)
	ctx := context.Background()

	st, err := svc.Auth.Setup(ctx, "1234", "0123456789abcdef", "+15550001")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Configured || !st.HasPhone || st.Authenticated {
		t.Fatalf("Status after Setup = %+v, want configured with phone, not yet authenticated", st)
	}

	// The saved config is the one the CLI would read: reopening sees it.
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"api_id = 1234", `api_hash = '0123456789abcdef'`, `phone = '+15550001'`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("config.toml missing %q:\n%s", want, data)
		}
	}
}

func TestAuthSetupRejectsBadInput(t *testing.T) {
	freshMachine(t)
	svc := openGUI(t)
	ctx := context.Background()

	for _, args := range [][3]string{
		{"not-a-number", "hash", "+15550001"},
		{"1234", "", "+15550001"},
		{"1234", "hash", ""},
	} {
		_, err := svc.Auth.Setup(ctx, args[0], args[1], args[2])
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) {
			t.Fatalf("Setup%v error = %T %v, want *gui.Error", args, err, err)
		}
		if guiErr.Code != "ERR_CONFIG_INVALID" || guiErr.Category != "config" {
			t.Fatalf("Setup%v error = %+v, want ERR_CONFIG_INVALID / config", args, guiErr)
		}
	}
}

func TestAuthLoginAnswersPromptsThroughEvents(t *testing.T) {
	freshMachine(t)
	svc := openGUI(t)
	ctx := context.Background()
	rec := newPromptRecorder(svc, map[string]string{"code": "12345"})

	if _, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001"); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Auth.Login(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.AlreadyAuthenticated || res.User.DisplayName != "Test User" || res.User.UserID != 42 {
		t.Fatalf("Login = %+v, want a fresh login as Test User (42)", res)
	}

	prompts := rec.got()
	if len(prompts) != 1 {
		t.Fatalf("prompts = %+v, want exactly one code prompt", prompts)
	}
	p := prompts[0]
	if p.ID == "" || p.Kind != "code" || p.Attempt != 1 || p.Reused || p.Resent {
		t.Fatalf("prompt = %+v, want a first-attempt code prompt", p)
	}

	st, err := svc.Auth.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Authenticated || st.User == nil || st.User.DisplayName != "Test User" {
		t.Fatalf("Status after login = %+v, want authenticated as Test User", st)
	}
}

func TestAuthLoginCancelledPrompt(t *testing.T) {
	freshMachine(t)
	svc := openGUI(t)
	ctx := context.Background()
	// No answers registered: every prompt is cancelled.
	newPromptRecorder(svc, nil)

	if _, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Auth.Login(ctx, "", false)
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Login error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CANCELLED" || guiErr.Category != "cancelled" {
		t.Fatalf("Login error = %+v, want ERR_CANCELLED / cancelled", guiErr)
	}
}

func TestAuthLoginWithTwoFactor(t *testing.T) {
	freshMachine(t)
	t.Setenv("TD_FAKE_AUTH_PASSWORD", "hunter2")
	svc := openGUI(t)
	ctx := context.Background()
	rec := newPromptRecorder(svc, map[string]string{"code": "12345", "password": "hunter2"})

	if _, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001"); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Auth.Login(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.User.DisplayName != "Test User" {
		t.Fatalf("Login = %+v, want Test User", res)
	}

	prompts := rec.got()
	if len(prompts) != 2 {
		t.Fatalf("prompts = %+v, want a code prompt then a password prompt", prompts)
	}
	if prompts[0].Kind != "code" || prompts[1].Kind != "password" || prompts[1].Attempt != 1 {
		t.Fatalf("prompts = %+v, want code then first-attempt password", prompts)
	}
	if prompts[0].ID == prompts[1].ID {
		t.Fatalf("prompts share id %q; each prompt needs its own", prompts[0].ID)
	}
}

func TestAuthLoginRateLimitMapsWaitDetails(t *testing.T) {
	freshMachine(t)
	t.Setenv("TD_FAKE_LOGIN_FLOOD_WAIT", "42")
	svc := openGUI(t)
	ctx := context.Background()
	rec := newPromptRecorder(svc, map[string]string{"code": "12345"})

	if _, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Auth.Login(ctx, "", false)
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Login error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_TELEGRAM_RATE_LIMITED" || guiErr.Category != "api" || guiErr.Message == "" {
		t.Fatalf("Login error = %+v, want ERR_TELEGRAM_RATE_LIMITED / api with a message", guiErr)
	}
	if got := guiErr.Details["retry_after_seconds"]; got != 42 {
		t.Fatalf("Details[retry_after_seconds] = %v, want 42 (details: %v)", got, guiErr.Details)
	}
	if got, ok := guiErr.Details["retry_at"].(string); !ok || got == "" {
		t.Fatalf("Details[retry_at] = %v, want an RFC 3339 timestamp", guiErr.Details["retry_at"])
	}
	// Telegram refused before a code was sent: no prompt reached the user.
	if prompts := rec.got(); len(prompts) != 0 {
		t.Fatalf("prompts = %+v, want none before the rate limit", prompts)
	}
}

func TestAuthLogoutReturnsToLoginState(t *testing.T) {
	freshMachine(t)
	svc := openGUI(t)
	ctx := context.Background()
	newPromptRecorder(svc, map[string]string{"code": "12345"})

	if _, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Auth.Login(ctx, "", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Auth.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Auth.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Authenticated || st.User != nil {
		t.Fatalf("Status after logout = %+v, want unauthenticated", st)
	}
	if !st.Configured {
		t.Fatalf("Status after logout = %+v, want credentials kept", st)
	}

	// The account can log in again on the same GUI session.
	if _, err := svc.Auth.Login(ctx, "", false); err != nil {
		t.Fatalf("re-login after logout: %v", err)
	}
}

func TestGUIAndCLIHoldSeparateSessions(t *testing.T) {
	// The CLI's own environment: credentials present, its own session path.
	dir := t.TempDir()
	t.Setenv("TD_FAKE_TELEGRAM", "1")
	t.Setenv("TD_FAKE_TELEGRAM_STATE", filepath.Join(dir, "fake.json"))
	t.Setenv("TD_CONFIG", filepath.Join(dir, "config.toml"))
	t.Setenv("TD_DB", filepath.Join(dir, "td.db"))
	t.Setenv("TD_SESSION", filepath.Join(dir, "session.json"))
	t.Setenv("TD_API_ID", "1")
	t.Setenv("TD_API_HASH", "hash")
	t.Setenv("TD_PHONE", "+1000")
	ctx := context.Background()

	// The CLI front end logs in on the default session.
	cli, closeCLI, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeCLI()
	if _, err := cli.AuthLogin(ctx,
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{}); err != nil {
		t.Fatal(err)
	}

	// The GUI logs in alongside on its own session.
	svc := openGUI(t)
	newPromptRecorder(svc, map[string]string{"code": "12345"})
	if _, err := svc.Auth.Login(ctx, "", false); err != nil {
		t.Fatal(err)
	}

	for _, lock := range []string{"session.json.lock", "gui-session.json.lock"} {
		if _, err := os.Stat(filepath.Join(dir, lock)); err != nil {
			t.Fatalf("expected %s to exist: %v", lock, err)
		}
	}

	// Both front ends are logged in at once, on their own sessions.
	cliUser, ok, err := cli.TG.Status(ctx)
	if err != nil || !ok {
		t.Fatalf("CLI status = %v, %v; want logged in", ok, err)
	}
	st, err := svc.Auth.Status(ctx)
	if err != nil || !st.Authenticated {
		t.Fatalf("GUI status = %+v, %v; want authenticated", st, err)
	}
	if st.User == nil || st.User.UserID != cliUser.ID {
		t.Fatalf("GUI user = %+v, CLI user = %+v; want the same account", st.User, cliUser)
	}
}
