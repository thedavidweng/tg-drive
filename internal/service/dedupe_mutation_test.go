package service

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

func TestDedupeLocksKeeperAndTargets(t *testing.T) {
	for _, locked := range []string{"/copies/a", "/copies/b"} {
		t.Run(locked, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			ctx := context.Background()
			for _, path := range []string{"/copies/a", "/copies/b"} {
				if _, err := app.UploadFile(ctx, writeLocal(t, "same"), path, ConflictFail, false, UploadOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			ch, err := app.channel(ctx)
			if err != nil {
				t.Fatal(err)
			}
			before := tg.Messages(ch.tgID)
			key := sqlitestore.LockKey(ch.rowID, locked)
			if err := app.DB.AcquireLock(ctx, key, "other-process", time.Minute); err != nil {
				t.Fatal(err)
			}
			result, err := app.DeleteDuplicates(ctx, "/copies", DedupeDeleteOptions{Confirm: true})
			if appErrCode(t, err) != apperr.ErrOperationLocked || result.Deleted != 0 {
				t.Fatalf("locked cleanup = %+v, %v", result, err)
			}
			if !reflect.DeepEqual(before, tg.Messages(ch.tgID)) {
				t.Fatal("lock conflict changed remote messages")
			}
			if err := app.DB.ReleaseLock(ctx, key, "other-process"); err != nil {
				t.Fatal(err)
			}
			if _, err := app.DeleteDuplicates(ctx, "/copies", DedupeDeleteOptions{Confirm: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type dedupeReadClient struct {
	telegram.Client
	messageID int
	read      func(io.Writer) error
}

func (c *dedupeReadClient) DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error {
	if messageID == c.messageID {
		return c.read(dst)
	}
	return c.Client.DownloadMedia(ctx, channelID, messageID, dst)
}

func TestDedupeVerifiesEveryRemoteCopyBeforeMutating(t *testing.T) {
	for _, failure := range []string{"missing-keeper", "changed-last-copy", "cancel-after-verification"} {
		t.Run(failure, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var ids []int
			for _, path := range []string{"/copies/a", "/copies/b", "/copies/c"} {
				up, err := app.UploadFile(ctx, writeLocal(t, "same"), path, ConflictFail, false, UploadOptions{})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, up.MessageID)
			}
			tgID, err := app.tgChannelID(ctx)
			if err != nil {
				t.Fatal(err)
			}
			code := apperr.ErrPathConflict
			switch failure {
			case "missing-keeper":
				if err := tg.DeleteMessage(ctx, tgID, ids[0]); err != nil {
					t.Fatal(err)
				}
				code = apperr.ErrRemoteNotFound
			case "changed-last-copy":
				app.TG = &dedupeReadClient{Client: app.TG, messageID: ids[2], read: func(w io.Writer) error {
					_, err := io.WriteString(w, "edit")
					return err
				}}
			case "cancel-after-verification":
				original := app.TG
				app.TG = &dedupeReadClient{Client: original, messageID: ids[2], read: func(w io.Writer) error {
					err := original.DownloadMedia(ctx, tgID, ids[2], w)
					cancel()
					return err
				}}
				code = apperr.ErrCancelled
			}
			before := tg.Messages(tgID)
			result, err := app.DeleteDuplicates(ctx, "/copies", DedupeDeleteOptions{Confirm: true})
			if appErrCode(t, err) != code || result.Deleted != 0 {
				t.Fatalf("preflight = %+v, %v; want %s", result, err, code)
			}
			if !reflect.DeepEqual(before, tg.Messages(tgID)) {
				t.Fatal("failed preflight mutated remote messages")
			}
		})
	}
}

func TestDedupeInterruptionReopenRetryAndReconstruction(t *testing.T) {
	for _, failure := range []string{"permission", "flood-wait", "cancel", "index-commit"} {
		t.Run(failure, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			app.Cfg.Delete.Mode = "delete"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var interruptedID int
			for _, path := range []string{"/copies/a", "/copies/b", "/copies/c", "/copies/d", "/outside/keep"} {
				up, err := app.UploadFile(ctx, writeLocal(t, "same"), path, ConflictFail, false, UploadOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if path == "/copies/c" {
					interruptedID = up.MessageID
				}
			}
			calls := 0
			wantCode := apperr.ErrChannelPermission
			if failure == "index-commit" {
				if _, err := app.DB.Raw().Exec(`
					create trigger reject_dedupe_commit before update on files
					when old.canonical_path='/copies/c' and new.status='deleted'
					begin select raise(abort, 'injected index failure'); end`); err != nil {
					t.Fatal(err)
				}
				wantCode = apperr.ErrDB
			} else {
				app.TG = &interruptDirectoryClient{Client: app.TG, messageID: interruptedID, interrupt: func() error {
					calls++
					switch failure {
					case "cancel":
						cancel()
						return context.Canceled
					case "flood-wait":
						return &telegram.FloodWaitError{Seconds: 37}
					default:
						return &telegram.PermissionDeniedError{}
					}
				}}
				if failure == "cancel" {
					wantCode = apperr.ErrCancelled
				}
				if failure == "flood-wait" {
					wantCode = apperr.ErrTelegramRateLimited
				}
			}
			result, err := app.DeleteDuplicates(ctx, "/copies", DedupeDeleteOptions{Confirm: true})
			if appErrCode(t, err) != wantCode || result.Deleted != 1 || !reflect.DeepEqual(result.DeletedPaths, []string{"/copies/b"}) {
				t.Fatalf("partial cleanup = %+v, %v", result, err)
			}
			ae, _ := apperr.As(err)
			if ae.Details["deleted"] != 1 || (failure != "index-commit" && calls != 1) {
				t.Fatalf("progress/attempts = %+v / %d", ae.Details, calls)
			}
			if failure == "flood-wait" && ae.Details["retry_after_seconds"] != 37 {
				t.Fatalf("rate limit details = %+v", ae.Details)
			}
			if failure == "index-commit" {
				if _, err := app.DB.Raw().Exec(`drop trigger reject_dedupe_commit`); err != nil {
					t.Fatal(err)
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
			ctx = context.Background()
			// A remote success followed by an index failure needs an explicit
			// scan before retry: the stale index is not proof of remote bytes.
			if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
				t.Fatal(err)
			}
			result, err = app.DeleteDuplicates(ctx, "/copies", DedupeDeleteOptions{Confirm: true})
			wantDeleted := 2
			if failure == "index-commit" {
				wantDeleted = 1
			}
			if err != nil || result.Deleted != wantDeleted {
				t.Fatalf("retry = %+v, %v", result, err)
			}
			scan, err := app.Scan(ctx, ScanOptions{Full: true})
			if err != nil || scan.Active != 2 {
				t.Fatalf("reconstructed tree = %+v, %v", scan, err)
			}
			for _, path := range []string{"/copies/a", "/outside/keep"} {
				dst := filepath.Join(t.TempDir(), "copy")
				if _, err := app.DownloadFile(ctx, path, dst, ConflictFail, DownloadOptions{}); err != nil {
					t.Fatal(err)
				}
				if got, err := os.ReadFile(dst); err != nil || string(got) != "same" {
					t.Fatalf("retained bytes = %q, %v", got, err)
				}
			}
		})
	}
}

func TestDedupeHonorsTombstonePolicy(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	app.Cfg.Delete.Mode = "tombstone"
	ctx := context.Background()
	var duplicateID int
	for _, path := range []string{"/copies/a", "/copies/b"} {
		up, err := app.UploadFile(ctx, writeLocal(t, "same"), path, ConflictFail, false, UploadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		duplicateID = up.MessageID
	}
	result, err := app.DeleteDuplicates(ctx, "/copies", DedupeDeleteOptions{Confirm: true})
	if err != nil || result.Deleted != 1 {
		t.Fatalf("tombstone cleanup = %+v, %v", result, err)
	}
	tgID, err := app.tgChannelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	media, err := tg.GetMessage(ctx, tgID, duplicateID)
	if err != nil || string(media.Data) != "same" {
		t.Fatalf("tombstone unexpectedly removed bytes = %+v, %v", media, err)
	}
	scan, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil || scan.Active != 1 {
		t.Fatalf("tombstone reconstruction = %+v, %v", scan, err)
	}
}
