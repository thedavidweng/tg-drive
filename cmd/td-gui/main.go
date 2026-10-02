//go:build gui

// Command td-gui is the desktop GUI (ADR 0031). It is the only package that
// imports Wails; build it with -tags gui after building the frontend.
package main

import (
	"fmt"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/thedavidweng/tg-drive-cli/frontend"
	"github.com/thedavidweng/tg-drive-cli/internal/gui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "td-gui:", err)
		os.Exit(1)
	}
}

func run() error {
	svc, closeGUI, err := gui.Open()
	if err != nil {
		return err
	}
	defer closeGUI()

	app := application.New(application.Options{
		Name:        "td-gui",
		Description: "Telegram-backed drive",
		Services: []application.Service{
			application.NewService(svc.Drive),
			application.NewService(svc.Auth),
			application.NewService(svc.Transfers),
			application.NewService(svc.Settings),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(frontend.Assets()),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "td",
		Width:     960,
		Height:    640,
		MinWidth:  640,
		MinHeight: 420,
		URL:       "/",
	})
	return app.Run()
}
