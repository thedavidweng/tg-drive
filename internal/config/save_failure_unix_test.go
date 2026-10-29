//go:build linux || darwin

package config

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSavePreservesFileOnWriteFailure(t *testing.T) {
	if path := os.Getenv("TD_TEST_CONFIG_WRITE_FAILURE"); path != "" {
		signal.Ignore(syscall.SIGXFSZ)
		var prev syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &prev); err != nil {
			t.Fatal(err)
		}
		// Only the soft limit moves: Max stays untouched so restoring Cur
		// afterwards is always permitted for an unprivileged process.
		t.Cleanup(func() {
			if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &prev); err != nil {
				t.Error(err)
			}
		})
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 1, Max: prev.Max}); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, Defaults()); err == nil {
			t.Fatal("Save succeeded despite the file size limit")
		}
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := []byte("[upload]\nthreads = 7\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSavePreservesFileOnWriteFailure$")
	cmd.Env = append(os.Environ(), "TD_TEST_CONFIG_WRITE_FAILURE="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("failed save changed the existing file: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("failed save left temporary files: %v", entries)
	}
}
