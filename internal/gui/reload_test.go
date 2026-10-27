//go:build gui

package gui

import (
	"context"
	"path/filepath"
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
