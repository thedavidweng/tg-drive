package transfer_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/core/telegram/fake"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/transfer"
)

// The CLI runs one Transfer per process, so these properties of a Manager
// running several at once are pinned here, against the fake Telegram.

// newApp returns an App on a fresh index and a logged-in fake with a bound
// drive channel. Uploads above 1 KiB take the fake's resumable path in
// 1 KiB parts.
func newApp(t *testing.T) (*service.App, *fake.Client) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Storage.DBPath = filepath.Join(dir, "index.db")
	db, err := sqlitestore.Open(cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tg := fake.New()
	tg.SetCredentials("12345", "")
	ctx := context.Background()
	if _, err := tg.Login(ctx, 1, "hash", "+1000",
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{}); err != nil {
		t.Fatal(err)
	}
	app := &service.App{Cfg: cfg, DB: db, TG: tg}
	if _, err := app.InitRoot(ctx, t.TempDir(), "Drive", "Drive", ""); err != nil {
		t.Fatal(err)
	}
	tg.SetResumableThreshold(1024)
	tg.SetPartSize(1024)
	return app, tg
}

func localFile(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i % 251)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestManagerRunsAtMostConcurrencyTransfers: with transfers.concurrency 2,
// five submitted uploads all complete, and no more than two are ever past
// queued and not yet ended.
func TestManagerRunsAtMostConcurrencyTransfers(t *testing.T) {
	app, tg := newApp(t)
	app.Cfg.Transfers.Concurrency = 2
	tg.SetTransferDelay(20 * time.Millisecond)

	var mu sync.Mutex
	running, most := map[string]bool{}, 0
	m := transfer.New(app, transfer.Options{FrontEnd: transfer.FrontEndCLI, Observer: transfer.Observer{
		OnStage: func(tr transfer.Transfer) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case tr.Stage.Terminal():
				delete(running, tr.ID)
			case tr.Stage != transfer.StageQueued:
				running[tr.ID] = true
			}
			most = max(most, len(running))
		},
	}})

	ctx := context.Background()
	var handles []*transfer.Handle[*service.UploadResult]
	for i := range 5 {
		h, err := m.SubmitUpload(ctx, transfer.Upload{
			Source: localFile(t, fmt.Sprintf("f%d.bin", i), 6*1024),
			Dest:   fmt.Sprintf("/f%d.bin", i),
			Policy: service.ConflictFail,
		})
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, h)
	}
	queued, err := m.List(ctx, transfer.Filter{Stage: transfer.StageQueued})
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) < 3 {
		t.Fatalf("queued right after submitting five = %d, want at least the three without a slot", len(queued))
	}
	for _, h := range handles {
		if _, err := h.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if most != 2 {
		t.Fatalf("most Transfers running at once = %d, want 2", most)
	}
	done, err := m.List(ctx, transfer.Filter{Stage: transfer.StageCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 5 {
		t.Fatalf("completed = %d, want all 5", len(done))
	}
}

// TestManagerThrottlesProgress: a Transfer whose upload reports progress
// many times a second records it only a few times a second, and still ends
// with its full byte count.
func TestManagerThrottlesProgress(t *testing.T) {
	app, tg := newApp(t)
	tg.SetTransferDelay(20 * time.Millisecond)

	var mu sync.Mutex
	reported, recorded := 0, 0
	m := transfer.New(app, transfer.Options{FrontEnd: transfer.FrontEndCLI, Observer: transfer.Observer{
		OnProgress: func(transfer.Transfer) {
			mu.Lock()
			defer mu.Unlock()
			recorded++
		},
	}})
	const parts = 60
	start := time.Now()
	h, err := m.SubmitUpload(context.Background(), transfer.Upload{
		Source: localFile(t, "big.bin", parts*1024),
		Dest:   "/big.bin",
		Policy: service.ConflictFail,
		Options: service.UploadOptions{Threads: 1, Observer: service.Observer{
			OnProgress: func(service.Progress) {
				mu.Lock()
				defer mu.Unlock()
				reported++
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Wait(); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if reported != parts {
		t.Fatalf("upload reported progress %d times, want once per part (%d)", reported, parts)
	}
	if limit := int(5*elapsed.Seconds()) + 1; recorded < 1 || recorded > limit {
		t.Fatalf("recorded progress %d times in %s, want between 1 and %d (at most five a second)", recorded, elapsed, limit)
	}
	got, err := m.Get(context.Background(), h.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != transfer.StageCompleted || got.BytesDone != parts*1024 || got.BytesTotal != parts*1024 {
		t.Fatalf("transfer = %+v, want completed with all %d bytes", got, parts*1024)
	}
}

// TestManagerRenewsLease: a running Transfer's lease advances with the
// owner's heartbeat (the locks.ttl_seconds TTL and its one-third renewal
// interval, as Operation locks use), recorded in the index for other
// processes to see; once the Transfer ends, its lease stops advancing.
func TestManagerRenewsLease(t *testing.T) {
	app, tg := newApp(t)
	app.Cfg.Locks.TTLSeconds = 3
	tg.SetTransferDelay(100 * time.Millisecond)

	m := transfer.New(app, transfer.Options{FrontEnd: transfer.FrontEndCLI})
	h, err := m.SubmitUpload(context.Background(), transfer.Upload{
		Source: localFile(t, "big.bin", 30*1024),
		Dest:   "/big.bin",
		Policy: service.ConflictFail,
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := leaseExpiry(t, app, h.ID())
	for deadline := time.Now().Add(5 * time.Second); ; {
		if got := leaseExpiry(t, app, h.ID()); got > initial {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("lease still %s after 5s, want the heartbeat past %s", got, initial)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := h.Wait(); err != nil {
		t.Fatal(err)
	}
	ended := leaseExpiry(t, app, h.ID())
	time.Sleep(1500 * time.Millisecond)
	if got := leaseExpiry(t, app, h.ID()); got != ended {
		t.Fatalf("lease advanced to %s after the Transfer ended at %s", got, ended)
	}
}

func leaseExpiry(t *testing.T, app *service.App, id string) string {
	t.Helper()
	row, err := app.DB.GetTransfer(context.Background(), id)
	if err != nil || row == nil {
		t.Fatalf("read transfer %s: row=%v err=%v", id, row, err)
	}
	if row.LeaseExpiresAt == "" {
		t.Fatalf("transfer %s has no lease; the owner must lease it from submission", id)
	}
	return row.LeaseExpiresAt
}

// TestManagerCancelQueuedNeverStarts: a queued Transfer cancelled through
// another Manager — another process's view of the same index — ends
// cancelled without its upload ever starting, while the Transfer ahead of
// it completes undisturbed.
func TestManagerCancelQueuedNeverStarts(t *testing.T) {
	app, tg := newApp(t)
	app.Cfg.Transfers.Concurrency = 1
	app.Cfg.Locks.TTLSeconds = 3
	tg.SetTransferDelay(100 * time.Millisecond)

	m := transfer.New(app, transfer.Options{FrontEnd: transfer.FrontEndCLI})
	ctx := context.Background()
	// The first upload holds the one slot for about 3s (30 parts), so the
	// second Transfer is still queued when the cancel lands.
	first, err := m.SubmitUpload(ctx, transfer.Upload{
		Source: localFile(t, "first.bin", 30*1024),
		Dest:   "/first.bin",
		Policy: service.ConflictFail,
	})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := m.SubmitUpload(ctx, transfer.Upload{
		Source: localFile(t, "queued.bin", 6*1024),
		Dest:   "/queued.bin",
		Policy: service.ConflictFail,
	})
	if err != nil {
		t.Fatal(err)
	}

	other := transfer.New(app, transfer.Options{})
	if _, err := other.Cancel(ctx, queued.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := queued.Wait(); !apperr.IsCancelled(err) {
		t.Fatalf("queued Transfer ended with %v, want ERR_CANCELLED", err)
	}
	got, err := other.Get(ctx, queued.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != transfer.StageCancelled || got.FinishedAt == nil || !got.CancelRequested {
		t.Fatalf("cancelled queued Transfer = %+v, want cancelled with the flag recorded", got)
	}
	if _, err := first.Wait(); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	// The cancelled Transfer never started: there is nothing to download.
	_, err = app.DownloadFile(ctx, "/queued.bin", filepath.Join(t.TempDir(), "q.bin"), service.ConflictFail, service.DownloadOptions{})
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrRemoteNotFound {
		t.Fatalf("downloading the never-started upload: %v, want ERR_REMOTE_NOT_FOUND", err)
	}

	// Cancelling an ended Transfer is a usage error; an unknown ID is not
	// found.
	if _, err := m.Cancel(ctx, queued.ID()); err == nil {
		t.Fatal("cancelling an ended Transfer must fail")
	} else if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrUsage {
		t.Fatalf("cancel of an ended Transfer = %v, want ERR_USAGE", err)
	}
	if _, err := m.Cancel(ctx, "00000000-0000-4000-8000-000000000000"); err == nil {
		t.Fatal("cancelling an unknown Transfer must fail")
	} else if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrTransferNotFound {
		t.Fatalf("cancel of an unknown Transfer = %v, want ERR_TRANSFER_NOT_FOUND", err)
	}
}
