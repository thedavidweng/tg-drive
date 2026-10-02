package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Before binding a root, a front end is offered the existing drive channels
// (never their discussion groups) and a title for a new channel named after
// the root directory.
func TestInitChoicesOffersChannelsAndTitle(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	root := filepath.Join(t.TempDir(), "Vacation")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := app.InitChoices(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultTitle != "Vacation" {
		t.Fatalf("default title = %q, want the root's name", got.DefaultTitle)
	}
	if len(got.Channels) != 1 || got.Channels[0].Title != "Test Channel" {
		t.Fatalf("channels = %+v, want only the bound drive channel", got.Channels)
	}
}

// A relative root such as "." is named after the directory it resolves to.
func TestDefaultChannelTitleResolvesRelativeRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Pictures")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if got := DefaultChannelTitle("."); got != "Pictures" {
		t.Fatalf("DefaultChannelTitle(.) = %q, want Pictures", got)
	}
	if got := DefaultChannelTitle("./"); got != "Pictures" {
		t.Fatalf("DefaultChannelTitle(./) = %q, want Pictures", got)
	}
}
