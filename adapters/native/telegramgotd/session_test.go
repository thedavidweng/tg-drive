package telegramgotd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/gotd/td/session"
)

// The fake Telegram client used by the E2E suite never reaches gotd's
// session storage, so its crash-safety guarantees are pinned here.

// A second storage on the same file (as another process would open it)
// racing a writer stands in for a crash mid-write: it must only ever observe
// a complete session, never a truncated one.
func TestSessionStorageNeverExposesTornWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to replace a file another handle has open")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStorage(path)
	reader := newSessionStorage(path)
	a := bytes.Repeat([]byte("a"), 1<<20)
	b := bytes.Repeat([]byte("b"), 1<<20)
	if err := store.StoreSession(ctx, a); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			next := a
			if i%2 == 0 {
				next = b
			}
			if err := store.StoreSession(ctx, next); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 300 {
		got, err := reader.LoadSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
			close(stop)
			wg.Wait()
			t.Fatalf("reader saw a torn session of %d bytes", len(got))
		}
	}
	close(stop)
	wg.Wait()
}

func TestSessionStorageMissingFileStartsFresh(t *testing.T) {
	store := newSessionStorage(filepath.Join(t.TempDir(), "session.json"))
	if _, err := store.LoadSession(context.Background()); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("LoadSession on a missing file = %v, want session.ErrNotFound", err)
	}
}

func TestSessionStorageIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes only")
	}
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := newSessionStorage(path).StoreSession(context.Background(), []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("session mode = %o, want 600", perm)
	}
}
