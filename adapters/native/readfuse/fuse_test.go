//go:build linux || darwin

package readfuse

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"testing/fstest"

	fusefs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestReadOnlyFuseWithoutKernelMount(t *testing.T) {
	ctx := context.Background()
	files := fstest.MapFS{"file.txt": &fstest.MapFile{Data: []byte("0123456789"), Mode: 0o444}}
	root := NewRoot(func(context.Context) fs.FS { return files }).(*node)
	fusefs.NewNodeFS(root, &fusefs.Options{})
	var entry fuse.EntryOut
	child, code := root.Lookup(ctx, "file.txt", &entry)
	if code != 0 || entry.Size != 10 || entry.Mode&0o222 != 0 {
		t.Fatalf("lookup=%+v errno=%v", entry, code)
	}
	n := child.Operations().(*node)
	if _, _, code := n.Open(ctx, uint32(os.O_WRONLY)); code != syscall.EROFS {
		t.Fatalf("write open=%v", code)
	}
	result, code := n.Read(ctx, nil, make([]byte, 3), 6)
	if code != 0 {
		t.Fatal(code)
	}
	defer result.Done()
	bytes, readCode := result.Bytes(nil)
	if readCode != 0 || string(bytes) != "678" {
		t.Fatalf("range=%q errno=%v", bytes, readCode)
	}
	if _, code := root.Lookup(ctx, "../outside", &entry); code != syscall.EINVAL {
		t.Fatalf("escape=%v", code)
	}
	entries, code := root.Readdir(ctx)
	if code != 0 {
		t.Fatal(code)
	}
	defer entries.Close()
	if !entries.HasNext() {
		t.Fatal("empty tree")
	}
	item, code := entries.Next()
	if code != 0 || item.Name != "file.txt" {
		t.Fatalf("entry=%+v errno=%v", item, code)
	}
}

func TestMountRefusesPopulatedOrSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owned")
	if err := os.WriteFile(file, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := func(context.Context) fs.FS { return fstest.MapFS{} }
	if err := Serve(context.Background(), dir, files); !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("populated target=%v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), link, files); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("symlink target=%v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "preserved" {
		t.Fatalf("target altered: %q %v", data, err)
	}
}
