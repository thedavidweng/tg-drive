//go:build gui && server

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/thedavidweng/tg-drive-cli/internal/gui"
)

// The server build (the UI preview) is headless: no tray, no close
// interception, no quit confirmation, and no single-instance lock — a
// crashed preview must never block the next run. desktop is the no-op
// stand-in main.go wires the same way.
type desktop struct{}

func newDesktop(*application.App, *application.WebviewWindow, *gui.Services) *desktop {
	return &desktop{}
}

func (*desktop) observeTransfer(string, any) {}

func (*desktop) shouldQuit() bool { return true }

func (*desktop) show() {}

func singleInstanceOptions(func()) *application.SingleInstanceOptions { return nil }
