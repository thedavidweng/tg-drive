package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func TestDirectoryRemoteWriteIndexFailureReconstruction(t *testing.T) {
	for _, operation := range []string{"move", "delete"} {
		t.Run(operation, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			ctx := context.Background()
			for _, path := range []string{"/batch/a.txt", "/batch/b.txt", "/batch/c.txt", "/target/keep.txt"} {
				if _, err := app.UploadFile(ctx, writeLocal(t, path), path, ConflictFail, false, UploadOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			run := func() error {
				if operation == "move" {
					return app.MoveFile(ctx, "/batch", "/target", MoveOptions{Confirm: true})
				}
				_, err := app.DeleteFile(ctx, "/batch", DeleteOptions{Confirm: true})
				return err
			}
			// The remote mutation runs before either index update. Abort the
			// second file's commit while keeping its completed remote write.
			if _, err := app.DB.Raw().Exec(`
				create trigger reject_second_file_commit before update on files
				when old.canonical_path='/batch/b.txt'
					and (new.canonical_path!=old.canonical_path or new.status='deleted')
				begin select raise(abort, 'injected index failure'); end`); err != nil {
				t.Fatal(err)
			}
			err := run()
			if appErrCode(t, err) != apperr.ErrDB {
				t.Fatalf("interrupted operation = %v, want ERR_DB", err)
			}
			ae, _ := apperr.As(err)
			id, ok := ae.Details["operation_id"].(string)
			if !ok || id == "" || ae.Details["failed"] != 1 || ae.Details["remaining"] != 2 {
				t.Fatalf("interrupted progress = %+v", ae.Details)
			}
			if fileStatus(t, app, "/batch/b.txt") != "active" {
				t.Fatal("failed commit unexpectedly changed the second file's index")
			}
			if _, err := app.DB.Raw().Exec(`drop trigger reject_second_file_commit`); err != nil {
				t.Fatal(err)
			}
			if err := app.DB.Close(); err != nil {
				t.Fatal(err)
			}
			app.DB, err = sqlitestore.Open(app.Cfg.Storage.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = app.DB.Close() })
			auditWrites(t, app, tg)
			if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
				t.Fatal(err)
			}
			wantSecond := "missing"
			table, countColumn := "directory_delete_operations", "deleted"
			if operation == "move" {
				wantSecond = ""
				table, countColumn = "directory_move_operations", "moved"
				if fileStatus(t, app, "/target/batch/b.txt") != "active" {
					t.Fatal("full scan did not reconstruct the remotely moved second file")
				}
			}
			if got := fileStatus(t, app, "/batch/b.txt"); got != wantSecond {
				t.Fatalf("reconstructed source status = %q, want %q", got, wantSecond)
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			var total, changed, failed, remaining int
			var status string
			if err := app.DB.Raw().QueryRow("select status,total,"+countColumn+",failed,remaining from "+table+" where id!=?", id).
				Scan(&status, &total, &changed, &failed, &remaining); err != nil {
				t.Fatal(err)
			}
			if status != "completed" || total != 1 || changed != 1 || failed != 0 || remaining != 0 {
				t.Fatalf("retry = %s/%d/%d/%d/%d", status, total, changed, failed, remaining)
			}
			if err := app.DB.Raw().QueryRow("select status,total,"+countColumn+",failed,remaining from "+table+" where id=?", id).
				Scan(&status, &total, &changed, &failed, &remaining); err != nil {
				t.Fatal(err)
			}
			if status != "failed" || total != 3 || changed != 1 || failed != 1 || remaining != 2 {
				t.Fatalf("original history = %s/%d/%d/%d/%d", status, total, changed, failed, remaining)
			}
			scan, err := app.Scan(ctx, ScanOptions{Full: true})
			wantActive := 1
			if operation == "move" {
				wantActive = 4
			}
			if err != nil || scan.Active != wantActive || fileStatus(t, app, "/target/keep.txt") != "active" {
				t.Fatalf("final tree = %+v, %v; want %d active and unrelated file retained", scan, err, wantActive)
			}
			if operation == "move" {
				for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
					dest := filepath.Join(t.TempDir(), name)
					if _, err := app.DownloadFile(ctx, "/target/batch/"+name, dest, ConflictFail, DownloadOptions{}); err != nil {
						t.Fatal(err)
					}
					if got, err := os.ReadFile(dest); err != nil || string(got) != "/batch/"+name {
						t.Fatalf("reconstructed bytes = %q, %v", got, err)
					}
				}
			}
		})
	}
}
