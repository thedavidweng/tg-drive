//go:build gui

package gui_test

import (
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/internal/gui"
)

// The tray and quit flows cannot run headless (no display, no tray
// applet), so their decisions live in pure seams pinned here: the tray
// menu built from the typed transfer-event stream, and the quit
// confirmation decision over the count of active Transfers.

func tr(id, kind, stage string) gui.Transfer {
	return gui.Transfer{
		ID: id, Kind: kind, Stage: stage,
		Source: "/home/me/big.bin", Dest: "/big.bin",
	}
}

func menuLabels(rows []gui.TrayRow) []string {
	labels := make([]string, 0, len(rows))
	for _, r := range rows {
		labels = append(labels, r.Label)
	}
	return labels
}

func TestTrayMenuEmptyShowsIdleShowAndQuit(t *testing.T) {
	tracker := gui.NewTrayTracker()
	rows := tracker.Menu()

	if len(rows) != 4 {
		t.Fatalf("rows = %v, want idle row, separator, show, quit", menuLabels(rows))
	}
	if rows[0].Action != gui.TrayActionNone || rows[0].Label != "No active transfers" {
		t.Errorf("row 0 = %+v, want the disabled idle row", rows[0])
	}
	if rows[1].Action != gui.TrayActionSeparator {
		t.Errorf("row 1 = %+v, want a separator", rows[1])
	}
	if rows[2].Action != gui.TrayActionShow || rows[2].Label != "Show td" {
		t.Errorf("row 2 = %+v, want Show td", rows[2])
	}
	if rows[3].Action != gui.TrayActionQuit || rows[3].Label != "Quit td" {
		t.Errorf("row 3 = %+v, want Quit td", rows[3])
	}
}

func TestTrayTrackerListsActiveTransfers(t *testing.T) {
	tracker := gui.NewTrayTracker()
	tracker.Apply(tr("1", "upload", "queued"))
	tracker.Apply(tr("2", "download", "downloading"))

	rows := tracker.Menu()
	if len(rows) != 5 {
		t.Fatalf("rows = %v, want two transfers, separator, show, quit", menuLabels(rows))
	}
	for _, row := range rows[:2] {
		if row.Action != gui.TrayActionNone {
			t.Errorf("transfer row = %+v, want a non-clickable status row", row)
		}
	}
	if !strings.Contains(rows[0].Label, "↑") || !strings.Contains(rows[0].Label, "big.bin") ||
		!strings.Contains(rows[0].Label, "queued") {
		t.Errorf("upload row = %q, want arrow, name, and stage", rows[0].Label)
	}
	if !strings.Contains(rows[1].Label, "↓") || !strings.Contains(rows[1].Label, "downloading") {
		t.Errorf("download row = %q, want arrow and stage", rows[1].Label)
	}
}

func TestTrayTrackerProgressUpdatesInPlace(t *testing.T) {
	tracker := gui.NewTrayTracker()
	tracker.Apply(tr("1", "upload", "uploading"))
	tracker.Apply(tr("2", "upload", "uploading"))

	p := tr("1", "upload", "uploading")
	p.BytesDone, p.BytesTotal = 420, 1000
	tracker.Apply(p)

	rows := tracker.Menu()
	if len(rows) != 5 {
		t.Fatalf("rows = %v, want the progress event to update, not add", menuLabels(rows))
	}
	if !strings.Contains(rows[0].Label, "42%") {
		t.Errorf("row 0 = %q, want the byte progress as a percentage", rows[0].Label)
	}
	if strings.Contains(rows[1].Label, "%") {
		t.Errorf("row 1 = %q, want no percentage while the total is unknown", rows[1].Label)
	}
}

func TestTrayTrackerCountsItemsForMultiItemTransfers(t *testing.T) {
	tracker := gui.NewTrayTracker()
	album := tr("1", "album_upload", "uploading")
	album.Dest = "/photos/"
	album.ItemsDone, album.ItemsTotal = 3, 7
	tracker.Apply(album)

	row := tracker.Menu()[0]
	if !strings.Contains(row.Label, "3/7") {
		t.Errorf("album row = %q, want the item counts", row.Label)
	}
	if !strings.Contains(row.Label, "photos") {
		t.Errorf("album row = %q, want the destination directory's name", row.Label)
	}
}

func TestTrayTrackerNamesTheRootDestination(t *testing.T) {
	tracker := gui.NewTrayTracker()
	album := tr("1", "album_upload", "uploading")
	album.Dest = "/"
	tracker.Apply(album)

	if row := tracker.Menu()[0]; !strings.Contains(row.Label, "/") {
		t.Errorf("album row = %q, want the root directory named", row.Label)
	}
}

func TestTrayTrackerDropsTerminalAndRemovedTransfers(t *testing.T) {
	tracker := gui.NewTrayTracker()
	tracker.Apply(tr("1", "upload", "uploading"))
	tracker.Apply(tr("2", "upload", "uploading"))

	done := tr("1", "upload", "completed")
	tracker.Apply(done)
	tracker.Remove("2")

	rows := tracker.Menu()
	if len(rows) != 4 || rows[0].Label != "No active transfers" {
		t.Fatalf("rows = %v, want the idle menu once nothing runs", menuLabels(rows))
	}
}

func TestTrayTrackerKeepsFirstSeenOrder(t *testing.T) {
	tracker := gui.NewTrayTracker()
	tracker.Apply(tr("1", "upload", "uploading"))
	tracker.Apply(tr("2", "upload", "uploading"))
	tracker.Apply(tr("1", "upload", "publishing"))

	rows := tracker.Menu()
	if !strings.Contains(rows[0].Label, "publishing") {
		t.Errorf("row 0 = %q, want transfer 1 first and updated", rows[0].Label)
	}
}

func TestDecideQuit(t *testing.T) {
	if gui.DecideQuit(0) != gui.QuitImmediately {
		t.Error("no active Transfers: quitting must not ask")
	}
	if gui.DecideQuit(1) != gui.QuitAsk {
		t.Error("one active Transfer: quitting must ask")
	}
	if gui.DecideQuit(5) != gui.QuitAsk {
		t.Error("five active Transfers: quitting must ask")
	}
}

func TestQuitMessage(t *testing.T) {
	if one := gui.QuitMessage(1); !strings.Contains(one, "1 transfer is") || !strings.Contains(one, "it") {
		t.Errorf("QuitMessage(1) = %q, want singular", one)
	}
	if many := gui.QuitMessage(3); !strings.Contains(many, "3 transfers are") || !strings.Contains(many, "them") {
		t.Errorf("QuitMessage(3) = %q, want plural", many)
	}
}
