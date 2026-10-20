package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"time"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
)

// ReadTree exposes one pinned drive subtree as a read-only, seekable filesystem.
// Each operation checks that its durable binding has not been reassigned.
type ReadTree struct {
	app      *App
	binding  watchBinding
	remote   string
	ctx      context.Context
	lifetime context.Context
}

type readChannelKey struct{}

func (a *App) OpenReadTree(ctx context.Context, remote string) (*ReadTree, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	b := watchBinding{ch: ch}
	if err := a.DB.Raw().QueryRowContext(ctx, `select account_id,root_local_path from channels where id=?`, ch.rowID).Scan(&b.account, &b.root); err != nil {
		return nil, err
	}
	// Use the same canonical path contract as other drive operations.
	remote, err = fsmodel.NormalizeCanonicalPath(remote)
	if err != nil {
		return nil, err
	}
	tree := &ReadTree{app: a, binding: b, remote: remote, ctx: ctx, lifetime: ctx}
	info, err := fs.Stat(tree, ".")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "open", Path: remote, Err: fs.ErrInvalid}
	}
	return tree, nil
}

func (t *ReadTree) WithContext(ctx context.Context) *ReadTree {
	copy := *t
	copy.ctx = ctx
	return &copy
}

func (t *ReadTree) checkBinding() error {
	if err := cancelled(t.ctx); err != nil {
		return err
	}
	if err := cancelled(t.lifetime); err != nil {
		return err
	}
	return t.app.checkWatchBinding(t.ctx, t.binding)
}

func (t *ReadTree) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if err := t.checkBinding(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(t.ctx)
	stop := context.AfterFunc(t.lifetime, cancel)
	closeContext := func() { stop(); cancel() }
	opened := false
	defer func() {
		if !opened {
			closeContext()
		}
	}()
	perFile := *t
	perFile.ctx = ctx
	t = &perFile
	ctx = context.WithValue(t.ctx, readChannelKey{}, t.binding.ch)
	target := path.Join(t.remote, name)
	preview, err := t.app.ResolvePreview(ctx, target)
	if err == nil {
		info, err := t.app.ReadPreviewRange(ctx, preview, 0, 0, io.Discard)
		if err != nil {
			return nil, err
		}
		if !info.Seekable {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
		}
		opened = true
		return &readTreeFile{tree: t, preview: preview, info: treeInfo{name: path.Base(name), size: info.Size, updated: preview.UpdatedAt}, closeContext: closeContext}, nil
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.ErrRemoteNotFound {
		return nil, err
	}
	entries, err := t.app.ListDir(ctx, target)
	if err != nil {
		if errors.As(err, &appErr) && appErr.Code == apperr.ErrRemoteNotFound {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		return nil, err
	}
	opened = true
	return &readTreeFile{tree: t, info: treeInfo{name: path.Base(name), dir: true}, entries: entries, closeContext: closeContext}, nil
}

type treeInfo struct {
	name    string
	size    int64
	dir     bool
	updated string
}

func (i treeInfo) Name() string { return i.name }
func (i treeInfo) Size() int64  { return i.size }
func (i treeInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

func (i treeInfo) ModTime() time.Time {
	parsed, err := time.Parse(time.RFC3339, i.updated)
	if err != nil {
		return time.Unix(0, 0)
	}
	return parsed
}
func (i treeInfo) IsDir() bool { return i.dir }
func (i treeInfo) Sys() any    { return nil }

type readTreeFile struct {
	tree         *ReadTree
	preview      PreviewFile
	info         treeInfo
	entries      []LSEntry
	offset       int64
	position     int
	closed       bool
	closeContext func()
}

func (f *readTreeFile) Close() error {
	f.closed = true
	f.closeContext()
	return nil
}

func (f *readTreeFile) Stat() (fs.FileInfo, error) {
	if f.closed {
		return nil, fs.ErrClosed
	}
	return f.info, nil
}

func (f *readTreeFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.info.dir {
		return 0, fs.ErrInvalid
	}
	if len(p) == 0 {
		return 0, nil
	}
	if f.offset >= f.info.size {
		return 0, io.EOF
	}
	if err := f.tree.checkBinding(); err != nil {
		return 0, err
	}
	length := min(int64(len(p)), f.info.size-f.offset)
	var buf bytes.Buffer
	_, err := f.tree.app.ReadPreviewRange(f.tree.ctx, f.preview, f.offset, length, &buf)
	n := copy(p, buf.Bytes())
	f.offset += int64(n)
	if err != nil {
		return n, err
	}
	if int64(n) != length {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func (f *readTreeFile) Seek(offset int64, whence int) (int64, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.info.dir {
		return 0, fs.ErrInvalid
	}
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.offset
	case io.SeekEnd:
		offset += f.info.size
	default:
		return 0, fs.ErrInvalid
	}
	if offset < 0 {
		return 0, fs.ErrInvalid
	}
	f.offset = offset
	return offset, nil
}

func (f *readTreeFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if f.closed {
		return nil, fs.ErrClosed
	}
	if !f.info.dir {
		return nil, fs.ErrInvalid
	}
	if err := f.tree.checkBinding(); err != nil {
		return nil, err
	}
	if f.position == len(f.entries) && n > 0 {
		return nil, io.EOF
	}
	end := len(f.entries)
	if n > 0 {
		end = min(end, f.position+n)
	}
	out := make([]fs.DirEntry, 0, end-f.position)
	for _, e := range f.entries[f.position:end] {
		out = append(out, fs.FileInfoToDirEntry(treeInfo{name: path.Base(e.Path), size: e.Size, dir: e.Type == "dir", updated: e.UpdatedAt}))
	}
	f.position = end
	return out, nil
}
