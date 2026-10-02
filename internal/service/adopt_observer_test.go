package service

import (
	"context"
	"slices"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

func seedUnmanaged(t *testing.T, app *App, tg interface {
	AddMessage(int64, telegram.Message) telegram.Message
},
) {
	t.Helper()
	tgChID, _ := app.tgChannelID(context.Background())
	tg.AddMessage(tgChID, telegram.Message{
		ID: 70, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "clip.mp4", FileSize: 9, Data: []byte("videodata"),
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 72, Kind: telegram.KindText, MIME: "text/plain", Text: "shopping list\nmilk",
	})
}

// Adopting the unmanaged messages reports reading the history, then for
// each message its own stages and result: an already indexed message is
// skipped, a media message is downloaded for its hash before its index row
// is published, and the call ends publishing the machine records.
func TestAdoptObserverUnmanaged(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFile(ctx, writeSized(t, t.TempDir(), "up.bin", 10), "/up.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	seedUnmanaged(t, app, tg)

	var got observed
	if _, err := app.Adopt(ctx, AdoptOptions{Unmanaged: true, Confirm: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"stage call reading",
		"stage /notes/shopping list.txt publishing",
		"item /notes/shopping list.txt completed",
		"stage /videos/clip.mp4 downloading",
		"progress /videos/clip.mp4 9/9",
		"stage /videos/clip.mp4 publishing",
		"item /videos/clip.mp4 completed",
		"item /files/up.bin skipped",
		"stage call publishing",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}

// A dry run changes nothing, so it reports only that it read the history.
func TestAdoptObserverDryRun(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	seedUnmanaged(t, app, tg)

	var got observed
	if _, err := app.Adopt(context.Background(), AdoptOptions{Unmanaged: true, DryRun: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"stage call reading"}; !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}

// Rewriting captions reports reading the history, each per-file reply it
// deletes and each album member caption it rewrites, then publishing the
// machine records.
func TestAdoptObserverRewriteCaptions(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	for _, m := range []struct {
		id            int
		name, caption string
	}{{300, "a.mp4", "a.mp4"}, {301, "b.mp4", "#tag\nweekend dump"}, {302, "c.mp4", "c.mp4"}} {
		tg.AddMessage(tgChID, telegram.Message{
			ID: m.id, Kind: telegram.KindDocument, MIME: "video/mp4",
			FileName: m.name, FileSize: 4, Data: []byte("data"), Caption: m.caption, GroupedID: 9,
		})
	}
	if _, err := app.Adopt(ctx, AdoptOptions{Unmanaged: true, NoHash: true, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	for _, reply := range []int{400, 401} {
		media := reply - 100
		tg.AddMessage(tgChID, telegram.Message{ID: reply, Text: "td-manifest:v1 p=x", ReplyTo: &media})
	}

	var got observed
	if _, err := app.Adopt(ctx, AdoptOptions{RewriteCaptions: true, Confirm: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"stage call reading",
		"stage msg 401 publishing",
		"item msg 401 completed",
		"stage msg 400 publishing",
		"item msg 400 completed",
		"stage /videos/a.mp4 publishing",
		"item /videos/a.mp4 completed",
		"stage /videos/b.mp4 publishing",
		"item /videos/b.mp4 completed",
		"stage /videos/c.mp4 publishing",
		"item /videos/c.mp4 completed",
		"stage call publishing",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}
