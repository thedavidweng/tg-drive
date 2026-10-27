//go:build gui

package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thedavidweng/tg-drive/internal/service"
)

// openTestGUI seeds every path into a temp dir and opens the facade, the way
// cmd/td-gui does on a fresh machine.
func openTestGUI(t *testing.T) *Services {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TD_FAKE_TELEGRAM", "1")
	t.Setenv("TD_FAKE_TELEGRAM_STATE", filepath.Join(dir, "fake.json"))
	t.Setenv("TD_CONFIG", filepath.Join(dir, "config.toml"))
	t.Setenv("TD_DB", filepath.Join(dir, "td.db"))
	t.Setenv("TD_SESSION", filepath.Join(dir, "session.json"))
	svc, closeGUI, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeGUI)
	return svc
}

func TestSettingsSetConcurrentLiveKeys(t *testing.T) {
	svc := openTestGUI(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, change := range []struct{ key, value string }{
		{"upload.threads", "5"},
		{"upload.part_size_kb", "4096"},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Settings.Set(context.Background(), change.key, change.value)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg := svc.state.current().Cfg
	if cfg.Upload.Threads != 5 || cfg.Upload.PartSizeKB != 4096 {
		t.Fatalf("running upload config = %+v, want both live changes", cfg.Upload)
	}
	for key, want := range map[string]string{"upload.threads": "5", "upload.part_size_kb": "4096"} {
		saved, err := service.GetConfig(svc.Settings.opts, service.ConfigGetOptions{Key: key})
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(saved.Entries[0].Value); got != want {
			t.Fatalf("saved %s = %s, want %s", key, got, want)
		}
	}
}

// TestSettingsSetReachesTheRunningAppForLiveKeys: a key the GUI reads per
// call must be visible to the App later calls resolve, not only to the next
// start. Without reloadConfig the running App keeps the value it opened
// with, so this fails rather than merely restating that the file changed.
func TestSettingsSetReachesTheRunningAppForLiveKeys(t *testing.T) {
	svc := openTestGUI(t)
	ctx := context.Background()

	if got := svc.state.current().Cfg.Upload.PartSizeKB; got != 0 {
		t.Fatalf("part size before Set = %d, want the default 0", got)
	}
	entry, err := svc.Settings.Set(ctx, "upload.part_size_kb", "4096")
	if err != nil {
		t.Fatal(err)
	}
	if entry.RestartRequired {
		t.Fatalf("Set part size = %+v, want it applied without a restart", entry)
	}
	if got := svc.state.current().Cfg.Upload.PartSizeKB; got != 4096 {
		t.Fatalf("part size on the running App after Set = %d, want 4096", got)
	}
}

// TestSettingsSetLeavesStartupKeysOffTheRunningApp: a key the GUI opened at
// start with cannot reach the running App, so Set reports RestartRequired and
// the App keeps the value it opened with. A reload that pretended otherwise
// would hand later calls a config the client and database do not use.
func TestSettingsSetLeavesStartupKeysOffTheRunningApp(t *testing.T) {
	svc := openTestGUI(t)
	ctx := context.Background()

	before := svc.state.current().Cfg.Locks.SessionWaitSeconds
	entry, err := svc.Settings.Set(ctx, "locks.session_wait_seconds", "7")
	if err != nil {
		t.Fatal(err)
	}
	if !entry.RestartRequired {
		t.Fatalf("Set session wait = %+v, want RestartRequired", entry)
	}
	if got := svc.state.current().Cfg.Locks.SessionWaitSeconds; got != before {
		t.Fatalf("session wait on the running App = %d, want the value it opened with (%d)", got, before)
	}

	saved, err := service.GetConfig(svc.Settings.opts, service.ConfigGetOptions{Key: "locks.session_wait_seconds"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := saved.Entries[0].Value.(int); !ok || got != 7 {
		t.Fatalf("saved session wait = %v, want 7 on disk for the next start", saved.Entries[0].Value)
	}
}

// TestSettingsSetNeverLeaksPendingStartupValuesIntoTheRunningApp: startup
// keys saved for the next start stay on disk only. A live save afterwards must
// not carry them into the running App, whose client, database, and Session
// lock were opened with the values it started with. Reading the whole file
// back would do exactly that: the App would claim an api_id it never
// authenticated with, a session wait it never applied, and a database path
// that is not the open one.
func TestSettingsSetNeverLeaksPendingStartupValuesIntoTheRunningApp(t *testing.T) {
	svc := openTestGUI(t)
	ctx := context.Background()

	opened := svc.state.current().Cfg
	pendingDB := filepath.Join(t.TempDir(), "elsewhere.db")
	for _, pending := range []struct{ key, value string }{
		{"telegram.api_id", "999"},
		{"telegram.api_hash", "0123456789abcdef0123456789abcdef"},
		{"storage.db_path", pendingDB},
		{"locks.session_wait_seconds", "7"},
	} {
		entry, err := svc.Settings.Set(ctx, pending.key, pending.value)
		if err != nil {
			t.Fatalf("Set %s: %v", pending.key, err)
		}
		if !entry.RestartRequired {
			t.Fatalf("Set %s = %+v, want RestartRequired", pending.key, entry)
		}
	}

	if _, err := svc.Settings.Set(ctx, "upload.part_size_kb", "4096"); err != nil {
		t.Fatal(err)
	}

	running := svc.state.current().Cfg
	if running.Upload.PartSizeKB != 4096 {
		t.Fatalf("part size on the running App = %d, want the saved 4096", running.Upload.PartSizeKB)
	}
	if running.Telegram.APIID != opened.Telegram.APIID || running.Telegram.APIHash != opened.Telegram.APIHash {
		t.Fatalf("running App credentials = %d/%q, want the ones it opened with (%d/%q)",
			running.Telegram.APIID, running.Telegram.APIHash, opened.Telegram.APIID, opened.Telegram.APIHash)
	}
	if running.Storage.DBPath != opened.Storage.DBPath {
		t.Fatalf("running App database = %s, want the open one %s", running.Storage.DBPath, opened.Storage.DBPath)
	}
	if running.Locks.SessionWaitSeconds != opened.Locks.SessionWaitSeconds {
		t.Fatalf("running App session wait = %d, want the one it opened with (%d)",
			running.Locks.SessionWaitSeconds, opened.Locks.SessionWaitSeconds)
	}

	// The pending values are still on disk for the next start.
	for _, pending := range []struct{ key, value string }{
		{"telegram.api_id", "999"},
		{"locks.session_wait_seconds", "7"},
	} {
		saved, err := service.GetConfig(svc.Settings.opts, service.ConfigGetOptions{Key: pending.key})
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(saved.Entries[0].Value); got != pending.value {
			t.Fatalf("saved %s = %v, want %s kept for the next start", pending.key, got, pending.value)
		}
	}
	data, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), pendingDB) {
		t.Fatal("pending database path was lost from the config file")
	}
}

// TestSettingsSetReportsNothingSavedWhenValidationFails: a rejected value
// changes neither the file nor the running App, so a failed Set cannot leave a
// half-applied change behind.
func TestSettingsSetReportsNothingSavedWhenValidationFails(t *testing.T) {
	svc := openTestGUI(t)
	ctx := context.Background()

	before := svc.state.current().Cfg
	if _, err := svc.Settings.Set(ctx, "upload.threads", "0"); err == nil {
		t.Fatal("Set upload.threads 0 succeeded, want a validation error")
	}
	after := svc.state.current().Cfg
	if after.Upload.Threads != before.Upload.Threads || after.Storage.DBPath != before.Storage.DBPath ||
		after.Telegram.APIID != before.Telegram.APIID {
		t.Fatalf("running App config changed after a rejected Set:\n before %+v\n after  %+v", before, after)
	}
	saved, err := service.GetConfig(svc.Settings.opts, service.ConfigGetOptions{Key: "upload.threads"})
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Entries[0].Value; got != before.Upload.Threads {
		t.Fatalf("saved upload.threads = %v after a rejected Set, want %d", got, before.Upload.Threads)
	}
}
