package app

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// TestE2ESessionLock: while one process holds a connected session (a login
// waiting for its code), a second process that needs Telegram waits for the
// configured bound and then fails with ERR_SESSION_LOCKED, while an
// index-only command succeeds at once. Once the holder exits, the session is
// free again.
func TestE2ESessionLock(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, "a.txt", "a"), "/a.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "locks.session_wait_seconds", "1")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "auth", "logout")

	holder := tdCommand(bin, cfgPath, dbPath, statePath, nil, "auth", "login")
	codeIn, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	holderErr, err := holder.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	})
	waitForOutput(t, holderErr, "code: ")

	start := time.Now()
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 5, "ERR_SESSION_LOCKED", "auth", "status")
	if waited := time.Since(start); waited < time.Second {
		t.Fatalf("second process failed after %s, want it to wait the 1s bound", waited)
	}

	ls := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", "/")
	if findEntry(ls["entries"], "a.txt") == nil {
		t.Fatalf("ls / while session locked = %v", ls)
	}

	if _, err := io.WriteString(codeIn, "12345\n"); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err != nil {
		t.Fatalf("holder login: %v", err)
	}
	status := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "auth", "status")
	if status["authenticated"] != true {
		t.Fatalf("auth status after holder exit = %v", status)
	}
}

// waitForOutput reads r until want appears, failing after a generous bound.
// The reader keeps draining in the background so the child never blocks on
// a full stderr pipe.
func waitForOutput(t *testing.T, r io.Reader, want string) {
	t.Helper()
	found := make(chan struct{})
	go func() {
		var seen bytes.Buffer
		br := bufio.NewReader(r)
		signalled := false
		for {
			b, err := br.ReadByte()
			if err != nil {
				return
			}
			seen.WriteByte(b)
			if !signalled && strings.Contains(seen.String(), want) {
				signalled = true
				close(found)
			}
		}
	}()
	select {
	case <-found:
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}
