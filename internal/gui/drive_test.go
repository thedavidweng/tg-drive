//go:build gui

package gui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
		if entries[i] != want[i] {
			t.Fatalf("List(/)[%d] = %+v, want %+v", i, entries[i], want[i])
		}
	}
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
