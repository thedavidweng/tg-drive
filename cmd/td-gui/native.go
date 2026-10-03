//go:build gui && !server

package main

import (
	"context"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/thedavidweng/tg-drive/assets"
	"github.com/thedavidweng/tg-drive/internal/gui"
)

// desktop owns the native behaviours the headless server build stubs out
// (native_server.go): the system tray with its live Transfers menu,
// hiding the window to the tray on close, the quit confirmation while
// Transfers run, and focusing the running instance on a second launch.
// The decisions themselves live in internal/gui (TrayTracker, DecideQuit)
// so they are testable without a display.
type desktop struct {
	app     *application.App
	win     *application.WebviewWindow
	svc     *gui.Services
	tray    *application.SystemTray
	tracker *gui.TrayTracker

	// quitting lets a confirmed quit through the close hook and
	// ShouldQuit; confirming suppresses a second quit-confirmation dialog
	// while one is up.
	quitting   atomic.Bool
	confirming atomic.Bool
}

func newDesktop(app *application.App, win *application.WebviewWindow, svc *gui.Services) *desktop {
	d := &desktop{app: app, win: win, svc: svc, tracker: gui.NewTrayTracker()}

	// Closing the window hides it to the tray; Transfers run on, because
	// their context is the process's, not the window's. The hook runs
	// before Wails' own WindowClosing listener, so cancelling the event
	// keeps the window alive.
	win.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if d.quitting.Load() {
			return
		}
		event.Cancel()
		win.Hide()
	})

	// macOS: clicking the dock icon with the window hidden reopens it.
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		d.show()
	})

	d.tray = app.SystemTray.New()
	d.tray.SetIcon(assets.Icon)
	d.tray.SetTooltip("td")
	d.tray.OnClick(d.show)
	d.rebuildTrayMenu()
	return d
}

// show brings the main window back — from the tray, a minimize, a second
// launch, or the macOS dock's reopen — and focuses it.
func (d *desktop) show() {
	d.win.Show()
	d.win.UnMinimise()
	d.win.Focus()
}

// observeTransfer folds a typed transfer event into the tray menu. It runs
// on the facade's emitter goroutines; SetMenu marshals to the main thread.
func (d *desktop) observeTransfer(name string, data any) {
	switch name {
	case gui.EventTransferStage, gui.EventTransferProgress:
		tr, ok := data.(gui.Transfer)
		if !ok {
			return
		}
		d.tracker.Apply(tr)
	case gui.EventTransferRemoved:
		removed, ok := data.(gui.TransferRemoved)
		if !ok {
			return
		}
		d.tracker.Remove(removed.ID)
	default:
		return
	}
	d.rebuildTrayMenu()
}

// rebuildTrayMenu re-renders the tray menu from the tracker's model. The
// facade emits only on change, and the model's status rows change with
// every emission, so the menu is rebuilt as-is, without its own dedupe.
func (d *desktop) rebuildTrayMenu() {
	menu := d.app.NewMenu()
	for _, row := range d.tracker.Menu() {
		switch row.Action {
		case gui.TrayActionSeparator:
			menu.AddSeparator()
		case gui.TrayActionShow:
			menu.Add(row.Label).OnClick(func(*application.Context) { d.show() })
		case gui.TrayActionQuit:
			// The read behind the quit decision is I/O, so it must not run
			// on the menu's thread.
			menu.Add(row.Label).OnClick(func(*application.Context) { go d.requestQuit() })
		default:
			menu.Add(row.Label).SetEnabled(false)
		}
	}
	d.tray.SetMenu(menu)
}

// requestQuit runs the quit flow: with nothing running it quits outright;
// with Transfers running it asks, and only a confirmed quit goes through.
// The tray's Quit entry and the OS quit paths both land here — the latter
// by shouldQuit deflecting the first attempt.
func (d *desktop) requestQuit() {
	if d.quitting.Load() {
		return
	}
	// The index is the authority (a CLI front end's Transfers included);
	// the tracker's snapshot is the fallback when the read fails.
	active := d.tracker.ActiveCount()
	if list, err := d.svc.Transfers.List(context.Background()); err == nil && list != nil {
		active = len(list.Active)
	}
	if gui.DecideQuit(active) == gui.QuitImmediately {
		d.quit()
		return
	}
	if !d.confirming.CompareAndSwap(false, true) {
		return
	}
	dialog := d.app.Dialog.Question().
		SetTitle("Quit td?").
		SetMessage(gui.QuitMessage(active))
	keep := dialog.AddButton("Keep Running")
	keep.OnClick(func() { d.confirming.Store(false) })
	dialog.SetDefaultButton(keep)
	dialog.SetCancelButton(keep)
	dialog.AddButton("Quit").OnClick(func() {
		d.confirming.Store(false)
		d.quit()
	})
	dialog.Show()
}

// quit lets the close hook and ShouldQuit through and destroys the app.
// Transfers still running end interrupted in the index, resumable from
// either front end.
func (d *desktop) quit() {
	d.quitting.Store(true)
	d.app.Quit()
}

// shouldQuit is the application option every Wails quit path consults
// (Cmd+Q, the app menu, app.Quit). An unconfirmed quit deflects into the
// confirmation flow and cancels this attempt; the flow re-quits with the
// flag set when the user confirms — or when nothing runs.
func (d *desktop) shouldQuit() bool {
	if d.quitting.Load() {
		return true
	}
	go d.requestQuit()
	return false
}

// singleInstanceOptions locks the app to one instance per machine: the GUI
// holds one Telegram session, so a second launch focuses the running
// window instead of fighting over the session lock.
func singleInstanceOptions(show func()) *application.SingleInstanceOptions {
	return &application.SingleInstanceOptions{
		UniqueID: "com.thedavidweng.td-gui",
		OnSecondInstanceLaunch: func(application.SecondInstanceData) {
			show()
		},
	}
}
