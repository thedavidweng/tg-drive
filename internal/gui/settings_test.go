//go:build gui

package gui_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/internal/gui"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/version"
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

func TestSettingsVersionsReportsTheSharedModuleVersion(t *testing.T) {
	seedConfig(t, nil)
	svc := openGUI(t)

	v := svc.Settings.Versions(context.Background())
	if v.GUI != version.Version || v.CLI != version.Version {
		t.Fatalf("Versions = %+v, want both %q", v, version.Version)
	}
}
