package atomicdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishDoesNotReplaceExistingEmptyDirectory(t *testing.T) {
	parent := t.TempDir()
	staging := filepath.Join(parent, "staging")
	dest := filepath.Join(parent, "destination")
	for _, p := range []string{staging, dest} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(staging, "verified"), []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Publish(staging, dest); err == nil {
		t.Fatal("existing empty destination overwritten")
	}
	if _, err := os.Stat(filepath.Join(staging, "verified")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err := Publish(staging, dest); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "verified")); err != nil || string(data) != "bytes" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}
