package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

type revokeDirectoryPermission struct {
	telegram.Client
	messageID int
	revoke    func()
}

func (c *revokeDirectoryPermission) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	if messageID == c.messageID {
		c.revoke()
	}
	return c.Client.EditCaption(ctx, channelID, messageID, caption)
}

func (c *revokeDirectoryPermission) DeleteMessage(ctx context.Context, channelID int64, messageID int) error {
	if messageID == c.messageID {
		c.revoke()
	}
	return c.Client.DeleteMessage(ctx, channelID, messageID)
}

func TestDirectoryPermissionLossAndExplicitRetry(t *testing.T) {
	for _, operation := range []string{"move", "delete"} {
		for _, failAt := range []int{0, 1} {
			t.Run(operation+"/"+[]string{"before-first", "after-first"}[failAt], func(t *testing.T) {
				app, tg := testApp(t)
				loginAndInit(t, app, tg)
				ctx := context.Background()
				var messages []int
				for _, path := range []string{"/batch/a.txt", "/batch/b.txt", "/batch/c.txt", "/target/keep.txt"} {
					result, err := app.UploadFile(ctx, writeLocal(t, path), path, ConflictFail, false, UploadOptions{})
					if err != nil {
						t.Fatal(err)
					}
					messages = append(messages, result.MessageID)
				}
				tgID, err := app.tgChannelID(ctx)
				if err != nil {
					t.Fatal(err)
				}
				history := func() []telegram.Message {
					t.Helper()
					result, err := tg.History(ctx, tgID, 0, 100)
					if err != nil {
						t.Fatal(err)
					}
					return result
				}
				before := history()
				original := app.TG
				app.TG = &revokeDirectoryPermission{
					Client: original, messageID: messages[failAt],
					revoke: func() { tg.SetDenyPermissions(true) },
				}
				run := func() error {
					if operation == "move" {
						return app.MoveFile(ctx, "/batch", "/target", MoveOptions{Confirm: true})
					}
					_, err := app.DeleteFile(ctx, "/batch", DeleteOptions{Confirm: true})
					return err
				}
				failureID := func(total, changed int) string {
					t.Helper()
					err := run()
					if code := appErrCode(t, err); code != apperr.ErrChannelPermission {
						t.Fatalf("permission failure = %v, want ERR_CHANNEL_PERMISSION", err)
					}
					ae, _ := apperr.As(err)
					id, ok := ae.Details["operation_id"].(string)
					if !ok || id == "" || ae.Details["failed"] != 1 || ae.Details["remaining"] != total-changed {
						t.Fatalf("failure details = %+v", ae.Details)
					}
					return id
				}
				firstID := failureID(3, failAt)
				afterFailure := history()
				// The failed and unattempted files, including their manifests,
				// must not be rewritten or silently tombstoned.
				for _, id := range messages[failAt:] {
					find := func(items []telegram.Message) telegram.Message {
						t.Helper()
						for _, item := range items {
							if item.ID == id {
								return item
							}
						}
						t.Fatalf("missing remote message %d", id)
						return telegram.Message{}
					}
					if !reflect.DeepEqual(find(before), find(afterFailure)) {
						t.Fatalf("permission failure changed untouched remote message %d", id)
					}
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
				secondID := failureID(3-failAt, 0)
				if secondID == firstID || !reflect.DeepEqual(afterFailure, history()) {
					t.Fatal("a denied retry changed the remote tree or reused history")
				}
				tg.SetDenyPermissions(false)
				if err := run(); err != nil {
					t.Fatal(err)
				}
				table, changedColumn := "directory_delete_operations", "deleted"
				if operation == "move" {
					table, changedColumn = "directory_move_operations", "moved"
				}
				checkProgress := func(id, wantStatus string, wantTotal, wantChanged, wantFailed, wantRemaining int) {
					t.Helper()
					var status, last string
					var total, changed, failed, remaining int
					if err := app.DB.Raw().QueryRow("select status,total,"+changedColumn+",failed,remaining,last_path from "+table+" where id=?", id).
						Scan(&status, &total, &changed, &failed, &remaining, &last); err != nil {
						t.Fatal(err)
					}
					if status != wantStatus || total != wantTotal || changed != wantChanged || failed != wantFailed || remaining != wantRemaining {
						t.Fatalf("progress %s = %s/%d/%d/%d/%d", id, status, total, changed, failed, remaining)
					}
					if status == "failed" && last != []string{"/batch/a.txt", "/batch/b.txt"}[failAt] {
						t.Fatalf("failed path = %q", last)
					}
				}
				checkProgress(firstID, "failed", 3, failAt, 1, 3-failAt)
				checkProgress(secondID, "failed", 3-failAt, 0, 1, 3-failAt)
				var completedID string
				if err := app.DB.Raw().QueryRow("select id from " + table + " where status='completed'").Scan(&completedID); err != nil {
					t.Fatal(err)
				}
				checkProgress(completedID, "completed", 3-failAt, 3-failAt, 0, 0)
				scan, err := app.Scan(ctx, ScanOptions{Full: true})
				wantActive := 1
				if operation == "move" {
					wantActive = 4
				}
				if err != nil || scan.Active != wantActive || fileStatus(t, app, "/target/keep.txt") != "active" {
					t.Fatalf("reconstructed tree = %+v, %v", scan, err)
				}
				if operation == "move" {
					for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
						dst := filepath.Join(t.TempDir(), name)
						if _, err := app.DownloadFile(ctx, "/target/batch/"+name, dst, ConflictFail, DownloadOptions{}); err != nil {
							t.Fatal(err)
						}
						if got, err := os.ReadFile(dst); err != nil || string(got) != "/batch/"+name {
							t.Fatalf("moved bytes = %q, %v", got, err)
						}
					}
				}
			})
		}
	}
}
