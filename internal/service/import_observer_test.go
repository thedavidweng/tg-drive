package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// An import reports reading the saved chat, then each saved message once:
// one it cannot import is skipped, a duplicate of a file already in the tree
// is downloaded and skipped, and a new one is downloaded and then goes
// through the upload stages to completed, all under its drive path.
func TestImportSavedObserver(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	dupe := filepath.Join(t.TempDir(), "shared.bin")
	if err := os.WriteFile(dupe, []byte("dupe-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, dupe, "/shared.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	tg.AddSavedMessage(telegram.Message{
		ID: 501, Kind: telegram.KindDocument, FileName: "shared.bin",
		MIME: "application/octet-stream", FileSize: 10, Data: []byte("dupe-bytes"),
	})
	tg.AddSavedMessage(telegram.Message{ID: 600})
	seedSavedVideo(tg, 1201, "clip", []byte("video-bytes"))

	var got observed
	if _, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument, Confirm: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"stage call reading",
		"item msg 600 skipped",
		"stage /saved/shared.bin downloading",
		"progress /saved/shared.bin 10/10",
		"item /saved/shared.bin skipped",
		"stage /saved/Trips/clip.mp4 downloading",
		"progress /saved/Trips/clip.mp4 11/11",
		"stage /saved/Trips/clip.mp4 hashing",
		"stage /saved/Trips/clip.mp4 uploading",
		"stage /saved/Trips/clip.mp4 publishing",
		"item /saved/Trips/clip.mp4 completed",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}

// A dry-run import changes nothing, so it reports only reading the saved
// chat.
func TestImportSavedObserverDryRun(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	seedSavedVideo(tg, 1201, "clip", []byte("video-bytes"))

	var got observed
	if _, err := app.ImportSaved(context.Background(), ImportSavedOptions{
		PhotosAs: PhotosAsDocument, DryRun: true, Observer: got.observer(),
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"stage call reading"}; !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}
