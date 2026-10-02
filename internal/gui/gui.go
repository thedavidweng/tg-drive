//go:build gui

// Package gui is the desktop GUI facade (ADR 0031). Its services are bound
// to the frontend by cmd/td-gui; each method only translates a frontend call
// into a service call, the result into a DTO, and errors into Error. The
// package never imports Wails, so its tests need no webview or display.
package gui

import (
	"context"
	"path/filepath"
	"sync"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// SessionFileName is the GUI's Telegram session file. It sits beside the
// CLI's session so each front end logs in on its own (ADR 0034).
const SessionFileName = "gui-session.json"

// DeviceModel is the GUI's Telegram device identity: its logins show up
// under this name in Telegram's device list, separate from the CLI's (ADR
// 0034).
const DeviceModel = "td-gui"

// Services are the facade services cmd/td-gui binds, one per frontend area.
type Services struct {
	Drive     *Drive
	Auth      *Auth
	Channels  *Channels
	Transfers *Transfers
	Settings  *Settings

	state *appState
}

// appState owns the service App behind a mutex so Auth can reopen it: a
// fresh machine opens offline (no Telegram client) and setup upgrades it to
// a connected one. Services resolve the App per call, never caching it.
type appState struct {
	mu       sync.Mutex
	app      *service.App
	closeApp func()
	// channel is the active channel selector (a bound channel's Telegram
	// ID). The App's selector is fixed at Open (service.Options.Channel),
	// so Channels switches channels by reopening the App with another one;
	// "" selects the first bound channel.
	channel string
}

func (s *appState) current() *service.App {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app
}

// reopen replaces the App with a freshly opened one, bound to the selected
// channel. The old App closes only after the new one opened, so a failed
// reopen keeps the previous state.
func (s *appState) reopen() error {
	opts := openOptions()
	s.mu.Lock()
	opts.Channel = s.channel
	s.mu.Unlock()
	app, closeApp, err := service.Open(opts)
	if err != nil {
		return toError(err)
	}
	s.mu.Lock()
	old := s.closeApp
	s.app, s.closeApp = app, closeApp
	s.mu.Unlock()
	old()
	return nil
}

// switchChannel reopens the App bound to another channel. A failed reopen
// keeps the previous App and selection.
func (s *appState) switchChannel(channel string) error {
	s.mu.Lock()
	previous := s.channel
	s.channel = channel
	s.mu.Unlock()
	if err := s.reopen(); err != nil {
		s.mu.Lock()
		s.channel = previous
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *appState) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeApp()
}

func openOptions() service.Options {
	_, cliSession, _ := config.ResolvePaths(config.Overrides{})
	return service.Options{
		SessionPath: filepath.Join(filepath.Dir(cliSession), SessionFileName),
		DeviceModel: DeviceModel,
	}
}

// Open opens the service layer with the GUI's own session and returns the
// facade services with a function that closes them. Config and database
// resolve as they do for the CLI, so both front ends share one index. A
// machine without Telegram credentials opens offline instead of failing, so
// the setup screen is reachable; Auth.Setup reopens the App online.
func Open() (*Services, func(), error) {
	opts := openOptions()
	app, closeApp, err := service.Open(opts)
	if err != nil {
		if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrConfigMissing {
			return nil, func() {}, toError(err)
		}
		opts := openOptions()
		opts.Offline = true
		app, closeApp, err = service.Open(opts)
		if err != nil {
			return nil, func() {}, toError(err)
		}
	}
	settings := &Settings{opts: opts}
	state := &appState{app: app, closeApp: closeApp}
	closeServices := state.close
	if omarchyDetect() && omarchyThemeDir() != "" {
		ctx, cancel := context.WithCancel(context.Background())
		settings.ThemeChanges = watchOmarchy(ctx, omarchyPollInterval())
		closeServices = func() {
			cancel()
			state.close()
		}
	}
	drive := &Drive{state: state}
	return &Services{
		Drive:     drive,
		Auth:      &Auth{state: state, prompts: map[string]chan promptAnswer{}},
		Channels:  &Channels{state: state, drive: drive},
		Transfers: &Transfers{},
		Settings:  settings,
		state:     state,
	}, closeServices, nil
}

// SetPromptEmitter wires how auth prompts reach the frontend. cmd/td-gui
// connects it to the typed auth.prompt Wails event; tests connect their own.
// Services is not a bound Wails service, so this method is not in the
// frontend bindings.
func (s *Services) SetPromptEmitter(emit func(AuthPrompt)) {
	s.Auth.mu.Lock()
	defer s.Auth.mu.Unlock()
	s.Auth.emitter = emit
}

// SetDriveEmitter wires how the Drive facade's typed events
// (directory-changed, scan-progress) reach the frontend. cmd/td-gui
// connects it to the Wails event manager; tests connect a recorder.
// Services is not a bound Wails service, so this method is not in the
// frontend bindings.
func (s *Services) SetDriveEmitter(em Emitter) {
	s.Drive.emit = em
}
