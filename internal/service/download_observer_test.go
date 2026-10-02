package service

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// A single-file download reports the downloading stage, byte progress up to
// the file's size, and one completed item.
func TestDownloadObserverSingleFile(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeSized(t, t.TempDir(), "a.bin", 2500)
	if _, err := app.UploadFile(ctx, local, "/dl/a.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}

	var got observed
	dest := filepath.Join(t.TempDir(), "a.bin")
	if _, err := app.DownloadFile(ctx, "/dl/a.bin", dest, ConflictFail, DownloadOptions{Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"stage /dl/a.bin downloading",
		"progress /dl/a.bin 2500/2500",
		"item /dl/a.bin completed",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}

// A recursive download with --continue-on-error reports every file once: a
// file whose local destination already exists fails, the rest complete with
// byte progress, each under the local path it was written to.
func TestDownloadObserverRecursive(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	src := t.TempDir()
	for name, size := range map[string]int{"a.bin": 10, "c.bin": 30} {
		if _, err := app.UploadFile(ctx, writeSized(t, src, name, size), "/tree/"+name, ConflictFail, false, UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.UploadFile(ctx, writeSized(t, src, "b.bin", 20), "/tree/sub/b.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	writeSized(t, dest, "c.bin", 1)

	var got observed
	res, err := app.DownloadRecursive(ctx, "/tree", dest, ConflictFail, true, DownloadOptions{Observer: got.observer()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Downloaded != 2 || res.Failed != 1 {
		t.Fatalf("result = %+v, want 2 downloaded, 1 failed", res)
	}
	// Listings put directories first, so the walk descends into sub first.
	want := []string{
		"stage /tree/sub/b.bin downloading",
		"progress /tree/sub/b.bin 20/20",
		"item /tree/sub/b.bin completed",
		"stage /tree/a.bin downloading",
		"progress /tree/a.bin 10/10",
		"item /tree/a.bin completed",
		"item /tree/c.bin failed err",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
	sources := got.sources()
	for path, local := range map[string]string{
		"/tree/a.bin":     filepath.Join(dest, "a.bin"),
		"/tree/c.bin":     filepath.Join(dest, "c.bin"),
		"/tree/sub/b.bin": filepath.Join(dest, "sub", "b.bin"),
	} {
		if sources[path] != local {
			t.Fatalf("%s reported local %q, want %q", path, sources[path], local)
		}
	}
}

// A download --skip-existing finds in place reports one skipped item and
// moves no bytes.
func TestDownloadObserverSkip(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFile(ctx, writeSized(t, t.TempDir(), "a.bin", 10), "/a.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	writeSized(t, dest, "a.bin", 1)

	var got observed
	if _, err := app.DownloadFile(ctx, "/a.bin", dest+"/", ConflictSkip, DownloadOptions{Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"item /a.bin skipped"}; !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
	if local := got.sources()["/a.bin"]; local != filepath.Join(dest, "a.bin") {
		t.Fatalf("reported local %q, want %q", local, filepath.Join(dest, "a.bin"))
	}
}

// Telegram re-encodes native photos, so a photo download cannot know its
// size in advance: progress counts the bytes with no total.
func TestDownloadObserverNativePhotoHasNoTotal(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFileAs(ctx, writeSized(t, t.TempDir(), "p.jpg", 6), "/p.jpg", ConflictFail, false,
		Presentation{Kind: telegram.KindPhoto}, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	var got observed
	if _, err := app.DownloadFile(ctx, "/p.jpg", filepath.Join(t.TempDir(), "p.jpg"), ConflictFail, DownloadOptions{Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	want := []string{"stage /p.jpg downloading", "progress /p.jpg 6/0", "item /p.jpg completed"}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}
