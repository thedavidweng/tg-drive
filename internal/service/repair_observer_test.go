package service

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func assertObserved(t *testing.T, got *observed, want []string) {
	t.Helper()
	if !slices.Equal(got.all(), want) {
		t.Fatalf("observed:\n%v\nwant:\n%v", got.all(), want)
	}
}

// Pending repair reports each stale row once: one whose local source is
// still there goes through the upload stages again, and one whose source is
// gone fails.
func TestRepairPendingObserver(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	channelID, _, _ := app.channelID(ctx)
	for _, row := range []struct{ path, local string }{
		{"/crashed.txt", writeLocal(t, "x")},
		{"/gone.txt", filepath.Join(t.TempDir(), "gone.txt")},
	} {
		if _, err := app.DB.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,original_local_path,status,updated_at) values(?,?,?,?,'pending','2020-01-01T00:00:00Z')`,
			channelID, row.path, filepath.Base(row.path), row.local); err != nil {
			t.Fatal(err)
		}
	}

	var got observed
	if _, err := app.Repair(ctx, RepairOptions{Pending: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	assertObserved(t, &got, []string{
		"stage /crashed.txt hashing",
		"stage /crashed.txt uploading",
		"stage /crashed.txt publishing",
		"item /crashed.txt completed",
		"item /gone.txt failed err",
	})
}

// Orphan repair publishes the missing records of an upload that reached
// Telegram and reports it completed.
func TestRepairOrphanedObserver(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tg.SetFailReply(true)
	tg.SetFailDelete(true)
	_, _ = app.UploadFile(ctx, writeLocal(t, "payload"), "/o/h.bin", ConflictFail, false, UploadOptions{})
	tg.SetFailReply(false)
	tg.SetFailDelete(false)

	var got observed
	if _, err := app.Repair(ctx, RepairOptions{Orphaned: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	assertObserved(t, &got, []string{"stage /o/h.bin publishing", "item /o/h.bin completed"})
}

// Repairing one path rewrites its records and reports it completed.
func TestRepairPathObserver(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFile(ctx, writeLocal(t, "x"), "/a.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}

	var got observed
	if _, err := app.Repair(ctx, RepairOptions{Path: ptr("/a.bin"), Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	assertObserved(t, &got, []string{"stage /a.bin publishing", "item /a.bin completed"})
}

// Scan-error repair is a full scan and reports as one.
func TestRepairScanErrorsObserver(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFile(ctx, writeLocal(t, "x"), "/a.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}

	var got observed
	if _, err := app.Repair(ctx, RepairOptions{ScanErrors: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	assertObserved(t, &got, []string{"stage call reading", "stage call indexing", "item /a.bin completed"})
}

// Hash repair downloads each file without a hash, with byte progress, then
// publishes the hash into its records.
func TestRepairHashObserver(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if _, err := app.UploadFile(ctx, writeSized(t, t.TempDir(), "h.bin", 10), "/h.bin", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	stripHashes(t, app)

	var got observed
	if _, err := app.Repair(ctx, RepairOptions{Hash: true, Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	assertObserved(t, &got, []string{
		"stage /h.bin downloading",
		"progress /h.bin 10/10",
		"stage /h.bin publishing",
		"item /h.bin completed",
	})
}

// Caption repair reports a caption it rewrites as completed and one already
// clean as skipped; its dry run changes nothing and reports nothing.
func TestRepairCaptionsObserver(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	dirty, err := app.UploadFile(ctx, writeLocal(t, "a"), "/c/a.mp4", ConflictFail, false, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, writeLocal(t, "b"), "/c/b.mp4", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	channelID, _, _ := app.channelID(ctx)
	tgChannelID, _ := app.tgChannelID(ctx)
	if err := tg.EditCaption(ctx, tgChannelID, dirty.MessageID,
		oldRenderedCaption(t, app, channelID, "/c/a.mp4", "a.mp4", "title")); err != nil {
		t.Fatal(err)
	}

	var dry observed
	if _, err := app.Repair(ctx, RepairOptions{Captions: true, Path: ptr("/c"), DryRun: true, Observer: dry.observer()}); err != nil {
		t.Fatal(err)
	}
	assertObserved(t, &dry, nil)

	var got observed
	if _, err := app.Repair(ctx, RepairOptions{Captions: true, Path: ptr("/c"), Observer: got.observer()}); err != nil {
		t.Fatal(err)
	}
	assertObserved(t, &got, []string{
		"stage /c/a.mp4 publishing",
		"item /c/a.mp4 completed",
		"item /c/b.mp4 skipped",
	})
}
