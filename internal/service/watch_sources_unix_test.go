//go:build !windows

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func TestFilesystemWatchRejectsSymlinksAndNoncanonicalNames(t *testing.T) {
	for _, kind := range []string{"symlink", "backslash"} {
		t.Run(kind, func(t *testing.T) {
			a, tg := testApp(t)
			loginAndInit(t, a, tg)
			local := t.TempDir()
			if kind == "symlink" {
				if err := os.Symlink(writeLocal(t, "outside"), filepath.Join(local, "link")); err != nil {
					t.Fatal(err)
				}
			} else {
				watchWrite(t, local, "a\\b.txt", "must not become a subdirectory")
			}
			if _, err := watchOnce(t, a, context.Background(), local, "/sync", false); appErrCode(t, err) != apperr.ErrPathInvalid {
				t.Fatalf("source error=%v", err)
			}
			entries, err := a.ListDir(context.Background(), "/")
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid source mutated destination: %+v %v", entries, err)
			}
		})
	}
}
