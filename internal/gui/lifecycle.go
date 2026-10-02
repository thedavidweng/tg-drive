//go:build gui

package gui

import (
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/thedavidweng/tg-drive-cli/internal/transfer"
)

// Desktop-lifecycle decisions for cmd/td-gui: the tray menu built from the
// typed transfer events, and whether quitting asks for confirmation. They
// are pure seams because a display, a tray applet, and a second OS process
// are out of reach of headless tests; cmd/td-gui wires them to Wails.
//
// The tray menu text is English-only: the facade has no locale (the
// frontend owns i18n), and a native menu cannot follow the webview's
// language without a round trip per rebuild.

// TrayAction is what selecting a tray menu row does.
type TrayAction int

const (
	// TrayActionNone is a status row: it shows a Transfer's progress and
	// cannot be selected.
	TrayActionNone TrayAction = iota
	// TrayActionSeparator separates the Transfers from the app entries.
	TrayActionSeparator
	// TrayActionShow shows and focuses the main window.
	TrayActionShow
	// TrayActionQuit asks to quit (with confirmation while Transfers run).
	TrayActionQuit
)

// TrayRow is one row of the system tray menu model.
type TrayRow struct {
	Label  string
	Action TrayAction
}

// TrayTracker tracks the active Transfers the tray menu lists. It is fed
// by the same typed transfer-stage, transfer-progress, and
// transfer-removed events the Transfers tab receives (Services.SetTransferEmitter
// is the single emission point; cmd/td-gui fans out to both), so the menu
// and the tab never disagree.
type TrayTracker struct {
	mu sync.Mutex
	// order is the transfer IDs in first-seen order; byID holds each
	// active Transfer's latest snapshot. Terminal stages and removals drop
	// out of both.
	order []string
	byID  map[string]Transfer
}

func NewTrayTracker() *TrayTracker {
	return &TrayTracker{byID: map[string]Transfer{}}
}

// Apply folds a transfer-stage or transfer-progress snapshot into the
// tracked set: a running Transfer is added or updated, a terminal one
// drops out.
func (t *TrayTracker) Apply(tr Transfer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if transfer.Stage(tr.Stage).Terminal() {
		t.removeLocked(tr.ID)
		return
	}
	if _, known := t.byID[tr.ID]; !known {
		t.order = append(t.order, tr.ID)
	}
	t.byID[tr.ID] = tr
}

// Remove folds a transfer-removed event into the tracked set.
func (t *TrayTracker) Remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removeLocked(id)
}

func (t *TrayTracker) removeLocked(id string) {
	if _, known := t.byID[id]; !known {
		return
	}
	delete(t.byID, id)
	for i, other := range t.order {
		if other == id {
			t.order = append(t.order[:i], t.order[i+1:]...)
			return
		}
	}
}

// ActiveCount is how many Transfers the tracker believes are running.
func (t *TrayTracker) ActiveCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.order)
}

// Menu renders the tracked Transfers as the tray menu's rows: one status
// row per active Transfer (or the idle row), then Show and Quit.
func (t *TrayTracker) Menu() []TrayRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := make([]TrayRow, 0, len(t.order)+4)
	if len(t.order) == 0 {
		rows = append(rows, TrayRow{Label: "No active transfers", Action: TrayActionNone})
	}
	for _, id := range t.order {
		rows = append(rows, TrayRow{Label: trayTransferLabel(t.byID[id]), Action: TrayActionNone})
	}
	rows = append(rows,
		TrayRow{Action: TrayActionSeparator},
		TrayRow{Label: "Show td", Action: TrayActionShow},
		TrayRow{Label: "Quit td", Action: TrayActionQuit},
	)
	return rows
}

// trayTransferLabel renders one Transfer's status row: a direction arrow,
// the name a person recognizes, the stage, and the progress when it is
// known — "↑ big.bin · uploading · 42%", "↓ photos · downloading · 3/7".
func trayTransferLabel(tr Transfer) string {
	arrow := "↓"
	title := tr.Source
	if uploadKind(tr.Kind) {
		arrow = "↑"
		title = tr.Dest
	}
	label := fmt.Sprintf("%s %s · %s", arrow, baseName(title), tr.Stage)
	switch transfer.Kind(tr.Kind) {
	case transfer.KindUpload, transfer.KindDownload:
		// Single-file kinds count bytes; the total is unknown until the
		// size probe lands, so early rows show no percentage.
		if tr.BytesTotal > 0 {
			label += fmt.Sprintf(" · %d%%", (tr.BytesDone*100+tr.BytesTotal/2)/tr.BytesTotal)
		}
	default:
		// Multi-item kinds count files.
		if tr.ItemsTotal > 0 {
			label += fmt.Sprintf(" · %d/%d", tr.ItemsDone, tr.ItemsTotal)
		}
	}
	return label
}

// uploadKind reports whether the Transfer kind moves bytes up to Telegram.
func uploadKind(kind string) bool {
	switch transfer.Kind(kind) {
	case transfer.KindUpload, transfer.KindAlbumUpload, transfer.KindRecursiveUpload:
		return true
	}
	return false
}

// baseName is the trailing path element of a remote (slash-separated)
// path, falling back to the path itself when it names no file — an album
// or recursive upload's destination is a directory, possibly the root.
func baseName(p string) string {
	trimmed := strings.TrimRight(p, "/")
	if trimmed == "" {
		return "/"
	}
	return path.Base(trimmed)
}

// QuitDecision is what a quit request does.
type QuitDecision int

const (
	// QuitImmediately quits without asking: nothing is running.
	QuitImmediately QuitDecision = iota
	// QuitAsk asks for confirmation first: Transfers are still running and
	// quitting interrupts them.
	QuitAsk
)

// DecideQuit reports whether a quit request needs the user's confirmation.
func DecideQuit(activeTransfers int) QuitDecision {
	if activeTransfers <= 0 {
		return QuitImmediately
	}
	return QuitAsk
}

// QuitMessage is the quit-confirmation dialog's body.
func QuitMessage(activeTransfers int) string {
	if activeTransfers == 1 {
		return "1 transfer is still running. Quitting interrupts it."
	}
	return fmt.Sprintf("%d transfers are still running. Quitting interrupts them.", activeTransfers)
}
