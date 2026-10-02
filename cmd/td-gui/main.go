//go:build gui

// Command td-gui is the desktop GUI (ADR 0031). It is the only package that
// imports Wails; build it with -tags gui after building the frontend.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

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
			application.NewService(svc.Channels),
			application.NewService(svc.Transfers),
			application.NewService(svc.Settings),
			application.NewService(svc.Import),
			application.NewService(svc.Maintenance),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(frontend.Assets()),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	svc.SetPromptEmitter(func(p gui.AuthPrompt) {
		app.Event.Emit(EventAuthPrompt, p)
	})
	svc.SetDriveEmitter(func(name string, data any) {
		app.Event.Emit(name, data)
	})
	svc.SetTransferEmitter(func(name string, data any) {
		app.Event.Emit(name, data)
	})
	svc.SetFilePicker(newPicker(app))
	svc.SetImportEmitter(func(name string, data any) {
		app.Event.Emit(name, data)
	})
	svc.SetMaintenanceEmitter(func(name string, data any) {
		app.Event.Emit(name, data)
	})
	syncCtx, stopSync := context.WithCancel(context.Background())
	defer stopSync()
	svc.StartSync(syncCtx, gui.DefaultSyncInterval)
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "td",
		Width:     960,
		Height:    640,
		MinWidth:  640,
		MinHeight: 420,
		URL:       "/",
		// Files dragged from the OS onto a data-file-drop-target element
		// surface as the window's WindowFilesDropped event.
		EnableFileDrop: true,
	})
	// The facade never sees the window: a native file drop becomes the
	// typed files-dropped event, and the frontend starts the upload through
	// the Transfers facade like any picker-chosen upload.
	win.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		files := event.Context().DroppedFiles()
		if len(files) == 0 {
			return
		}
		app.Event.Emit(gui.EventFilesDropped, gui.FilesDropped{Paths: files})
	})
	// The facade watches the Omarchy theme; every change reaches the
	// frontend as the typed event.
	if svc.Settings.ThemeChanges != nil {
		go func() {
			for th := range svc.Settings.ThemeChanges {
				app.Event.Emit(gui.OmarchyThemeChangedEvent, th)
			}
		}()
	}
	return app.Run()
}
