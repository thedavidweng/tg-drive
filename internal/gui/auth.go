//go:build gui

package gui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// Auth is the facade service for Telegram setup, login, and logout.
type Auth struct {
	state *appState

	mu        sync.Mutex
	emitter   func(AuthPrompt)
	prompts   map[string]chan promptAnswer
	promptSeq int
	loggingIn bool
}

// Prompt kinds emitted on the auth.prompt event.
const (
	PromptCode     = "code"
	PromptPassword = "password"
)

// AuthPrompt is the auth.prompt event payload: login needs an answer from
// the user. The frontend answers with Auth.AnswerPrompt (or Auth.CancelPrompt)
// using the prompt's ID.
type AuthPrompt struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Attempt is the 1-based entry attempt for the current code or password.
	Attempt int `json:"attempt"`
	// MaxAttempts bounds code attempts; 0 when the flow does not say
	// (password prompts).
	MaxAttempts int `json:"max_attempts"`
	// Reused is true when the prompt is for a code sent by an earlier login,
	// Resent when a fresh code was just sent because the previous one
	// expired.
	Reused bool `json:"reused"`
	Resent bool `json:"resent"`
	// SentAt is when the code was sent (RFC 3339), empty when unknown.
	SentAt string `json:"sent_at,omitempty"`
}

// promptAnswer is what the frontend sends back for one AuthPrompt.
type promptAnswer struct {
	value string
	err   error
}

// AuthUser identifies the logged-in Telegram account.
type AuthUser struct {
	DisplayName string `json:"display_name"`
	Phone       string `json:"phone"`
	UserID      int64  `json:"user_id"`
}

// AuthStatus is what the shell needs to choose between the setup, login,
// and main screens.
type AuthStatus struct {
	// Configured is true when api_id and api_hash are saved.
	Configured bool `json:"configured"`
	// HasPhone is true when a phone number is saved; the login screen asks
	// for one when it is not.
	HasPhone      bool      `json:"has_phone"`
	Authenticated bool      `json:"authenticated"`
	User          *AuthUser `json:"user,omitempty"`
}

// Setup saves the API credentials and phone from the setup form through the
// service's Telegram config setup, then reopens the App online and returns
// the new status, so the frontend can move straight to login. All three
// fields are required.
func (a *Auth) Setup(ctx context.Context, apiID, apiHash, phone string) (*AuthStatus, error) {
	answers := map[service.TelegramField]string{
		service.TelegramAPIID:   strings.TrimSpace(apiID),
		service.TelegramAPIHash: strings.TrimSpace(apiHash),
		service.TelegramPhone:   strings.TrimSpace(phone),
	}
	if _, err := strconv.ParseInt(answers[service.TelegramAPIID], 10, 64); err != nil {
		return nil, toError(apperr.New(apperr.ErrConfigInvalid, "invalid api_id"))
	}
	if answers[service.TelegramAPIHash] == "" {
		return nil, toError(apperr.New(apperr.ErrConfigInvalid, "api_hash required"))
	}
	if answers[service.TelegramPhone] == "" {
		return nil, toError(apperr.New(apperr.ErrConfigInvalid, "phone required"))
	}
	ask := func(f service.TelegramField) (string, error) { return answers[f], nil }
	if _, _, err := service.ConfigureTelegram(service.Options{}, true, ask); err != nil {
		return nil, toError(err)
	}
	// The online reopen creates the session directory and the database that
	// service.SetupTelegram would, beside the GUI's own session file.
	if err := a.state.reopen(); err != nil {
		return nil, err
	}
	return a.Status(ctx)
}

// LoginResult reports a completed login.
type LoginResult struct {
	AlreadyAuthenticated bool     `json:"already_authenticated"`
	User                 AuthUser `json:"user"`
}

// Login runs the interactive Telegram login. When the config has no phone
// yet, phone is saved first (the login screen asks for it in that case).
// forceNewCode requests a fresh code instead of reusing a pending one. The
// login code and the 2FA password arrive as auth.prompt events and are
// answered with AnswerPrompt.
func (a *Auth) Login(ctx context.Context, phone string, forceNewCode bool) (*LoginResult, error) {
	a.mu.Lock()
	if a.loggingIn {
		a.mu.Unlock()
		return nil, toError(apperr.New(apperr.ErrUsage, "login already in progress"))
	}
	a.loggingIn = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.loggingIn = false
		a.mu.Unlock()
	}()

	app := a.state.current()
	if app.Cfg.Telegram.Phone == "" {
		phone = strings.TrimSpace(phone)
		if phone == "" {
			return nil, toError(apperr.New(apperr.ErrConfigMissing, "telegram.phone required"))
		}
		ask := func(service.TelegramField) (string, error) { return phone, nil }
		if _, _, err := service.ConfigureTelegram(service.Options{}, true, ask); err != nil {
			return nil, toError(err)
		}
		// The open App cached the config without the phone; reload it.
		if err := a.state.reopen(); err != nil {
			return nil, err
		}
		app = a.state.current()
	}
	if app.TG == nil {
		// Setup ran in this session but the App is still offline.
		if err := a.state.reopen(); err != nil {
			return nil, err
		}
		app = a.state.current()
	}

	passwordAttempt := 0
	codeFn := func(p telegram.CodePrompt) (string, error) {
		prompt := AuthPrompt{
			Kind:        PromptCode,
			Attempt:     p.Attempt,
			MaxAttempts: p.MaxAttempts,
			Reused:      p.Reused,
			Resent:      p.Resent,
		}
		if !p.SentAt.IsZero() {
			prompt.SentAt = p.SentAt.UTC().Format(time.RFC3339)
		}
		return a.ask(ctx, prompt)
	}
	passwordFn := func() (string, error) {
		passwordAttempt++
		return a.ask(ctx, AuthPrompt{Kind: PromptPassword, Attempt: passwordAttempt})
	}
	res, err := app.AuthLogin(ctx, codeFn, passwordFn, telegram.LoginOptions{ForceNewCode: forceNewCode})
	if err != nil {
		return nil, toError(err)
	}
	return &LoginResult{
		AlreadyAuthenticated: res.AlreadyAuthenticated,
		User: AuthUser{
			DisplayName: res.DisplayName,
			Phone:       res.Phone,
			UserID:      res.UserID,
		},
	}, nil
}

// Logout logs the GUI session out. The saved credentials stay, so logging
// in again only needs a new code.
func (a *Auth) Logout(ctx context.Context) error {
	app := a.state.current()
	if app.TG == nil {
		return toError(apperr.New(apperr.ErrAuthRequired, "not logged in"))
	}
	return toError(app.AuthLogout(ctx))
}

// AnswerPrompt supplies the value for the pending prompt with the given ID.
func (a *Auth) AnswerPrompt(_ context.Context, id, value string) error {
	return a.resolve(id, promptAnswer{value: value})
}

// CancelPrompt aborts the pending prompt with the given ID, cancelling the
// login that asked.
func (a *Auth) CancelPrompt(_ context.Context, id string) error {
	return a.resolve(id, promptAnswer{err: apperr.Cancelled()})
}

func (a *Auth) resolve(id string, answer promptAnswer) error {
	a.mu.Lock()
	ch, ok := a.prompts[id]
	a.mu.Unlock()
	if !ok {
		return toError(apperr.New(apperr.ErrUsage, "no pending prompt with that id"))
	}
	// The buffer holds one answer; a duplicate drops instead of parking this
	// binding call on a channel nobody reads any more.
	select {
	case ch <- answer:
	default:
	}
	return nil
}

// ask emits the prompt through the wired emitter and waits for the
// frontend's answer.
func (a *Auth) ask(ctx context.Context, prompt AuthPrompt) (string, error) {
	a.mu.Lock()
	a.promptSeq++
	prompt.ID = fmt.Sprintf("prompt-%d", a.promptSeq)
	ch := make(chan promptAnswer, 1)
	a.prompts[prompt.ID] = ch
	emitter := a.emitter
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.prompts, prompt.ID)
		a.mu.Unlock()
	}()
	if emitter == nil {
		return "", fmt.Errorf("auth prompt emitter not wired")
	}
	emitter(prompt)
	select {
	case answer := <-ch:
		return answer.value, answer.err
	case <-ctx.Done():
		return "", apperr.Cancelled()
	}
}

// Status reports the GUI session's setup and login state. An offline app
// (no credentials yet) is simply unauthenticated.
func (a *Auth) Status(ctx context.Context) (*AuthStatus, error) {
	app := a.state.current()
	st := &AuthStatus{
		Configured: app.Cfg.Telegram.APIID != 0 && app.Cfg.Telegram.APIHash != "",
		HasPhone:   app.Cfg.Telegram.Phone != "",
	}
	if app.TG == nil {
		return st, nil
	}
	res, err := app.AuthStatus(ctx)
	if err != nil {
		return nil, toError(err)
	}
	st.Authenticated = res.Authenticated
	if res.AuthUser != nil {
		st.User = &AuthUser{
			DisplayName: res.DisplayName,
			Phone:       res.Phone,
			UserID:      res.UserID,
		}
	}
	return st, nil
}
