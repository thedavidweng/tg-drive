package telegramgotd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/go-faster/errors"
	"github.com/gotd/td/session"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/fileperm"
)

// sessionStorage is gotd session storage that replaces the session file
// atomically. gotd's FileStorage rewrites the file in place, so a crash
// mid-write leaves a truncated session and loses the login.
type sessionStorage struct {
	path string
	mu   sync.Mutex
}

var _ session.Storage = (*sessionStorage)(nil)

func newSessionStorage(path string) *sessionStorage {
	return &sessionStorage{path: path}
}

func (s *sessionStorage) LoadSession(_ context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "read session")
	}
	return data, nil
}

// StoreSession writes a temp file in the session's directory, syncs it, and
// renames it over the session, so readers see the old or the new session and
// never a partial one.
func (s *sessionStorage) StoreSession(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return errors.Wrap(err, "create session temp file")
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errors.Wrap(err, "write session")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errors.Wrap(err, "sync session")
	}
	if err := tmp.Close(); err != nil {
		return errors.Wrap(err, "close session")
	}
	// The rename replaces the old file's owner-only DACL on Windows with the
	// directory's inherited one, so restrict the new file before it lands.
	if err := fileperm.Restrict(tmpPath); err != nil {
		return errors.Wrap(err, "restrict session")
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return errors.Wrap(err, "replace session")
	}
	committed = true
	syncDir(dir)
	return nil
}

// syncDir makes the rename durable. Windows cannot open a directory for
// syncing, and NTFS journals the rename itself.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
