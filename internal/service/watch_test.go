package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

func watchOnce(t *testing.T, a *App, ctx context.Context, local, remote string, remove bool) (WatchResult, error) {
	t.Helper()
	var result WatchResult
	err := a.WatchDirectory(ctx, local, remote, WatchOptions{Confirm: true, Delete: remove, Once: true, Interval: time.Millisecond}, func(r WatchResult) error { result = r; return nil })
	return result, err
}

func watchWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	target := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemWatchContentRestartRenameAndDelete(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	local := t.TempDir()
	watchWrite(t, local, "nested/a.txt", "first")
	first, err := watchOnce(t, a, ctx, local, "/watch", false)
	if err != nil || first.Uploaded != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	databasePath := a.Cfg.Storage.DBPath
	_ = a.DB.Close()
	a.DB, err = sqlitestore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.DB.Close() }()
	a.TG.(*writeAudit).db = a.DB
	repeat, err := watchOnce(t, a, ctx, local, "/watch", false)
	if err != nil || repeat.Unchanged != 1 || repeat.Uploaded != 0 {
		t.Fatalf("restart=%+v err=%v", repeat, err)
	}
	watchWrite(t, local, "nested/a.txt", "changed")
	changed, err := watchOnce(t, a, ctx, local, "/watch", false)
	if err != nil || changed.Uploaded != 1 {
		t.Fatalf("change=%+v err=%v", changed, err)
	}
	if err := os.Rename(filepath.Join(local, "nested/a.txt"), filepath.Join(local, "nested/b.txt")); err != nil {
		t.Fatal(err)
	}
	renamed, err := watchOnce(t, a, ctx, local, "/watch", false)
	if err != nil || renamed.Uploaded != 1 || renamed.Retained != 1 {
		t.Fatalf("rename=%+v err=%v", renamed, err)
	}
	if _, err := a.UploadFile(ctx, writeLocal(t, "unrelated"), "/watch/unrelated.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	removed, err := watchOnce(t, a, ctx, local, "/watch", true)
	if err != nil || removed.Deleted != 1 || removed.Unchanged != 1 {
		t.Fatalf("delete=%+v err=%v", removed, err)
	}
	ch, _ := a.channel(ctx)
	if _, found, err := a.DB.ActiveByPath(ctx, ch.rowID, "/watch/unrelated.txt"); err != nil || !found {
		t.Fatalf("unowned file lost: %v", err)
	}
}

func TestFilesystemWatchConflictsAndCheckpointRecovery(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	local := t.TempDir()
	watchWrite(t, local, "a.txt", "wanted")
	if _, err := a.UploadFile(ctx, writeLocal(t, "different"), "/sync/a.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := watchOnce(t, a, ctx, local, "/sync", false); appErrCode(t, err) != apperr.ErrPathConflict {
		t.Fatalf("unowned conflict=%v", err)
	}
	if _, err := a.UploadFile(ctx, writeLocal(t, "wanted"), "/sync/a.txt", ConflictReplace, false, UploadOptions{ConfirmReplace: true}); err != nil {
		t.Fatal(err)
	}
	// Simulates a committed upload before its watch checkpoint was written.
	adopted, err := watchOnce(t, a, ctx, local, "/sync", false)
	if err != nil || adopted.Unchanged != 1 || adopted.Uploaded != 0 {
		t.Fatalf("adopt=%+v err=%v", adopted, err)
	}
	if _, err := a.UploadFile(ctx, writeLocal(t, "remote edit"), "/sync/a.txt", ConflictReplace, false, UploadOptions{ConfirmReplace: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := watchOnce(t, a, ctx, local, "/sync", false); appErrCode(t, err) != apperr.ErrPathConflict {
		t.Fatalf("remote edit=%v", err)
	}
	if err := os.Remove(filepath.Join(local, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := watchOnce(t, a, ctx, local, "/sync", true); appErrCode(t, err) != apperr.ErrPathConflict {
		t.Fatalf("delete changed remote=%v", err)
	}
}

func TestFilesystemWatchCancellationResumesAndRejectsChangedBinding(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	local := t.TempDir()
	watchWrite(t, local, "file.txt", "stable")
	ctx, cancel := context.WithCancel(context.Background())
	err := a.WatchDirectory(ctx, local, "/sync", WatchOptions{Confirm: true, Interval: time.Hour}, func(WatchResult) error { cancel(); return nil })
	if appErrCode(t, err) != apperr.ErrCancelled {
		t.Fatalf("cancel=%v", err)
	}
	result, err := watchOnce(t, a, context.Background(), local, "/sync", false)
	if err != nil || result.Unchanged != 1 {
		t.Fatalf("resume=%+v err=%v", result, err)
	}
	calls := 0
	err = a.WatchDirectory(context.Background(), local, "/sync", WatchOptions{Confirm: true, Interval: time.Millisecond}, func(WatchResult) error {
		calls++
		_, err := a.DB.Raw().Exec(`update channels set root_local_path='/rebound'`)
		return err
	})
	if appErrCode(t, err) != apperr.ErrPathConflict || calls != 1 {
		t.Fatalf("rebind calls=%d err=%v", calls, err)
	}
}

func TestFilesystemWatchForumRootsStayIsolated(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	peer, err := tg.CreateChannel(ctx, "Forum")
	if err != nil {
		t.Fatal(err)
	}
	var roots []string
	for _, topic := range []int{10, 20} {
		tg.SetForumTopic(peer.ID, telegram.ForumTopic{ID: topic, Title: fmt.Sprint(topic)})
		root := t.TempDir()
		roots = append(roots, root)
		if _, err := a.InitForumRoot(ctx, root, fmt.Sprint(peer.ID), topic); err != nil {
			t.Fatal(err)
		}
		watchWrite(t, root, "file.txt", fmt.Sprint(topic))
		if _, err := watchOnce(t, a, WithLocalRoot(ctx, root), root, "/sync", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(roots[0], "file.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := watchOnce(t, a, WithLocalRoot(ctx, roots[0]), roots[0], "/sync", true); err != nil {
		t.Fatal(err)
	}
	ch, err := a.channel(WithLocalRoot(ctx, roots[1]))
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.DB.ActiveByPath(ctx, ch.rowID, "/sync/file.txt"); err != nil || !found {
		t.Fatalf("other topic file lost: %v", err)
	}
}

func TestFilesystemWatchRejectsUnconfirmedAndHashDisabled(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	local := t.TempDir()
	ctx := context.Background()
	err := a.WatchDirectory(ctx, local, "/", WatchOptions{Interval: time.Second}, func(WatchResult) error { return nil })
	if appErrCode(t, err) != apperr.ErrConfirmationRequired {
		t.Fatalf("confirmation=%v", err)
	}
	a.Cfg.Hash.Enabled = false
	if _, err := watchOnce(t, a, ctx, local, "/sync", false); appErrCode(t, err) != apperr.ErrConfigInvalid {
		t.Fatalf("hash config=%v", err)
	}
	a.Cfg.Hash.Enabled = true
}

func TestFilesystemWatchCheckpointFailureAdoptsCommittedUpload(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	local := t.TempDir()
	watchWrite(t, local, "a.txt", "a")
	watchWrite(t, local, "b.txt", "b")
	if _, err := a.DB.Raw().Exec(`create trigger reject_watch_checkpoint before insert on watch_checkpoints when new.relative_path='b.txt' begin select raise(abort,'injected checkpoint failure'); end`); err != nil {
		t.Fatal(err)
	}
	if _, err := watchOnce(t, a, context.Background(), local, "/sync", false); appErrCode(t, err) != apperr.ErrDB {
		t.Fatalf("checkpoint failure=%v", err)
	}
	if _, err := a.DB.Raw().Exec(`drop trigger reject_watch_checkpoint`); err != nil {
		t.Fatal(err)
	}
	result, err := watchOnce(t, a, context.Background(), local, "/sync", false)
	if err != nil || result.Unchanged != 2 || result.Uploaded != 0 {
		t.Fatalf("recovery=%+v err=%v", result, err)
	}
}

func TestFilesystemWatchRenameUploadsBeforeDeletingOldCopy(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	local := t.TempDir()
	watchWrite(t, local, "a.txt", "retain until replaced")
	if _, err := watchOnce(t, a, ctx, local, "/sync", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(local, "a.txt"), filepath.Join(local, "z.txt")); err != nil {
		t.Fatal(err)
	}
	tg.SetFailUpload(true)
	if _, err := watchOnce(t, a, ctx, local, "/sync", true); err == nil {
		t.Fatal("failed upload accepted")
	}
	ch, _ := a.channel(ctx)
	if _, found, err := a.DB.ActiveByPath(ctx, ch.rowID, "/sync/a.txt"); err != nil || !found {
		t.Fatalf("old copy deleted before destination committed: %v", err)
	}
	tg.SetFailUpload(false)
	result, err := watchOnce(t, a, ctx, local, "/sync", true)
	if err != nil || result.Uploaded != 1 || result.Deleted != 1 {
		t.Fatalf("retry=%+v err=%v", result, err)
	}
}

type changedWatchBytes struct{ telegram.Client }

func (c changedWatchBytes) DownloadMedia(ctx context.Context, peer int64, id int, w io.Writer) error {
	_, err := io.WriteString(w, "changed without index update")
	return err
}

func TestFilesystemWatchVerifiesRemoteBytesBeforeMutation(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	local := t.TempDir()
	watchWrite(t, local, "a.txt", "original")
	if _, err := watchOnce(t, a, context.Background(), local, "/sync", false); err != nil {
		t.Fatal(err)
	}
	a.TG = changedWatchBytes{Client: a.TG}
	watchWrite(t, local, "a.txt", "changed locally")
	if _, err := watchOnce(t, a, context.Background(), local, "/sync", false); appErrCode(t, err) != apperr.ErrPathConflict {
		t.Fatalf("remote bytes verification=%v", err)
	}
}

func TestFilesystemWatchLeaseAndChangedSnapshot(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	local := t.TempDir()
	watchWrite(t, local, "file.txt", "content")
	opts := WatchOptions{Confirm: true, Once: true, Interval: time.Millisecond}
	err := a.WatchDirectory(context.Background(), local, "/sync", opts, func(WatchResult) error {
		err := a.WatchDirectory(context.Background(), local, "/sync", opts, func(WatchResult) error { return nil })
		if appErrCode(t, err) != apperr.ErrOperationLocked {
			t.Fatalf("second watch=%v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(local)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if _, _, err := watchSnapshot(context.Background(), root, "file.txt", watchFile{hash: "wrong", size: 7}); appErrCode(t, err) != apperr.ErrPathConflict {
		t.Fatalf("changed snapshot=%v", err)
	}
}
