package service

import (
	"context"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// holdForeignLock takes a path's operation lock as another process would,
// for the rest of the test.
func holdForeignLock(t *testing.T, app *App, ctx context.Context, path string) {
	t.Helper()
	rowID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.DB.AcquireLock(ctx, sqlitestore.LockKey(rowID, path), "another-process", time.Minute); err != nil {
		t.Fatal(err)
	}
}

func wantOperationLocked(t *testing.T, err error) {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.ErrOperationLocked {
		t.Fatalf("want ERR_OPERATION_LOCKED, got %v", err)
	}
}

// adopt --rewrite-captions edits captions and deletes manifest replies of
// indexed files, so a concurrent operation on one of those paths makes it
// fail before anything is written.
func TestAdoptRewriteCaptionsHonorsPathLocks(t *testing.T) {
	t.Run("ungrouped caption", func(t *testing.T) {
		app, tg := testApp(t)
		loginAndInit(t, app, tg)
		ctx := context.Background()
		tgChID, _ := app.tgChannelID(ctx)
		tg.AddMessage(tgChID, telegram.Message{
			ID: 80, Kind: telegram.KindDocument, MIME: "video/mp4",
			FileName: "clip.mp4", FileSize: 4, Data: []byte("abcd"),
		})
		if _, err := app.Adopt(ctx, AdoptOptions{MessageID: 80, Dest: "/videos/clip.mp4", NoHash: true, Confirm: true}); err != nil {
			t.Fatal(err)
		}
		legacy := "clip.mp4\nvideos/\n\ntd:v1 p=x n=y\n#td_videos_xx"
		if err := tg.EditCaption(ctx, tgChID, 80, legacy); err != nil {
			t.Fatal(err)
		}
		holdForeignLock(t, app, ctx, "/videos/clip.mp4")

		_, err := app.Adopt(ctx, AdoptOptions{RewriteCaptions: true, Confirm: true})
		wantOperationLocked(t, err)
		got, _ := tg.GetMessage(ctx, tgChID, 80)
		if got.Caption != legacy {
			t.Fatalf("caption of a locked path rewritten: %q", got.Caption)
		}
	})

	t.Run("album captions and manifest replies", func(t *testing.T) {
		app, tg := testApp(t)
		loginAndInit(t, app, tg)
		ctx := context.Background()
		tgChID, _ := app.tgChannelID(ctx)
		for i, caption := range []string{"a.mp4", "#weekend\ndump"} {
			tg.AddMessage(tgChID, telegram.Message{
				ID: 300 + i, Kind: telegram.KindDocument, MIME: "video/mp4",
				FileName: string(rune('a'+i)) + ".mp4", FileSize: 4, Data: []byte("data"),
				Caption: caption, GroupedID: 9,
			})
		}
		if _, err := app.Adopt(ctx, AdoptOptions{Unmanaged: true, NoHash: true, Confirm: true}); err != nil {
			t.Fatal(err)
		}
		rt := 301
		tg.AddMessage(tgChID, telegram.Message{ID: 400, Text: "td-manifest:v1 p=x", ReplyTo: &rt})
		holdForeignLock(t, app, ctx, "/videos/b.mp4")

		_, err := app.Adopt(ctx, AdoptOptions{RewriteCaptions: true, Confirm: true})
		wantOperationLocked(t, err)
		if _, err := tg.GetMessage(ctx, tgChID, 400); err != nil {
			t.Fatalf("manifest reply of a locked path deleted: %v", err)
		}
		for id, want := range map[int]string{300: "a.mp4", 301: "#weekend\ndump"} {
			got, _ := tg.GetMessage(ctx, tgChID, id)
			if got.Caption != want {
				t.Fatalf("caption of album member %d rewritten under a lock: %q", id, got.Caption)
			}
		}
	})
}
