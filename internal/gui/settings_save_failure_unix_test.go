//go:build gui && (linux || darwin)

package gui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
)

func TestSettingsPersistFailureKeepsRunningGeneration(t *testing.T) {
	if os.Getenv("TD_TEST_GUI_WRITE_FAILURE") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSettingsPersistFailureKeepsRunningGeneration$")
		cmd.Env = append(os.Environ(), "TD_TEST_GUI_WRITE_FAILURE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		return
	}
	svc := openTestGUI(t)
	ctx := context.Background()
	if _, err := svc.Settings.Set(ctx, "upload.threads", "3"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	app, manager := svc.state.current(), svc.state.currentManager()
	signal.Ignore(syscall.SIGXFSZ)
	var prev syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &prev); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &prev); err != nil {
			t.Error(err)
		}
	})
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 1, Max: prev.Max}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Settings.Set(ctx, "upload.threads", "5"); err == nil {
		t.Fatal("Set succeeded despite write failure")
	}
	after, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || svc.state.current() != app || svc.state.currentManager() != manager || app.Cfg.Upload.Threads != 3 {
		t.Fatal("failed write changed disk or running generation")
	}
}
