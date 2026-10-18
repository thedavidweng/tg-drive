package service

import (
	"context"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func TestDirectoryMovePreservesLocalFolders(t *testing.T) {
	for _, withFiles := range []bool{false, true} {
		name := "empty"
		if withFiles {
			name = "with_files"
		}
		t.Run(name, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			ctx := context.Background()
			for _, path := range []string{"/source_%/empty/nested", "/source_other", "/target"} {
				if err := app.Mkdir(ctx, path); err != nil {
					t.Fatal(err)
				}
			}
			if withFiles {
				if _, err := app.UploadFile(ctx, writeLocal(t, "data"), "/source_%/file.txt", ConflictFail, false, UploadOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			second, err := app.InitRoot(ctx, t.TempDir(), "", "Other", "")
			if err != nil {
				t.Fatal(err)
			}
			other := WithLocalRoot(ctx, second.LocalRoot)
			if err := app.Mkdir(other, "/source_%/empty/nested"); err != nil {
				t.Fatal(err)
			}
			if err := app.MoveFile(ctx, "/source_%", "/target", MoveOptions{Confirm: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := app.ListDir(ctx, "/source_%"); appErrCode(t, err) != apperr.ErrRemoteNotFound {
				t.Fatalf("source directory is still visible: %v", err)
			}
			for _, path := range []string{"/target/source_%/empty/nested", "/source_other"} {
				if _, err := app.ListDir(ctx, path); err != nil {
					t.Fatalf("local directory %s: %v", path, err)
				}
			}
			if withFiles && fileStatus(t, app, "/target/source_%/file.txt") != "active" {
				t.Fatal("file was not moved with its local folders")
			}
			if _, err := app.ListDir(other, "/source_%/empty/nested"); err != nil {
				t.Fatalf("other channel changed: %v", err)
			}
		})
	}
}

func TestDirectoryMoveEmptyFolderRejectsFileDestination(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if err := app.Mkdir(ctx, "/empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, writeLocal(t, "keep"), "/target", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := app.MoveFile(ctx, "/empty", "/target", MoveOptions{Confirm: true}); appErrCode(t, err) != apperr.ErrPathExists {
		t.Fatalf("file destination error = %v", err)
	}
	if _, err := app.ListDir(ctx, "/empty"); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryMoveLocalCleanupFailureAndRetry(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if err := app.Mkdir(ctx, "/batch/empty/nested"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, writeLocal(t, "data"), "/batch/file.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Raw().Exec(`
		create trigger reject_local_move before delete on nodes
		when old.canonical_path='/batch/empty/nested'
		begin select raise(abort, 'test local move failure'); end`); err != nil {
		t.Fatal(err)
	}
	err := app.MoveFile(ctx, "/batch", "/moved", MoveOptions{Confirm: true})
	if appErrCode(t, err) != apperr.ErrDB {
		t.Fatalf("cleanup error = %v", err)
	}
	ae, _ := apperr.As(err)
	operationID := ae.Details["operation_id"].(string)
	progress, err := app.DB.DirectoryMoveProgress(ctx, operationID)
	if err != nil || progress.Status != "failed" || progress.Moved != 1 || progress.Remaining != 0 {
		t.Fatalf("failed local move progress = %+v, %v", progress, err)
	}
	if _, err := app.ListDir(ctx, "/batch/empty/nested"); err != nil {
		t.Fatalf("rollback lost source folder: %v", err)
	}
	if _, err := app.ListDir(ctx, "/moved/empty"); appErrCode(t, err) != apperr.ErrRemoteNotFound {
		t.Fatalf("rollback left destination folder: %v", err)
	}
	if _, err := app.DB.Raw().Exec(`drop trigger reject_local_move`); err != nil {
		t.Fatal(err)
	}
	if err := app.MoveFile(ctx, "/batch", "/moved", MoveOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ListDir(ctx, "/moved/empty/nested"); err != nil {
		t.Fatalf("retry destination changed: %v", err)
	}
	if _, err := app.ListDir(ctx, "/batch"); appErrCode(t, err) != apperr.ErrRemoteNotFound {
		t.Fatalf("retry left source folder: %v", err)
	}
	history, err := app.DB.DirectoryMoveProgress(ctx, operationID)
	if err != nil || history != progress {
		t.Fatalf("retry changed prior history: %+v, %v", history, err)
	}
}

func TestDirectoryMoveLocalFolderCollisionPrecedesRemoteWrites(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if err := app.Mkdir(ctx, "/source/empty"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/source/a.txt", "/target/source/empty"} {
		if _, err := app.UploadFile(ctx, writeLocal(t, path), path, ConflictFail, false, UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.MoveFile(ctx, "/source", "/target", MoveOptions{Confirm: true}); appErrCode(t, err) != apperr.ErrPathExists {
		t.Fatalf("local folder collision = %v", err)
	}
	if fileStatus(t, app, "/source/a.txt") != "active" || fileStatus(t, app, "/target/source/a.txt") != "" {
		t.Fatal("directory collision was discovered after moving a file")
	}
}

func TestDirectoryMoveLocalFolderIntoRootAndParent(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if err := app.Mkdir(ctx, "/parent/batch/empty"); err != nil {
		t.Fatal(err)
	}
	if err := app.MoveFile(ctx, "/parent/batch", "/", MoveOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ListDir(ctx, "/batch/empty"); err != nil {
		t.Fatalf("root did not retain source basename: %v", err)
	}
	if err := app.MoveFile(ctx, "/batch", "/", MoveOptions{Confirm: true}); err != nil {
		t.Fatalf("move to current parent: %v", err)
	}
	if _, err := app.ListDir(ctx, "/batch/empty"); err != nil {
		t.Fatalf("no-op move lost local folder: %v", err)
	}
}

func TestDirectoryMoveLocalFolderPreservesDestinationAncestorGC(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	if err := app.Mkdir(ctx, "/source/empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, writeLocal(t, "keep"), "/target/keep.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := app.MoveFile(ctx, "/source", "/target", MoveOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteFile(ctx, "/target/source", DeleteOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteFile(ctx, "/target/keep.txt", DeleteOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ListDir(ctx, "/target"); appErrCode(t, err) != apperr.ErrRemoteNotFound {
		t.Fatalf("move made an existing derived ancestor permanent: %v", err)
	}
}
