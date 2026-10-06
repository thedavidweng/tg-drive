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

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/config"
	"github.com/thedavidweng/tg-drive/internal/service"
	"github.com/thedavidweng/tg-drive/internal/transfer"
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
	Drive       *Drive
	Auth        *Auth
	Channels    *Channels
	Transfers   *Transfers
	Settings    *Settings
	Import      *Import
	Maintenance *Maintenance

	state *appState
}

// appState owns the service App behind a mutex so Auth can reopen it: a
// fresh machine opens offline (no Telegram client) and setup upgrades it to
// a connected one. Services resolve the App per call, never caching it. It
// also owns the one Transfer Manager every facade shares; the Manager is
// rebuilt on an Auth reopen, since it binds the App it runs Transfers on,
// but every Manager shares one Limiter, so the concurrency budget is global
// and follows the saved transfers.concurrency live.
type appState struct {
	mu       sync.Mutex
	app      *service.App
	closeApp func()
	// channel is the active channel selector (a bound channel's Telegram
	// ID); "" selects the first bound channel. Switching it never reopens
	// the App: use pins it on each call's ctx, and a Transfer pins the
	// channel it was submitted on, so running Transfers keep their App,
	// Telegram client, and channel across a switch.
	channel string
	manager *transfer.Manager
	limiter *transfer.Limiter
	// observer is the Manager Observer wired in Open; a reopen reuses it
	// for the new Manager.
	observer transfer.Observer
}

func (s *appState) current() *service.App {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app
}

// use is the current App and ctx selecting the active channel for calls
// made with it.
func (s *appState) use(ctx context.Context) (*service.App, context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app, service.WithChannel(ctx, s.channel)
}

// scoped is ctx selecting the active channel.
func (s *appState) scoped(ctx context.Context) context.Context {
	_, ctx = s.use(ctx)
	return ctx
}

// activeChannel is the active channel selector; "" selects the first bound
// channel.
func (s *appState) activeChannel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channel
}

func (s *appState) newManager(app *service.App) *transfer.Manager {
	return transfer.New(app, transfer.Options{FrontEnd: transfer.FrontEndGUI, Observer: s.observer, Limiter: s.limiter})
}

// currentManager is the Transfer Manager of the current App.
func (s *appState) currentManager() *transfer.Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manager
}

// reopen replaces the App with a freshly opened one, after Auth changed the
// Telegram config, and a Transfer Manager for it. The old App closes only
// after the new one opened, so a failed reopen keeps the previous state.
// Auth reopens only while logged out, when no Transfer can run.
func (s *appState) reopen() error {
	app, closeApp, err := service.Open(openOptions())
	if err != nil {
		return toError(err)
	}
	if n := app.Cfg.Transfers.Concurrency; n > 0 {
		s.limiter.SetLimit(n)
	}
	manager := s.newManager(app)
	s.mu.Lock()
	old := s.closeApp
	s.app, s.closeApp = app, closeApp
	s.manager = manager
	s.mu.Unlock()
	old()
	return nil
}

// switchChannel makes channel the active one for every later call.
func (s *appState) switchChannel(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channel = channel
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
	state := &appState{app: app, closeApp: closeApp, limiter: transfer.NewLimiter(app.Cfg.Transfers.Concurrency)}
	settings := &Settings{opts: opts, state: state}
	transfers := &Transfers{state: state, seen: map[string]Transfer{}}
	state.observer = transfer.Observer{
		OnStage:    transfers.observeStage,
		OnProgress: transfers.observeProgress,
	}
	state.manager = state.newManager(app)
	closeServices := state.close
	if omarchyDetect() && omarchyThemeDir() != "" {
		ctx, cancel := context.WithCancel(context.Background())
		settings.ThemeChanges = watchOmarchy(ctx, omarchyPollInterval())
		closeServices = func() {
			cancel()
			state.close()
		}
	}
	drive := &Drive{state: state, media: newMedia(state)}
	return &Services{
		Drive:       drive,
		Auth:        &Auth{state: state, prompts: map[string]chan promptAnswer{}},
		Channels:    &Channels{state: state, drive: drive},
		Transfers:   transfers,
		Settings:    settings,
		Import:      &Import{state: state, prompts: map[string]chan promptAnswer{}},
		Maintenance: &Maintenance{state: state},
		state:       state,
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

// SetTransferEmitter wires how the Transfers facade's typed events
// (transfer-stage, transfer-progress, transfer-removed) reach the
// frontend. cmd/td-gui connects it to the Wails event manager; tests
// connect a recorder. Services is not a bound Wails service, so this
// method is not in the frontend bindings.
func (s *Services) SetTransferEmitter(em Emitter) {
	s.Transfers.mu.Lock()
	defer s.Transfers.mu.Unlock()
	s.Transfers.emit = em
}

// SetChannelsEmitter wires how the Channels facade's typed event
// (channels-changed) reaches the frontend, same seam as SetDriveEmitter.
func (s *Services) SetChannelsEmitter(em Emitter) {
	s.Channels.mu.Lock()
	defer s.Channels.mu.Unlock()
	s.Channels.emit = em
}

// SetFilePicker connects the native file dialogs the Transfers facade's
// PickFiles and PickDirectory open. cmd/td-gui connects the Wails dialog
// manager; tests connect a fake. Services is not a bound Wails service, so
// this method is not in the frontend bindings.
func (s *Services) SetFilePicker(p FilePicker) {
	s.Transfers.pick = p
}

// SetImportEmitter wires how the Import facade's typed events
// (import.prompt, import.item) reach the frontend, same seam as
// SetDriveEmitter.
func (s *Services) SetImportEmitter(em Emitter) {
	s.Import.emit = em
}

// SetMaintenanceEmitter wires how the Maintenance facade's typed events
// (repair.item) reach the frontend, same seam as SetDriveEmitter.
func (s *Services) SetMaintenanceEmitter(em Emitter) {
	s.Maintenance.emit = em
}
