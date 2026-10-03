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
	"github.com/thedavidweng/tg-drive-cli/internal/transfer"
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

// appState is the GUI's process model (ADR 0031). One base App owns the
// database and the one long-lived Telegram client of the GUI's session; one
// App per bound channel shares them, and the channel switcher only selects
// which App the views read. One Transfer Manager runs every GUI Transfer,
// each pinned to the channel it was submitted on, so a switch never touches
// a running Transfer and the concurrency limit covers them all.
//
// Only Auth replaces the base App (a fresh machine opens offline; setup and
// login need a connected one), and it refuses to while GUI Transfers run.
// Services resolve the App per call, never caching it.
type appState struct {
	mu        sync.Mutex
	base      *service.App
	closeBase func()
	// apps are the per-channel Apps over base, keyed by channel selector.
	apps map[string]*service.App
	// channel is the active channel selector (a bound channel's Telegram
	// ID); "" selects the first bound channel.
	channel string
	manager *transfer.Manager
	// observer is the Manager Observer wired in Open; a new Manager reuses
	// it.
	observer transfer.Observer
	// configStale marks a saved Settings change the running Manager has not
	// picked up yet: the Manager is replaced once it runs nothing.
	configStale bool
}

func newAppState(base *service.App, closeBase func(), observer transfer.Observer) *appState {
	s := &appState{base: base, closeBase: closeBase, apps: map[string]*service.App{}}
	s.observer = observer
	s.observer.OnIdle = s.onIdle
	s.manager = s.newManager(base)
	return s
}

func (s *appState) newManager(base *service.App) *transfer.Manager {
	return transfer.New(base, transfer.Options{FrontEnd: transfer.FrontEndGUI, Observer: s.observer})
}

// current is the App of the active channel.
func (s *appState) current() *service.App {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appLocked(s.channel)
}

func (s *appState) appLocked(channel string) *service.App {
	if app, ok := s.apps[channel]; ok {
		return app
	}
	b := s.base
	app := &service.App{Cfg: b.Cfg, ConfigPath: b.ConfigPath, DB: b.DB, TG: b.TG, Channel: channel}
	s.apps[channel] = app
	return app
}

// selected is the active channel selector, for pinning a Transfer to it.
func (s *appState) selected() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channel
}

// currentManager is the one Transfer Manager.
func (s *appState) currentManager() *transfer.Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manager
}

// running is how many Transfers this GUI owns and has not finished.
func (s *appState) running() int {
	return s.currentManager().Running()
}

// reopen replaces the base App with a freshly opened one — the offline App
// of a fresh machine with a connected one after setup — and rebuilds the
// per-channel Apps and the Manager over it. It refuses while this GUI runs
// Transfers, which the old App's client and database serve. The old App
// closes only after the new one opened, so a failed reopen keeps the
// previous state.
func (s *appState) reopen() error {
	if n := s.running(); n > 0 {
		return toError(apperr.New(apperr.ErrUsage,
			"transfers are running; wait for them to finish or cancel them first"))
	}
	app, closeApp, err := service.Open(openOptions())
	if err != nil {
		return toError(err)
	}
	s.mu.Lock()
	old := s.closeBase
	s.base, s.closeBase = app, closeApp
	s.apps = map[string]*service.App{}
	s.manager = s.newManager(app)
	s.configStale = false
	s.mu.Unlock()
	old()
	return nil
}

// reloadConfig applies a saved config change to the running GUI: the
// per-channel Apps pick the new config up at once, and the Manager (which
// fixes transfers.concurrency when built) is replaced now when it runs
// nothing, else when its last Transfer ends. Calls already running keep the
// App they started with; the database and Telegram client stay open.
func (s *appState) reloadConfig() error {
	cfg, path, err := service.LoadConfig(openOptions())
	if err != nil {
		return toError(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.base
	s.base = &service.App{Cfg: cfg, ConfigPath: path, DB: b.DB, TG: b.TG}
	s.apps = map[string]*service.App{}
	s.configStale = true
	if s.manager.Running() == 0 {
		s.manager = s.newManager(s.base)
		s.configStale = false
	}
	return nil
}

// onIdle swaps in the Manager a Settings change asked for, once the old
// one finished its last Transfer.
func (s *appState) onIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configStale && s.manager.Running() == 0 {
		s.manager = s.newManager(s.base)
		s.configStale = false
	}
}

// switchChannel selects another bound channel's App. Nothing is reopened.
func (s *appState) switchChannel(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channel = channel
}

func (s *appState) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeBase()
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
	transfers := &Transfers{seen: map[string]Transfer{}}
	state := newAppState(app, closeApp, transfer.Observer{
		OnStage:    transfers.observeStage,
		OnProgress: transfers.observeProgress,
	})
	transfers.state = state
	settings := &Settings{state: state}
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

// RunningTransfers is how many Transfers this GUI owns and has not
// finished: the ones quitting would interrupt. Another front end's
// Transfers keep running when the GUI quits, so they do not count.
func (s *Services) RunningTransfers() int {
	return s.state.running()
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

// SetFilePicker connects the native file dialogs the Transfers facade's
// PickFiles and PickDirectory open. cmd/td-gui connects the Wails dialog
// manager; tests connect a fake. Services is not a bound Wails service, so
// this method is not in the frontend bindings.
func (s *Services) SetFilePicker(p FilePicker) {
	s.Transfers.pick = p
}

// SetChannelsEmitter wires how the Channels facade's typed event
// (channels-changed) reaches the frontend, same seam as SetDriveEmitter.
func (s *Services) SetChannelsEmitter(em Emitter) {
	s.Channels.mu.Lock()
	defer s.Channels.mu.Unlock()
	s.Channels.emit = em
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
