//go:build !windows

package fileperm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPrivateRejectsSharedOrSymlinkIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if file, err := OpenPrivate(path); err == nil {
		_ = file.Close()
		t.Fatal("shared identity accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if file, err := OpenPrivate(link); err == nil {
		_ = file.Close()
		t.Fatal("symlink identity accepted")
	}
}
