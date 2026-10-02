//go:build gui

package gui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/internal/gui"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// seedDrive sets up a bound drive the way the CLI would (its own session,
// the persistent fake Telegram) and uploads files into it, so the GUI opens
// an index it did not write.
func seedDrive(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TD_FAKE_TELEGRAM", "1")
	t.Setenv("TD_FAKE_TELEGRAM_STATE", filepath.Join(dir, "fake.json"))
	t.Setenv("TD_CONFIG", filepath.Join(dir, "config.toml"))
	t.Setenv("TD_DB", filepath.Join(dir, "td.db"))
	t.Setenv("TD_SESSION", filepath.Join(dir, "session.json"))
	t.Setenv("TD_API_ID", "1")
	t.Setenv("TD_API_HASH", "hash")
	t.Setenv("TD_PHONE", "+1000")
	ctx := context.Background()

	app, closeApp, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	if _, err := app.AuthLogin(ctx,
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.InitRoot(ctx, t.TempDir(), "", "Drive", ""); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	for remote, body := range files {
		local := filepath.Join(src, filepath.Base(remote))
		if err := os.WriteFile(local, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := app.UploadFile(ctx, local, remote, service.ConflictFail, false, service.UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func openGUI(t *testing.T) *gui.Services {
	t.Helper()
	svc, closeGUI, err := gui.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeGUI)
	return svc
}

func TestDriveListsSeededDirectory(t *testing.T) {
	seedDrive(t, map[string]string{
		"/notes.txt":        "hello",
		"/photos/cat.jpg":   "meow",
		"/photos/dog.jpg":   "woof",
		"/zeta/archive.zip": "zip",
	})
	svc := openGUI(t)

	entries, err := svc.Drive.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	want := []gui.Entry{
		{Name: "photos", Path: "/photos", Type: "dir"},
		{Name: "zeta", Path: "/zeta", Type: "dir"},
		{Name: "notes.txt", Path: "/notes.txt", Type: "file", Size: 5},
	}
	if len(entries) != len(want) {
		t.Fatalf("List(/) = %+v, want %+v", entries, want)
	}
	for i := range want {
		got := entries[i]
		got.Date = "" // covered by TestDriveListEntriesCarryADate
		if got != want[i] {
			t.Fatalf("List(/)[%d] = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

func TestDriveMkdirCreatesListableDirectory(t *testing.T) {
	seedDrive(t, map[string]string{"/photos/cat.jpg": "meow"})
	svc := openGUI(t)
	ctx := context.Background()

	if err := svc.Drive.Mkdir(ctx, "/photos/2024/summer"); err != nil {
		t.Fatal(err)
	}

	entries, err := svc.Drive.List(ctx, "/photos")
	if err != nil {
		t.Fatal(err)
	}
	want := []gui.Entry{
		{Name: "2024", Path: "/photos/2024", Type: "dir"},
		{Name: "cat.jpg", Path: "/photos/cat.jpg", Type: "file", Size: 4},
	}
	if len(entries) != len(want) {
		t.Fatalf("List(/photos) = %+v, want %+v", entries, want)
	}
	for i := range want {
		if entries[i].Name != want[i].Name || entries[i].Path != want[i].Path ||
			entries[i].Type != want[i].Type || entries[i].Size != want[i].Size {
			t.Fatalf("List(/photos)[%d] = %+v, want %+v", i, entries[i], want[i])
		}
	}
	// The nested mkdir created the intermediate directory too.
	mid, err := svc.Drive.List(ctx, "/photos/2024")
	if err != nil {
		t.Fatal(err)
	}
	if len(mid) != 1 || mid[0].Name != "summer" || mid[0].Type != "dir" {
		t.Fatalf("List(/photos/2024) = %+v, want the summer directory", mid)
	}
}

func TestDriveMkdirRejectsExistingAndFileConflicts(t *testing.T) {
	seedDrive(t, map[string]string{"/photos/cat.jpg": "meow"})
	svc := openGUI(t)
	ctx := context.Background()

	for _, path := range []string{"/photos", "/photos/cat.jpg", "/photos/cat.jpg/kittens"} {
		err := svc.Drive.Mkdir(ctx, path)
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) {
			t.Fatalf("Mkdir(%s) error = %T %v, want *gui.Error", path, err, err)
		}
		if guiErr.Category != "validation" {
			t.Fatalf("Mkdir(%s) error = %+v, want a validation error", path, guiErr)
		}
	}
}

func TestDriveMoveRequiresConfirmation(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)

	err := svc.Drive.Move(context.Background(), "/notes.txt", "/renamed.txt", gui.MoveOptions{})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Move error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CONFIRMATION_REQUIRED" || guiErr.Category != "safety" {
		t.Fatalf("Move error = %+v, want ERR_CONFIRMATION_REQUIRED / safety", guiErr)
	}
	// The rejected move changed nothing.
	entries, lerr := svc.Drive.List(context.Background(), "/")
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(entries) != 1 || entries[0].Name != "notes.txt" {
		t.Fatalf("List(/) = %+v, want notes.txt still in place", entries)
	}
}

func TestDriveMoveRenamesAFile(t *testing.T) {
	seedDrive(t, map[string]string{
		"/notes.txt":      "hello",
		"/photos/cat.jpg": "meow",
	})
	svc := openGUI(t)
	ctx := context.Background()

	if err := svc.Drive.Move(ctx, "/notes.txt", "/photos/readme.txt", gui.MoveOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.Drive.List(ctx, "/photos")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "cat.jpg" || entries[1].Name != "readme.txt" {
		t.Fatalf("List(/photos) = %+v, want cat.jpg and readme.txt", entries)
	}
}

func TestDriveDeleteRequiresConfirmation(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)

	_, err := svc.Drive.Delete(context.Background(), "/notes.txt", gui.DeleteOptions{})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Delete error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CONFIRMATION_REQUIRED" || guiErr.Category != "safety" {
		t.Fatalf("Delete error = %+v, want ERR_CONFIRMATION_REQUIRED / safety", guiErr)
	}
	entries, lerr := svc.Drive.List(context.Background(), "/")
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(entries) != 1 || entries[0].Name != "notes.txt" {
		t.Fatalf("List(/) = %+v, want notes.txt still in place", entries)
	}
}

func TestDriveDeleteRemovesAFile(t *testing.T) {
	seedDrive(t, map[string]string{
		"/notes.txt":      "hello",
		"/photos/cat.jpg": "meow",
	})
	svc := openGUI(t)
	ctx := context.Background()

	out, err := svc.Drive.Delete(ctx, "/notes.txt", gui.DeleteOptions{Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.Path != "/notes.txt" || out.Mode == "" {
		t.Fatalf("Delete = %+v, want the deleted path and its mode", out)
	}
	entries, err := svc.Drive.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "photos" {
		t.Fatalf("List(/) = %+v, want only photos left", entries)
	}
}

func TestDriveShareReturnsTheChannelInviteLink(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)

	link, err := svc.Drive.Share(context.Background(), "/notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if link == nil || link.URL == "" || link.Path != "/notes.txt" || link.Channel == "" {
		t.Fatalf("Share = %+v, want the invite link, path, and channel title", link)
	}
}

func TestDriveTreeRendersTheNestedStructure(t *testing.T) {
	seedDrive(t, map[string]string{
		"/notes.txt":             "hello",
		"/photos/cat.jpg":        "meow",
		"/photos/2024/beach.jpg": "sun",
	})
	svc := openGUI(t)

	tree, err := svc.Drive.Tree(context.Background(), "/", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 {
		t.Fatalf("Tree(/) = %+v, want notes.txt and photos", tree)
	}
	var photos *gui.TreeNode
	for i := range tree {
		if tree[i].Name == "photos" {
			photos = &tree[i]
		}
	}
	if photos == nil || photos.Type != "dir" || len(photos.Children) != 2 {
		t.Fatalf("Tree(/) photos = %+v, want a dir with two children", photos)
	}
	var sub *gui.TreeNode
	for i := range photos.Children {
		if photos.Children[i].Name == "2024" {
			sub = &photos.Children[i]
		}
	}
	if sub == nil || len(sub.Children) != 1 || sub.Children[0].Name != "beach.jpg" || sub.Children[0].Type != "file" {
		t.Fatalf("Tree(/) photos/2024 = %+v, want beach.jpg nested inside", sub)
	}
}

func TestDriveListEntriesCarryADate(t *testing.T) {
	seedDrive(t, map[string]string{
		"/notes.txt":      "hello",
		"/photos/cat.jpg": "meow",
	})
	svc := openGUI(t)

	entries, err := svc.Drive.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("List(/) = %+v, want photos and notes.txt", entries)
	}
	for _, e := range entries {
		if e.Date == "" {
			t.Fatalf("List(/) entry %+v has no date", e)
		}
		if _, perr := time.Parse(time.RFC3339, e.Date); perr != nil {
			t.Fatalf("List(/) entry %+v date is not RFC3339: %v", e, perr)
		}
	}
}

// Listing a file path itself (Unix-ls style) goes through a different query
// than listing a directory; it carries a date too.
func TestDriveListingAFilePathCarriesADate(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)

	entries, err := svc.Drive.List(context.Background(), "/notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "notes.txt" || entries[0].Type != "file" {
		t.Fatalf("List(/notes.txt) = %+v, want the file itself", entries)
	}
	if _, perr := time.Parse(time.RFC3339, entries[0].Date); perr != nil {
		t.Fatalf("List(/notes.txt) date is not RFC3339: %q (%v)", entries[0].Date, perr)
	}
}

// eventRecorder is the test Emitter: it records every typed event the
// facade emits.
type eventRecorder struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	name string
	data any
}

func (r *eventRecorder) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{name, data})
}

// scanProgresses returns the recorded scan-progress events in order.
func (r *eventRecorder) scanProgresses() []gui.ScanProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []gui.ScanProgress
	for _, e := range r.events {
		if e.name == gui.EventScanProgress {
			out = append(out, e.data.(gui.ScanProgress))
		}
	}
	return out
}

func TestDriveScanReportsProgressAndOutcome(t *testing.T) {
	seedDrive(t, map[string]string{
		"/a.txt": "aaa",
		"/b.txt": "bbb",
	})
	svc := openGUI(t)
	rec := &eventRecorder{}
	svc.SetDriveEmitter(rec.emit)

	out, err := svc.Drive.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "full" || out.Active != 2 {
		t.Fatalf("Scan = %+v, want a full scan with 2 active files", out)
	}
	events := rec.scanProgresses()
	if len(events) == 0 {
		t.Fatal("Scan emitted no progress events")
	}
	if events[0].Stage != "reading" {
		t.Fatalf("first scan progress = %+v, want the reading stage", events[0])
	}
	last := events[len(events)-1]
	if last.Stage != "indexing" || last.Indexed != 2 || last.Failed != 0 {
		t.Fatalf("last scan progress = %+v, want indexing with 2 indexed, 0 failed", last)
	}
}

// directoryChanges returns the recorded directory-changed events in order.
func (r *eventRecorder) directoryChanges() []gui.DirectoryChanged {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []gui.DirectoryChanged
	for _, e := range r.events {
		if e.name == gui.EventDirectoryChanged {
			out = append(out, e.data.(gui.DirectoryChanged))
		}
	}
	return out
}

// waitForListing polls until a directory-changed event for path satisfies
// want, or fails after a generous deadline.
func waitForListing(t *testing.T, rec *eventRecorder, path string, want func(names []string) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, dc := range rec.directoryChanges() {
			if dc.Path != path {
				continue
			}
			names := make([]string, 0, len(dc.Entries))
			for _, e := range dc.Entries {
				names = append(names, e.Name)
			}
			if want(names) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no directory-changed event for %s satisfied the expectation; events: %+v", path, rec.directoryChanges())
}

// A CLI process sharing the index uploads, moves, and deletes; the GUI's
// index sync notices through the data_version poll and re-reads the shown
// directory each time (ADR 0033).
func TestDriveSyncEmitsDirectoryChangedWhenTheCLIWrites(t *testing.T) {
	seedDrive(t, map[string]string{"/a.txt": "aaa"})
	svc := openGUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if _, err := svc.Drive.List(ctx, "/"); err != nil {
		t.Fatal(err)
	}
	rec := &eventRecorder{}
	svc.SetDriveEmitter(rec.emit)
	svc.StartSync(ctx, 20*time.Millisecond)

	// The CLI front end: the same index, its own session.
	cli, closeCLI, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeCLI()

	src := filepath.Join(t.TempDir(), "b.txt")
	if err := os.WriteFile(src, []byte("bbb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.UploadFile(ctx, src, "/b.txt", service.ConflictFail, false, service.UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForListing(t, rec, "/", func(names []string) bool {
		return slices.Contains(names, "b.txt")
	})

	if err := cli.MoveFile(ctx, "/b.txt", "/c.txt", service.MoveOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	waitForListing(t, rec, "/", func(names []string) bool {
		return slices.Contains(names, "c.txt") && !slices.Contains(names, "b.txt")
	})

	if _, err := cli.DeleteFile(ctx, "/c.txt", service.DeleteOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	waitForListing(t, rec, "/", func(names []string) bool {
		return !slices.Contains(names, "c.txt")
	})
}

// Auth reopening the App (setup/login) closes the old pool and with it the
// poller's pinned connection; index sync re-pins on the new App and keeps
// reporting CLI writes.
func TestDriveSyncSurvivesAnAuthReopen(t *testing.T) {
	seedDrive(t, map[string]string{"/a.txt": "aaa"})
	svc := openGUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if _, err := svc.Drive.List(ctx, "/"); err != nil {
		t.Fatal(err)
	}
	rec := &eventRecorder{}
	svc.SetDriveEmitter(rec.emit)
	svc.StartSync(ctx, 20*time.Millisecond)

	// Setup rewrites the Telegram config and reopens the App, closing the
	// pool the poller pinned.
	if _, err := svc.Auth.Setup(ctx, "1", "hash", "+1000"); err != nil {
		t.Fatal(err)
	}

	cli, closeCLI, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeCLI()
	src := filepath.Join(t.TempDir(), "b.txt")
	if err := os.WriteFile(src, []byte("bbb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.UploadFile(ctx, src, "/b.txt", service.ConflictFail, false, service.UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForListing(t, rec, "/", func(names []string) bool {
		return slices.Contains(names, "b.txt")
	})
}

func TestDriveListMapsServiceErrors(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)

	_, err := svc.Drive.List(context.Background(), "photos/../..")
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("List error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_PATH_INVALID" || guiErr.Category != "validation" || guiErr.Message == "" {
		t.Fatalf("List error = %+v, want ERR_PATH_INVALID / validation with a message", guiErr)
	}
}
