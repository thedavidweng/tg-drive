package service

import (
	"context"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// The ADR 0003 gates hold for every front end, so the service itself must
// refuse an unconfirmed destructive call and leave the drive untouched.

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok || ae.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func wantListed(t *testing.T, app *App, dir, name string) {
	t.Helper()
	entries, err := app.ListDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == name {
			return
		}
	}
	t.Fatalf("%s missing from %s: %+v", name, dir, entries)
}

func TestMoveRequiresConfirmation(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFile(ctx, writeLocal(t, "m"), "/gate/m.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, app.MoveFile(ctx, "/gate/m.txt", "/gate/moved.txt", MoveOptions{}), apperr.ErrConfirmationRequired)
	wantListed(t, app, "/gate", "m.txt")
}

func TestDeleteRequiresConfirmation(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFile(ctx, writeLocal(t, "d"), "/gate/d.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := app.DeleteFile(ctx, "/gate/d.txt", DeleteOptions{Tombstone: true})
	wantCode(t, err, apperr.ErrConfirmationRequired)
	wantListed(t, app, "/gate", "d.txt")
}

func TestReplaceUploadRequiresConfirmation(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFile(ctx, writeLocal(t, "original"), "/gate/r.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	replacement := writeLocal(t, "replacement bytes")
	_, err := app.UploadFile(ctx, replacement, "/gate/r.txt", ConflictReplace, false, UploadOptions{})
	wantCode(t, err, apperr.ErrConfirmationRequired)
	_, err = app.UploadFileAs(ctx, replacement, "/gate/r.txt", ConflictReplace, false, Presentation{}, UploadOptions{})
	wantCode(t, err, apperr.ErrConfirmationRequired)
	_, err = app.UploadFilesAs(ctx, writeLocals(t, 2), "/gate/", ConflictReplace, false, Presentation{}, UploadOptions{})
	wantCode(t, err, apperr.ErrConfirmationRequired)
	_, err = app.UploadRecursive(ctx, t.TempDir(), "/gate", ConflictReplace, false, false, false, UploadOptions{})
	wantCode(t, err, apperr.ErrConfirmationRequired)

	entries, err := app.ListDir(ctx, "/gate")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Size != int64(len("original")) {
		t.Fatalf("unconfirmed replace changed /gate: %+v", entries)
	}
}

func TestAdoptRequiresConfirmationOrDryRun(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 50, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "clip.mp4", FileSize: 5, Data: []byte("video"),
	})
	for _, opts := range []AdoptOptions{
		{Unmanaged: true, NoHash: true},
		{MessageID: 50, Dest: "/clip.mp4", NoHash: true},
		{RewriteCaptions: true},
	} {
		_, err := app.Adopt(ctx, opts)
		wantCode(t, err, apperr.ErrConfirmationRequired)
	}
	if entries, err := app.ListDir(ctx, "/"); err != nil || len(entries) != 0 {
		t.Fatalf("unconfirmed adopt indexed %+v (err %v)", entries, err)
	}
	if _, err := app.Adopt(ctx, AdoptOptions{Unmanaged: true, NoHash: true, DryRun: true}); err != nil {
		t.Fatalf("dry-run needs no confirmation: %v", err)
	}
}

func TestImportSavedRequiresConfirmation(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	seedSavedVideo(tg, 900, "trip", []byte("saved video"))
	for _, opts := range []ImportSavedOptions{
		{PhotosAs: PhotosAsDocument},
		{PhotosAs: PhotosAsDocument, DeleteSource: true},
		{PhotosAs: PhotosAsDocument, DeleteSource: true, DryRun: true},
	} {
		_, err := app.ImportSaved(ctx, opts)
		wantCode(t, err, apperr.ErrConfirmationRequired)
	}
	if entries, err := app.ListDir(ctx, "/"); err != nil || len(entries) != 0 {
		t.Fatalf("unconfirmed import published %+v (err %v)", entries, err)
	}
	if len(tg.SavedMessages()) != 1 {
		t.Fatal("unconfirmed import deleted the saved original")
	}
	if _, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument, DryRun: true}); err != nil {
		t.Fatalf("dry-run needs no confirmation: %v", err)
	}
}

func TestRepairDeleteOrphanedRequiresConfirmation(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tg.SetFailReply(true)
	tg.SetFailDelete(true)
	_, _ = app.UploadFile(ctx, writeLocal(t, "payload"), deepPath("o.bin"), ConflictFail, false, UploadOptions{})
	tg.SetFailReply(false)
	tg.SetFailDelete(false)

	_, err := app.Repair(ctx, RepairOptions{Orphaned: true, DeleteOrphaned: true})
	wantCode(t, err, apperr.ErrConfirmationRequired)
	tgChID, _ := app.tgChannelID(ctx)
	if n := len(tg.Messages(tgChID)); n != 1 {
		t.Fatalf("unconfirmed repair touched the orphan: %d messages remain", n)
	}
	if got := fileStatus(t, app, deepPath("o.bin")); got != "orphaned" {
		t.Fatalf("status = %q, want orphaned", got)
	}
}

func TestRepairRejectsMoreThanOneMode(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	for _, opts := range []RepairOptions{
		{Pending: true, Orphaned: true},
		{ScanErrors: true, Hash: true},
		{Hash: true, Captions: true, Pending: true},
	} {
		_, err := app.Repair(context.Background(), opts)
		wantCode(t, err, apperr.ErrUsage)
	}
}
