package service

import (
	"context"
	"path/filepath"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

func openOptions(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		ConfigPath:  filepath.Join(dir, "config.toml"),
		DBPath:      filepath.Join(dir, "td.db"),
		SessionPath: filepath.Join(dir, "session.json"),
	}
}

func clearTelegramEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TD_FAKE_TELEGRAM", "TD_FAKE_TELEGRAM_STATE", "TD_API_ID", "TD_API_HASH", "TD_PHONE", "TD_WAIT"} {
		t.Setenv(k, "")
	}
}

// A drive set up through one Open is visible to the next: the config, the
// database, and the (persistent fake) Telegram client are all wired from the
// same options, and closing releases them for the next process.
func TestOpenWiresConfigDatabaseAndTelegram(t *testing.T) {
	clearTelegramEnv(t)
	opts := openOptions(t)
	t.Setenv("TD_FAKE_TELEGRAM", "1")
	t.Setenv("TD_FAKE_TELEGRAM_STATE", filepath.Join(t.TempDir(), "fake.json"))
	t.Setenv("TD_API_ID", "1")
	t.Setenv("TD_API_HASH", "hash")
	t.Setenv("TD_PHONE", "+1000")
	ctx := context.Background()

	app, closeApp, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.AuthLogin(ctx,
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.InitRoot(ctx, t.TempDir(), "", "Drive", ""); err != nil {
		t.Fatal(err)
	}
	closeApp()

	app, closeApp, err = Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	status, err := app.AuthStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Authenticated {
		t.Fatal("second Open lost the fake Telegram session")
	}
	st, err := app.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Initialized || st.Channel == nil || st.Channel.Title != "Drive" {
		t.Fatalf("second Open lost the channel binding: %+v", st)
	}
}

// Without Telegram credentials a real client cannot be opened, but local-only
// work still opens the database.
func TestOpenWithoutCredentials(t *testing.T) {
	clearTelegramEnv(t)
	opts := openOptions(t)

	if _, _, err := Open(opts); !isCode(err, apperr.ErrConfigMissing) {
		t.Fatalf("Open without credentials: err = %v, want %s", err, apperr.ErrConfigMissing)
	}

	opts.Offline = true
	app, closeApp, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	res, err := app.PathCodecDoctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.DBCheck != "pass" {
		t.Fatalf("path-codec db check = %+v", res)
	}
}

func isCode(err error, code string) bool {
	ae, ok := apperr.As(err)
	return ok && ae.Code == code
}
