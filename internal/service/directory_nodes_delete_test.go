package service

import (
	"context"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func TestDirectoryDeleteRemovesLocalNodesInSelectedSubtree(t *testing.T) {
	for _, withFiles := range []bool{false, true} {
		name := "empty"
		if withFiles {
			name = "with_files"
		}
		t.Run(name, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			ctx := context.Background()
			for _, path := range []string{"/keep/dir_%/empty/nested", "/keep/dir_other", "/outside"} {
				if err := app.Mkdir(ctx, path); err != nil {
					t.Fatal(err)
				}
			}
			if withFiles {
				if _, err := app.UploadFile(ctx, writeLocal(t, "data"), "/keep/dir_%/file.txt", ConflictFail, false, UploadOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			second, err := app.InitRoot(ctx, t.TempDir(), "", "Other", "")
			if err != nil {
				t.Fatal(err)
			}
			other := WithLocalRoot(ctx, second.LocalRoot)
			if err := app.Mkdir(other, "/keep/dir_%/empty/nested"); err != nil {
				t.Fatal(err)
			}

			res, err := app.DeleteFile(ctx, "/keep/dir_%", DeleteOptions{Confirm: true})
			if err != nil {
				t.Fatal(err)
			}
			wantDeleted := 0
			if withFiles {
				wantDeleted = 1
			}
			if res.Deleted != wantDeleted || res.Remaining != 0 {
				t.Fatalf("delete result = %+v", res)
			}
			if _, err := app.ListDir(ctx, "/keep/dir_%"); appErrCode(t, err) != apperr.ErrRemoteNotFound {
				t.Fatalf("deleted directory is still visible: %v", err)
			}
			for _, path := range []string{"/keep", "/keep/dir_other", "/outside"} {
				if _, err := app.ListDir(ctx, path); err != nil {
					t.Fatalf("unrelated local directory %s: %v", path, err)
				}
			}
			if _, err := app.ListDir(other, "/keep/dir_%/empty/nested"); err != nil {
				t.Fatalf("other channel's local directory: %v", err)
			}
		})
	}
}

func TestDirectoryDeleteKeepsLocalNodesUntilRetryCompletes(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if err := app.Mkdir(ctx, "/batch/empty"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if _, err := app.UploadFile(ctx, writeLocal(t, name), "/batch/"+name, ConflictFail, false, UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	tg.SetFailDeleteAfterFirst(true)
	res, err := app.DeleteFile(ctx, "/batch", DeleteOptions{Confirm: true})
	if appErrCode(t, err) != apperr.ErrTelegramRPC || res.Deleted != 1 || res.Remaining != 2 {
		t.Fatalf("partial delete = %+v, %v", res, err)
	}
	if _, err := app.ListDir(ctx, "/batch/empty"); err != nil {
		t.Fatalf("partial deletion lost local directory: %v", err)
	}
	tg.SetFailDeleteAfterFirst(false)
	if _, err := app.DeleteFile(ctx, "/batch", DeleteOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ListDir(ctx, "/batch"); appErrCode(t, err) != apperr.ErrRemoteNotFound {
		t.Fatalf("retry left local directory visible: %v", err)
	}
}

func TestDirectoryDeleteRootRemovesLocalFolders(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	for _, path := range []string{"/first/nested", "/second"} {
		if err := app.Mkdir(ctx, path); err != nil {
			t.Fatal(err)
		}
	}
	res, err := app.DeleteFile(ctx, "/", DeleteOptions{Confirm: true})
	if err != nil || res.Deleted != 0 || res.Remaining != 0 {
		t.Fatalf("empty root delete = %+v, %v", res, err)
	}
	entries, err := app.ListDir(ctx, "/")
	if err != nil || len(entries) != 0 {
		t.Fatalf("root after delete = %+v, %v", entries, err)
	}
}

func TestDirectoryDeleteNodeCleanupFailureIsAtomic(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if err := app.Mkdir(ctx, "/batch/empty/nested"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Raw().Exec(`
		create trigger reject_directory_cleanup before delete on nodes
		when old.canonical_path='/batch/empty/nested'
		begin select raise(abort, 'test cleanup failure'); end`); err != nil {
		t.Fatal(err)
	}
	res, err := app.DeleteFile(ctx, "/batch", DeleteOptions{Confirm: true})
	if appErrCode(t, err) != apperr.ErrDB {
		t.Fatalf("cleanup error = %v", err)
	}
	progress, err := app.DB.DirectoryDeleteProgress(ctx, res.OperationID)
	if err != nil || progress.Status != "failed" || progress.Error == "" {
		t.Fatalf("cleanup progress = %+v, %v", progress, err)
	}
	for _, path := range []string{"/batch", "/batch/empty", "/batch/empty/nested"} {
		if _, err := app.ListDir(ctx, path); err != nil {
			t.Fatalf("transaction removed %s despite cleanup failure: %v", path, err)
		}
	}
}
