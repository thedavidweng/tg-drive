package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

type failMoveCaption struct {
	telegram.Client
	messageID int
}

func (c *failMoveCaption) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	if messageID == c.messageID {
		return fmt.Errorf("caption edit failed")
	}
	return c.Client.EditCaption(ctx, channelID, messageID, caption)
}

func TestDirectoryMoveProgressSurvivesReopenAndRetry(t *testing.T) {
	for _, existingDestination := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%v", existingDestination), func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			ctx := context.Background()
			target := "/moved"
			if existingDestination {
				if _, err := app.UploadFile(ctx, writeLocal(t, "keep"), "/moved/keep.txt", ConflictFail, false, UploadOptions{}); err != nil {
					t.Fatal(err)
				}
				target += "/retry"
			}
			var failedMessage int
			for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
				res, err := app.UploadFile(ctx, writeLocal(t, name), "/retry/"+name, ConflictFail, false, UploadOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if name == "b.txt" {
					failedMessage = res.MessageID
				}
			}
			originalClient := app.TG
			app.TG = &failMoveCaption{Client: originalClient, messageID: failedMessage}
			err := app.MoveFile(ctx, "/retry", "/moved", MoveOptions{Confirm: true})
			if appErrCode(t, err) != apperr.ErrTelegramRPC {
				t.Fatalf("move error = %v, want ERR_TELEGRAM_RPC", err)
			}
			ae, _ := apperr.As(err)
			operationID, ok := ae.Details["operation_id"].(string)
			if !ok || operationID == "" || ae.Details["moved"] != 1 || ae.Details["failed"] != 1 || ae.Details["remaining"] != 2 || ae.Details["to"] != target {
				t.Fatalf("partial details = %+v", ae.Details)
			}
			failed, err := app.DB.DirectoryMoveProgress(ctx, operationID)
			if err != nil {
				t.Fatal(err)
			}
			if failed.Status != "failed" || failed.Total != 3 || failed.Moved != 1 || failed.Failed != 1 || failed.Remaining != 2 || failed.LastPath != "/retry/b.txt" || failed.Error == "" || failed.Destination != target {
				t.Fatalf("failed checkpoint = %+v", failed)
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
			if err := app.MoveFile(ctx, "/retry", "/moved", MoveOptions{Confirm: true}); err != nil {
				t.Fatal(err)
			}
			var retryID string
			if err := app.DB.Raw().QueryRowContext(ctx, `select id from directory_move_operations where id!=?`, operationID).Scan(&retryID); err != nil {
				t.Fatal(err)
			}
			retry, err := app.DB.DirectoryMoveProgress(ctx, retryID)
			if err != nil {
				t.Fatal(err)
			}
			if retry.Status != "completed" || retry.Total != 2 || retry.Moved != 2 || retry.Remaining != 0 || retry.Destination != target {
				t.Fatalf("retry checkpoint = %+v", retry)
			}
			history, err := app.DB.DirectoryMoveProgress(ctx, operationID)
			if err != nil || history != failed {
				t.Fatalf("failure history = %+v, %v; want %+v", history, err, failed)
			}
			for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
				if got := fileStatus(t, app, target+"/"+name); got != "active" {
					t.Fatalf("destination %s/%s status = %q", target, name, got)
				}
				if got := fileStatus(t, app, "/retry/"+name); got != "" {
					t.Fatalf("source %s status = %q", name, got)
				}
			}
		})
	}
}
