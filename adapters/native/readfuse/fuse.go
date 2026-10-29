//go:build linux || darwin

// Package readfuse exposes a read-only drive tree through the native FUSE protocol.
package readfuse

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	fusefs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type node struct {
	fusefs.Inode
	files func(context.Context) fs.FS
	name  string
}

func NewRoot(files func(context.Context) fs.FS) fusefs.InodeEmbedder {
	return &node{files: files, name: "."}
}

func status(err error) syscall.Errno {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, fs.ErrNotExist):
		return syscall.ENOENT
	case errors.Is(err, fs.ErrPermission):
		return syscall.EACCES
	case errors.Is(err, fs.ErrInvalid):
		return syscall.EINVAL
	case errors.Is(err, context.Canceled):
		return syscall.EINTR
	default:
		return syscall.EIO
	}
}

func fill(info fs.FileInfo, out *fuse.Attr) {
	out.Size = uint64(info.Size())
	out.Mode = syscall.S_IFREG | 0o444
	if info.IsDir() {
		out.Mode = syscall.S_IFDIR | 0o555
	}
	out.Nlink = 1
	out.SetTimes(nil, nil, nil)
}

func (n *node) Getattr(ctx context.Context, _ fusefs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	info, err := fs.Stat(n.files(ctx), n.name)
	if err != nil {
		return status(err)
	}
	fill(info, &out.Attr)
	return 0
}

func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fusefs.Inode, syscall.Errno) {
	if name == "." || !fs.ValidPath(name) || strings.Contains(name, "/") {
		return nil, syscall.EINVAL
	}
	child := path.Join(n.name, name)
	info, err := fs.Stat(n.files(ctx), child)
	if err != nil {
		return nil, status(err)
	}
	fill(info, &out.Attr)
	return n.NewInode(ctx, &node{files: n.files, name: child}, fusefs.StableAttr{Mode: out.Mode & syscall.S_IFMT}), 0
}

func (n *node) Readdir(ctx context.Context) (fusefs.DirStream, syscall.Errno) {
	entries, err := fs.ReadDir(n.files(ctx), n.name)
	if err != nil {
		return nil, status(err)
	}
	out := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		mode := uint32(syscall.S_IFREG)
		if e.IsDir() {
			mode = syscall.S_IFDIR
		}
		out = append(out, fuse.DirEntry{Name: e.Name(), Mode: mode})
	}
	return fusefs.NewListDirStream(out), 0
}

func (n *node) Open(ctx context.Context, flags uint32) (fusefs.FileHandle, uint32, syscall.Errno) {
	if flags&uint32(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		return nil, 0, syscall.EROFS
	}
	info, err := fs.Stat(n.files(ctx), n.name)
	if err != nil {
		return nil, 0, status(err)
	}
	if info.IsDir() {
		return nil, 0, syscall.EISDIR
	}
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (n *node) Read(ctx context.Context, _ fusefs.FileHandle, dest []byte, offset int64) (fuse.ReadResult, syscall.Errno) {
	if offset < 0 {
		return nil, syscall.EINVAL
	}
	file, err := n.files(ctx).Open(n.name)
	if err != nil {
		return nil, status(err)
	}
	defer func() { _ = file.Close() }()
	seeker, ok := file.(io.Seeker)
	if !ok {
		return nil, syscall.ENOTSUP
	}
	if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
		return nil, status(err)
	}
	count, err := io.ReadFull(file, dest)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, status(err)
	}
	return fuse.ReadResultData(dest[:count]), 0
}

func Serve(ctx context.Context, mountpoint string, files func(context.Context) fs.FS) error {
	info, err := os.Lstat(mountpoint)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &fs.PathError{Op: "mount", Path: mountpoint, Err: fs.ErrInvalid}
	}
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return &fs.PathError{Op: "mount", Path: mountpoint, Err: syscall.ENOTEMPTY}
	}
	mountpoint, err = filepath.Abs(mountpoint)
	if err != nil {
		return err
	}
	server, err := fusefs.Mount(mountpoint, NewRoot(files), &fusefs.Options{Name: "tg-drive", FsName: "tg-drive", Options: []string{"ro"}})
	if err != nil {
		return err
	}
	done := make(chan struct{})
	go func() { server.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if err := server.Unmount(); err != nil {
			return err
		}
		<-done
		return ctx.Err()
	}
}
