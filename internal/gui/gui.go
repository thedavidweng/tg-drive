//go:build gui

// Package gui is the desktop GUI facade (ADR 0031). Its services are bound
// to the frontend by cmd/td-gui; each method only translates a frontend call
// into a service call, the result into a DTO, and errors into Error. The
// package never imports Wails, so its tests need no webview or display.
package gui

import (
	"path/filepath"

	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// SessionFileName is the GUI's Telegram session file. It sits beside the
// CLI's session so each front end logs in on its own (ADR 0034).
const SessionFileName = "gui-session.json"

// Services are the facade services cmd/td-gui binds, one per frontend area.
type Services struct {
	Drive     *Drive
	Auth      *Auth
	Transfers *Transfers
	Settings  *Settings
}

// Open opens the service layer with the GUI's own session and returns the
// facade services with a function that closes them. Config and database
// resolve as they do for the CLI, so both front ends share one index.
func Open() (*Services, func(), error) {
	_, cliSession, _ := config.ResolvePaths(config.Overrides{})
	app, closeApp, err := service.Open(service.Options{
		SessionPath: filepath.Join(filepath.Dir(cliSession), SessionFileName),
	})
	if err != nil {
		return nil, func() {}, toError(err)
	}
	return &Services{
		Drive:     &Drive{app: app},
		Auth:      &Auth{},
		Transfers: &Transfers{},
		Settings:  &Settings{},
	}, closeApp, nil
}
