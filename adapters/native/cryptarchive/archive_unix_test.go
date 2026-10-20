//go:build !windows

package cryptarchive

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func TestArchiveRejectsSourceSymlink(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(context.Background(), source, io.Discard, identity.Recipient()); err == nil {
		t.Fatal("symlink source accepted")
	}
}
