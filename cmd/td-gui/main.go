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

// windowsTitleBarTheme tints the Windows title bar to the page: background
// and title text as #RRGGBB, packed into Wails' 0x00BBGGRR.
func windowsTitleBarTheme(background, text uint32) *application.WindowTheme {
	pack := func(rgb uint32) *uint32 {
		bgr := (rgb&0xFF)<<16 | (rgb & 0xFF00) | (rgb>>16)&0xFF
		return &bgr
	}
	return &application.WindowTheme{
		TitleBarColour:  pack(background),
		TitleTextColour: pack(text),
	}
}

func run() error {
	svc, closeGUI, err := gui.Open()
	if err != nil {
		return err
	}
	defer closeGUI()

	// desk is assigned once the window exists; the app options' callbacks
	// (a second launch, an OS quit request) can only fire after app.Run,
	// so the indirection never observes nil in practice — the guards keep
	// that honest.
	var desk *desktop
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
		SingleInstance: singleInstanceOptions(func() {
			if desk != nil {
				desk.show()
			}
		}),
		ShouldQuit: func() bool {
			return desk == nil || desk.shouldQuit()
		},
		Mac: application.MacOptions{
			// The window's close hides to the tray instead of destroying
			// the window, so the app must not quit when the last window
			// closes; quitting is the tray menu's or the OS quit path's
			// job.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
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
		Mac: application.MacWindow{
			// The inset hidden title bar: the traffic lights float over
			// the frontend's header, which is the window's drag region
			// (--wails-draggable in index.css, padded on darwin).
			TitleBar: application.MacTitleBarHiddenInset,
		},
		Windows: application.WindowsWindow{
			// Tint the title bar to the page's background in both themes
			// (the window frame otherwise stays the OS default).
			CustomTheme: application.ThemeSettings{
				DarkModeActive:    windowsTitleBarTheme(0x1a1a1e, 0xededf1),
				DarkModeInactive:  windowsTitleBarTheme(0x1a1a1e, 0x8b8b94),
				LightModeActive:   windowsTitleBarTheme(0xf4f4f6, 0x1c1c21),
				LightModeInactive: windowsTitleBarTheme(0xf4f4f6, 0x85858d),
			},
		},
	})
	// desk exists before any emitter or the index sync can fire.
	desk = newDesktop(app, win, svc)
	svc.SetPromptEmitter(func(p gui.AuthPrompt) {
		app.Event.Emit(EventAuthPrompt, p)
	})
	svc.SetDriveEmitter(func(name string, data any) {
		app.Event.Emit(name, data)
	})
	svc.SetTransferEmitter(func(name string, data any) {
		app.Event.Emit(name, data)
		desk.observeTransfer(name, data)
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
