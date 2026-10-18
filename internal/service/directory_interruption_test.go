package service

import (
	"context"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

type interruptDirectoryClient struct {
	telegram.Client
	messageID int
	interrupt func() error
}

func (c *interruptDirectoryClient) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	if messageID == c.messageID {
		return c.interrupt()
	}
	return c.Client.EditCaption(ctx, channelID, messageID, caption)
}

func (c *interruptDirectoryClient) DeleteMessage(ctx context.Context, channelID int64, messageID int) error {
	if messageID == c.messageID {
		return c.interrupt()
	}
	return c.Client.DeleteMessage(ctx, channelID, messageID)
}

func TestDirectoryInterruptionPreservesProgressAndRetry(t *testing.T) {
	for _, operation := range []string{"move", "delete"} {
		for _, cause := range []string{"cancel", "flood_wait"} {
			t.Run(operation+"/"+cause, func(t *testing.T) {
				app, tg := testApp(t)
				loginAndInit(t, app, tg)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var failedMessage int
				for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
					res, err := app.UploadFile(ctx, writeLocal(t, name), "/batch/"+name, ConflictFail, false, UploadOptions{})
					if err != nil {
						t.Fatal(err)
					}
					if name == "b.txt" {
						failedMessage = res.MessageID
					}
				}
				original := app.TG
				calls := 0
				app.TG = &interruptDirectoryClient{
					Client: original, messageID: failedMessage,
					interrupt: func() error {
						calls++
						if cause == "cancel" {
							cancel()
							return context.Canceled
						}
						return &telegram.FloodWaitError{Seconds: 37}
					},
				}
				run := func(ctx context.Context) error {
					if operation == "move" {
						return app.MoveFile(ctx, "/batch", "/destination", MoveOptions{Confirm: true})
					}
					_, err := app.DeleteFile(ctx, "/batch", DeleteOptions{Confirm: true})
					return err
				}
				err := run(ctx)
				wantCode := apperr.ErrCancelled
				if cause == "flood_wait" {
					wantCode = apperr.ErrTelegramRateLimited
				}
				if appErrCode(t, err) != wantCode {
					t.Fatalf("error = %v, want %s", err, wantCode)
				}
				ae, _ := apperr.As(err)
				if cause == "flood_wait" && ae.Details["retry_after_seconds"] != 37 {
					t.Fatalf("rate limit details = %+v", ae.Details)
				}
				if calls != 1 {
					t.Fatalf("remote attempts at interrupted file = %d, want 1", calls)
				}
				id, ok := ae.Details["operation_id"].(string)
				if !ok || id == "" {
					t.Fatalf("missing operation id: %+v", ae.Details)
				}
				check := func() {
					t.Helper()
					if operation == "move" {
						p, err := app.DB.DirectoryMoveProgress(context.Background(), id)
						if err != nil || p.Status != "failed" || p.Moved != 1 || p.Failed != 1 || p.Remaining != 2 || p.LastPath != "/batch/b.txt" {
							t.Fatalf("move progress = %+v, %v", p, err)
						}
					} else {
						p, err := app.DB.DirectoryDeleteProgress(context.Background(), id)
						if err != nil || p.Status != "failed" || p.Deleted != 1 || p.Failed != 1 || p.Remaining != 2 || p.LastPath != "/batch/b.txt" {
							t.Fatalf("delete progress = %+v, %v", p, err)
						}
					}
				}
				check()
				app.TG = original
				if err := run(context.Background()); err != nil {
					t.Fatal(err)
				}
				check()
				for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
					wantSource := ""
					if operation == "delete" {
						wantSource = "deleted"
					}
					if got := fileStatus(t, app, "/batch/"+name); got != wantSource {
						t.Fatalf("source %s status = %q", name, got)
					}
					if operation == "move" && fileStatus(t, app, "/destination/"+name) != "active" {
						t.Fatalf("missing moved file %s", name)
					}
				}
			})
		}
	}
}
