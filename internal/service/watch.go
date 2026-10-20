package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	"lukechampine.com/blake3"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// WatchOptions authorizes a one-way, content-based local directory watch.
// Deletion applies only to paths this watch has durably checkpointed.
type WatchOptions struct {
	Confirm  bool
	Delete   bool
	Once     bool
	Interval time.Duration
}

func (o WatchOptions) Validate() error {
	if !o.Confirm {
		return apperr.New(apperr.ErrConfirmationRequired, "filesystem watch writes require --confirm")
	}
	if o.Interval <= 0 {
		return apperr.New(apperr.ErrUsage, "watch interval must be positive")
	}
	return nil
}

// WatchResult describes one completed pass, not a claim of live two-way sync.
type WatchResult struct {
	LocalRoot  string `json:"local_root"`
	RemoteRoot string `json:"remote_root"`
	Uploaded   int    `json:"uploaded"`
	Unchanged  int    `json:"unchanged"`
	Deleted    int    `json:"deleted"`
	Retained   int    `json:"retained"`
}

type watchFile struct {
	hash string
	size int64
}

type watchBinding struct {
	ch      *channelContext
	account int64
	root    string
}

// WatchDirectory pins one binding for its lifetime. Completed file checkpoints
// survive cancellation and restart; errors stop the watch without auto-retry.
func (a *App) WatchDirectory(ctx context.Context, local, remote string, opts WatchOptions, emit func(WatchResult) error) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	if !a.Cfg.Hash.Enabled {
		return apperr.New(apperr.ErrConfigInvalid, "filesystem watch requires hash.enabled=true")
	}
	local, err := filepath.Abs(local)
	if err != nil {
		return err
	}
	info, err := os.Lstat(local)
	if err != nil {
		return apperr.Wrap(apperr.ErrLocalNotFound, "open watch directory", err)
	}
	if !info.IsDir() {
		return apperr.New(apperr.ErrPathInvalid, "watch source must be a directory, not a symlink or file")
	}
	root, err := os.OpenRoot(local)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	remote, err = fsmodel.NormalizeCanonicalPath(remote)
	if err != nil {
		return err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return err
	}
	binding := watchBinding{ch: ch}
	if err := a.DB.Raw().QueryRowContext(ctx, `select account_id,root_local_path from channels where id=?`, ch.rowID).Scan(&binding.account, &binding.root); err != nil {
		return apperr.Wrap(apperr.ErrDB, "pin watch binding", err)
	}
	// A separate lease excludes another owner of this exact checkpoint set.
	key := fmt.Sprintf("watch:%d:%q:%q", ch.rowID, local, remote)
	return a.withLocks(ctx, []string{key}, func(ctx context.Context) error {
		for {
			if err := cancelled(ctx); err != nil {
				return err
			}
			if err := a.checkWatchBinding(ctx, binding); err != nil {
				return err
			}
			result, err := a.watchPass(ctx, binding, root, local, remote, opts.Delete)
			if err != nil {
				return err
			}
			if err := emit(result); err != nil {
				return err
			}
			if opts.Once {
				return nil
			}
			timer := time.NewTimer(opts.Interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return cancelled(ctx)
			case <-timer.C:
			}
		}
	})
}

func (a *App) checkWatchBinding(ctx context.Context, b watchBinding) error {
	var account int64
	var peer, root string
	var topic int
	err := a.DB.Raw().QueryRowContext(ctx, `select account_id,tg_channel_id,topic_id,root_local_path from channels where id=?`, b.ch.rowID).Scan(&account, &peer, &topic, &root)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "read watch binding", err)
	}
	if account != b.account || peer != b.ch.tgIDStr || topic != b.ch.topicID || root != b.root {
		return apperr.New(apperr.ErrPathConflict, "watch binding changed; select the intended root and restart explicitly")
	}
	return nil
}

func watchInventory(ctx context.Context, root *os.Root) (map[string]watchFile, error) {
	files := map[string]watchFile{}
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := cancelled(ctx); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return apperr.New(apperr.ErrPathInvalid, "watch source contains a symlink or special file: "+name)
		}
		canonical, err := fsmodel.NormalizeCanonicalPath("/" + filepath.ToSlash(name))
		if err != nil {
			return err
		}
		if canonical != "/"+filepath.ToSlash(name) {
			return apperr.New(apperr.ErrPathInvalid, "watch source name is not a canonical remote path: "+name)
		}
		f, err := root.Open(name)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return apperr.New(apperr.ErrPathInvalid, "watch source changed type: "+name)
		}
		hash, err := computeHash(contextReader{ctx: ctx, r: f}, true)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)] = watchFile{hash: hash, size: info.Size()}
		return nil
	})
	return files, err
}

func (a *App) watchPass(ctx context.Context, b watchBinding, root *os.Root, local, remote string, remove bool) (WatchResult, error) {
	result := WatchResult{LocalRoot: local, RemoteRoot: remote}
	files, err := watchInventory(ctx, root)
	if err != nil {
		return result, err
	}
	baseline, err := a.DB.WatchCheckpoints(ctx, b.ch.rowID, local, remote)
	if err != nil {
		return result, err
	}
	names := map[string]bool{}
	for name := range files {
		names[name] = true
	}
	for name := range baseline {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	paths := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
		paths = append(paths, path.Join(remote, name))
	}
	sort.Slice(ordered, func(i, j int) bool {
		_, left := files[ordered[i]]
		_, right := files[ordered[j]]
		if left != right {
			return left
		}
		return ordered[i] < ordered[j]
	})
	err = a.operate(ctx, b.ch, paths, func(ctx context.Context) error {
		if err := a.checkWatchBinding(ctx, b); err != nil {
			return err
		}
		active, err := a.activePaths(ctx, b.ch.rowID)
		if err != nil {
			return err
		}
		types := make(map[string]bool, len(active))
		for _, entry := range active {
			types[entry.Canonical] = entry.IsDir
		}
		// Preflight every owned destination before the first write. Unowned remote
		// files can only be adopted when their actual bytes match the local file.
		current := map[string]bool{}
		for _, name := range ordered {
			target := path.Join(remote, name)
			desired, exists := files[name]
			old, owned := baseline[name]
			if !exists && !remove {
				result.Retained++
				continue
			}
			if types[target] {
				return apperr.New(apperr.ErrPathConflict, "watch target is a directory: "+target)
			}
			for _, ancestor := range fsmodel.AncestorPaths(target) {
				if isDir, exists := types[ancestor]; exists && !isDir {
					return apperr.New(apperr.ErrPathConflict, "watch target has a file ancestor: "+target)
				}
			}
			row, found, err := a.DB.ActiveByPath(ctx, b.ch.rowID, target)
			if err != nil {
				return apperr.Wrap(apperr.ErrDB, "read watch destination", err)
			}
			if !found {
				if owned && exists {
					return apperr.New(apperr.ErrPathConflict, "watched remote file disappeared: "+target)
				}
				continue
			}
			hash := old.Hash
			size := old.Size
			if exists && row.ContentHash.String == desired.hash && row.Size.Int64 == desired.size {
				hash = desired.hash
				size = desired.size
				current[name] = true
			} else if !owned {
				return apperr.New(apperr.ErrPathConflict, "unowned remote content differs: "+target)
			}
			verified, err := a.duplicateRow(ctx, b.ch.rowID, DuplicateFile{Path: target, Size: size}, hash)
			if err != nil {
				return err
			}
			h := &countingHasher{h: blake3.New(32, nil)}
			if err := a.TG.DownloadMedia(ctx, b.ch.tgID, int(verified.MessageID.Int64), h); err != nil {
				return telegram.MapError(err)
			}
			if h.n != size || "blake3:"+hex.EncodeToString(h.h.Sum(nil)) != hash {
				return apperr.New(apperr.ErrPathConflict, "watched remote bytes changed: "+target)
			}
		}
		for _, name := range ordered {
			if err := cancelled(ctx); err != nil {
				return err
			}
			if err := a.checkWatchBinding(ctx, b); err != nil {
				return err
			}
			target := path.Join(remote, name)
			desired, exists := files[name]
			if !exists {
				if !remove {
					continue
				}
				if _, found, err := a.DB.ActiveByPath(ctx, b.ch.rowID, target); err != nil {
					return err
				} else if found {
					if _, err := a.DeleteFile(ctx, target, DeleteOptions{Confirm: true}); err != nil {
						return err
					}
					result.Deleted++
				}
				if err := a.DB.DeleteWatchCheckpoint(ctx, b.ch.rowID, local, remote, name); err != nil {
					return err
				}
				continue
			}
			if current[name] {
				result.Unchanged++
			} else {
				snapshot, cleanup, err := watchSnapshot(ctx, root, name, desired)
				if err != nil {
					return err
				}
				policy := ConflictFail
				if _, owned := baseline[name]; owned {
					policy = ConflictReplace
				}
				uploaded, err := a.UploadFile(ctx, snapshot, target, policy, false, UploadOptions{ConfirmReplace: true})
				cleanup()
				if err != nil {
					return err
				}
				if uploaded.Path != target || uploaded.Hash != desired.hash || uploaded.Size != desired.size {
					return apperr.New(apperr.ErrPathConflict, "watch upload did not match planned destination and bytes")
				}
				result.Uploaded++
			}
			if err := a.DB.SaveWatchCheckpoint(ctx, b.ch.rowID, local, remote, name, desired.hash, desired.size); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func watchSnapshot(ctx context.Context, root *os.Root, name string, want watchFile) (string, func(), error) {
	dir, err := os.MkdirTemp("", "td-watch-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	source, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		cleanup()
		return "", nil, err
	}
	defer func() { _ = source.Close() }()
	target := filepath.Join(dir, path.Base(name))
	dest, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	h := blake3.New(32, nil)
	size, copyErr := io.Copy(io.MultiWriter(dest, h), contextReader{ctx: ctx, r: source})
	closeErr := dest.Close()
	if copyErr != nil {
		cleanup()
		return "", nil, copyErr
	}
	if closeErr != nil {
		cleanup()
		return "", nil, closeErr
	}
	if size != want.size || "blake3:"+hex.EncodeToString(h.Sum(nil)) != want.hash {
		cleanup()
		return "", nil, apperr.New(apperr.ErrPathConflict, "local file changed during watch pass: "+name)
	}
	return target, cleanup, nil
}
