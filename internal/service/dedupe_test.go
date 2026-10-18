package service

import (
	"context"
	"testing"
)

func TestFindDuplicatesScopesAndReportsUnhashedFiles(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	a := writeLocal(t, "same bytes")
	b := writeLocal(t, "same bytes")
	c := writeLocal(t, "different")
	if _, err := app.UploadFile(ctx, a, "/docs/a.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, b, "/docs/b.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, c, "/other/c.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Raw().Exec(`update files set content_hash='' where canonical_path='/other/c.txt'`); err != nil {
		t.Fatal(err)
	}

	result, err := app.FindDuplicates(ctx, "/docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Groups) != 1 || len(result.Groups[0].Files) != 2 {
		t.Fatalf("groups = %+v", result.Groups)
	}
	if result.HashedFiles != 2 || result.UnhashedFiles != 0 {
		t.Fatalf("hash counts = %+v", result)
	}
	want := result.Groups[0].Size
	if result.ReclaimableBytes != want {
		t.Fatalf("reclaimable = %d, want %d", result.ReclaimableBytes, want)
	}

	result, err = app.FindDuplicates(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if result.UnhashedFiles != 1 || len(result.Groups) != 1 {
		t.Fatalf("root audit = %+v", result)
	}
}

func TestFindDuplicatesRejectsInvalidPath(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	if _, err := app.FindDuplicates(context.Background(), "/invalid//path"); err == nil {
		t.Fatal("empty path segment should fail")
	}
}

func TestDeleteDuplicatesRetainsFirstPathAndRequiresConfirmation(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	a := writeLocal(t, "same bytes")
	b := writeLocal(t, "same bytes")
	if _, err := app.UploadFile(ctx, a, "/docs/a.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, b, "/docs/b.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteDuplicates(ctx, "/docs", DedupeDeleteOptions{}); err == nil {
		t.Fatal("unconfirmed cleanup should fail")
	}

	result, err := app.DeleteDuplicates(ctx, "/docs", DedupeDeleteOptions{Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Groups != 1 || result.Retained != 1 || result.Deleted != 1 {
		t.Fatalf("cleanup result = %+v", result)
	}
	if len(result.DeletedPaths) != 1 || result.DeletedPaths[0] != "/docs/b.txt" {
		t.Fatalf("deleted paths = %v", result.DeletedPaths)
	}
	audit, err := app.FindDuplicates(ctx, "/docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Groups) != 0 || audit.HashedFiles != 1 {
		t.Fatalf("post-cleanup audit = %+v", audit)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := app.DB.ActiveByPath(ctx, channelID, "/docs/b.txt"); err != nil || found {
		t.Fatalf("deleted row still active: found=%v err=%v", found, err)
	}
}
