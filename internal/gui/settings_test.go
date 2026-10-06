//go:build gui

package gui_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
	"github.com/thedavidweng/tg-drive/internal/version"
)

// seedConfig points every path at a temp dir and writes known config values
// through the service, the way `td config set` would.
func seedConfig(t *testing.T, values map[string]string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TD_FAKE_TELEGRAM", "1")
	t.Setenv("TD_FAKE_TELEGRAM_STATE", filepath.Join(dir, "fake.json"))
	t.Setenv("TD_CONFIG", filepath.Join(dir, "config.toml"))
	t.Setenv("TD_DB", filepath.Join(dir, "td.db"))
	t.Setenv("TD_SESSION", filepath.Join(dir, "session.json"))
	for k, v := range values {
		if _, err := service.SetConfig(service.Options{}, k, v); err != nil {
			t.Fatal(err)
		}
	}
}

func configEntry(t *testing.T, entries []gui.ConfigEntry, key string) gui.ConfigEntry {
	t.Helper()
	for _, e := range entries {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("no config entry %q in %+v", key, entries)
	return gui.ConfigEntry{}
}

func TestSettingsListsEveryKeyWithSecretsRedacted(t *testing.T) {
	seedConfig(t, map[string]string{
		"telegram.api_hash": "supersecret-hash",
		"telegram.phone":    "+15551234567",
	})
	svc := openGUI(t)

	entries, err := svc.Settings.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("List returned no config entries")
	}
	hash := configEntry(t, entries, "telegram.api_hash")
	if !hash.Secret || hash.Value != "redacted" {
		t.Fatalf("api_hash entry = %+v, want secret redacted", hash)
	}
	phone := configEntry(t, entries, "telegram.phone")
	if !phone.Secret || phone.Value == "+15551234567" {
		t.Fatalf("phone entry = %+v, want secret masked", phone)
	}
	if got := configEntry(t, entries, "transfers.concurrency").Value; got != int64(2) && got != 2 {
		t.Fatalf("transfers.concurrency = %v (%T), want the default 2", got, got)
	}
}

func TestSettingsRevealNeedsConfirmation(t *testing.T) {
	seedConfig(t, map[string]string{"telegram.api_hash": "supersecret-hash"})
	svc := openGUI(t)
	ctx := context.Background()

	_, err := svc.Settings.Reveal(ctx, "telegram.api_hash", false)
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Reveal without confirmation = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CONFIRMATION_REQUIRED" || guiErr.Category != "safety" {
		t.Fatalf("Reveal error = %+v, want ERR_CONFIRMATION_REQUIRED / safety", guiErr)
	}

	revealed, err := svc.Settings.Reveal(ctx, "telegram.api_hash", true)
	if err != nil {
		t.Fatal(err)
	}
	if revealed.Value != "supersecret-hash" || !revealed.Secret {
		t.Fatalf("revealed entry = %+v, want the stored hash", revealed)
	}
}

func TestSettingsSetRoundTrips(t *testing.T) {
	seedConfig(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	if _, err := svc.Settings.Set(ctx, "transfers.concurrency", "5"); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.Settings.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := configEntry(t, entries, "transfers.concurrency").Value; got != int64(5) && got != 5 {
		t.Fatalf("transfers.concurrency after set = %v, want 5", got)
	}

	_, err = svc.Settings.Set(ctx, "transfers.concurrency", "0")
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_CONFIG_INVALID" {
		t.Fatalf("Set invalid = %v, want ERR_CONFIG_INVALID", err)
	}
}

// TestSettingsVersionsReportsTheInstalledCLI builds a td binary stamped
// with its own version, the way the release does, and puts it on PATH: the
// td row reports that binary's version, not the GUI's stamp.
func TestSettingsVersionsReportsTheInstalledCLI(t *testing.T) {
	seedConfig(t, nil)
	bin := t.TempDir()
	name := "td"
	if runtime.GOOS == "windows" {
		name = "td.exe"
	}
	build := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-X github.com/thedavidweng/tg-drive/internal/version.Version=9.8.7",
		"-o", filepath.Join(bin, name), "github.com/thedavidweng/tg-drive/cmd/td")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build td: %v\n%s", err, out)
	}
	// Put the stamped CLI first while retaining Windows' DLL search paths.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	svc := openGUI(t)

	v := svc.Settings.Versions(context.Background())
	if v.GUI != version.Version || v.CLI != "9.8.7" {
		probe := exec.Command(filepath.Join(bin, name), "version", "--json")
		out, err := probe.CombinedOutput()
		t.Fatalf("Versions = %+v, want gui %q and the installed td's 9.8.7; direct probe: %v\n%s", v, version.Version, err, out)
	}
}

// TestSettingsVersionsWithoutACLI reports no td version when no td binary
// is installed, and ignores an unrelated program named td.
func TestSettingsVersionsWithoutACLI(t *testing.T) {
	seedConfig(t, nil)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", home)
	bin := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(filepath.Join(bin, "td"), []byte("#!/bin/sh\necho 'a todo list'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	svc := openGUI(t)

	v := svc.Settings.Versions(context.Background())
	if v.GUI != version.Version || v.CLI != "" {
		t.Fatalf("Versions = %+v, want gui %q and no td version", v, version.Version)
	}
}
