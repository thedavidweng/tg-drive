package service

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// A full scan reports reading history and then indexing for the call as a
// whole, a failed item for each scan error it records, and a completed item
// for each file it indexes.
func TestScanObserver(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	dir := t.TempDir()
	for _, name := range []string{"a.bin", "b.bin"} {
		if _, err := app.UploadFile(ctx, writeSized(t, dir, name, 10), "/s/"+name, ConflictFail, false, UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	tgChID, _ := app.tgChannelID(ctx)
	orphan := addAlbumMember(t, tg, tgChID, 7001, "x.jpg")

	var got observed
	if _, err := app.Scan(ctx, ScanOptions{Full: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"stage call reading",
		"stage call indexing",
		fmt.Sprintf("item msg %d failed err", orphan.ID),
		"item /s/a.bin completed",
		"item /s/b.bin completed",
	}
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}
