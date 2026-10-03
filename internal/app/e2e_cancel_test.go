//go:build !windows

package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Interrupts are delivered with os.Interrupt, which Windows cannot send to a
// child process; the cancellation path itself is cross-platform.

// cancelDeadline bounds how long td may take to exit after SIGINT. Every
// scenario leaves several seconds of fake transfer delay outstanding, so a
// process that ignores the signal cannot meet it.
const cancelDeadline = 2 * time.Second

// startTD starts the built binary against the fake with its stdout piped.
func startTD(t *testing.T, bin, cfgPath, dbPath, statePath string, extraEnv []string, args ...string) (*exec.Cmd, *bufio.Scanner, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--config", cfgPath, "--db", dbPath}, args...)...)
	cmd.Env = append(os.Environ(), "TD_FAKE_TELEGRAM=1", "TD_FAKE_TELEGRAM_STATE="+statePath,
		"TD_SESSION="+filepath.Join(filepath.Dir(statePath), "session.json"))
	cmd.Env = append(cmd.Env, extraEnv...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	return cmd, sc, &stderr
}

// interrupt sends SIGINT and kills td if it is still running well past
// cancelDeadline, so a td that ignores the signal fails the test instead of
// hanging it.
func interrupt(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	watchdog := time.AfterFunc(3*cancelDeadline, func() { _ = cmd.Process.Kill() })
	t.Cleanup(func() { watchdog.Stop() })
}

// interruptAndWait sends SIGINT, drains stdout, and returns the remaining
// stdout lines, the exit code, and how long td took to exit.
func interruptAndWait(t *testing.T, cmd *exec.Cmd, sc *bufio.Scanner) ([]string, int, time.Duration) {
	t.Helper()
	sent := time.Now()
	interrupt(t, cmd)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	err := cmd.Wait()
	return lines, exitCode(t, err), time.Since(sent)
}

func assertCancelled(t *testing.T, lines []string, code int, took time.Duration, stderr string) {
	t.Helper()
	if took > cancelDeadline {
		t.Fatalf("td took %s to exit after SIGINT, want under %s", took, cancelDeadline)
	}
	if code != 130 {
		t.Fatalf("exit code = %d, want 130; stdout=%v stderr=%s", code, lines, stderr)
	}
	if len(lines) == 0 {
		t.Fatalf("no error envelope on stdout; stderr=%s", stderr)
	}
	env := decodeError(t, lines[len(lines)-1])
	if env.Error.Code != "ERR_CANCELLED" {
		t.Fatalf("error code = %s, want ERR_CANCELLED (%s)", env.Error.Code, env.Error.Message)
	}
}

// TestE2EInterruptedCpResumes: SIGINT during a large td cp exits promptly
// with ERR_CANCELLED, ends its Transfer cancelled, keeps the
// resumable upload state and releases the
// path's Operation lock, so the next td cp resumes and sends only the parts
// that were never confirmed.
func TestE2EInterruptedCpResumes(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	const totalParts = 12
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), totalParts*mib)
	args := []string{"--json", "cp", big, "/big.bin", "--events", "--upload-part-size-kb", "1024", "--upload-threads", "1"}

	cmd, sc, stderr := startTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=400ms"}, args...)
	sawProgress := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"command":"cp.progress"`) {
			sawProgress = true
			break
		}
	}
	if !sawProgress {
		t.Fatalf("no cp.progress before exit; stderr=%s", stderr)
	}
	lines, code, took := interruptAndWait(t, cmd, sc)
	assertCancelled(t, lines, code, took, stderr.String())

	cancelled := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "cancelled")
	if len(cancelled) != 1 || cancelled[0]["finished_at"] == nil || cancelled[0]["error_code"] != nil {
		t.Fatalf("cancelled transfers after SIGINT = %v, want the one cp ended cancelled without an error", cancelled)
	}
	failed := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "failed")
	if len(failed) != 0 {
		t.Fatalf("failed transfers after SIGINT = %v, want none: Ctrl-C cancels, it does not fail", failed)
	}

	status := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "status")
	if status["upload_states"] != float64(1) {
		t.Fatalf("status upload_states = %v, want 1 kept for resume", status["upload_states"])
	}

	stdout, stderr2, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", args...)
	if err != nil {
		t.Fatalf("resumed cp: %v stdout=%s stderr=%s", err, stdout, stderr2)
	}
	evs := strings.Split(strings.TrimSpace(stdout), "\n")
	progress := 0
	for _, l := range evs {
		if strings.Contains(l, `"command":"cp.progress"`) {
			progress++
		}
	}
	if progress == 0 || progress >= totalParts {
		t.Fatalf("resumed cp sent %d parts, want between 1 and %d", progress, totalParts-1)
	}
	var final struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(evs[len(evs)-1]), &final); err != nil {
		t.Fatal(err)
	}
	if !final.OK || final.Data["resumed"] != true {
		t.Fatalf("resumed cp result = %s, want ok with resumed:true", evs[len(evs)-1])
	}
}

// TestE2EInterruptedRecursiveGet: SIGINT during td get --recursive stops it
// between files: td exits promptly with ERR_CANCELLED and leaves no partial
// download behind.
func TestE2EInterruptedRecursiveGet(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const files = 6
	src := filepath.Join(dir, "tree")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range files {
		e2eLocalFile(t, src, fmt.Sprintf("f%d.txt", i), fmt.Sprintf("file %d", i))
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", "--recursive", src, "/tree")

	out := filepath.Join(dir, "out")
	cmd, sc, stderr := startTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=500ms"},
		"--json", "get", "--recursive", "--continue-on-error", "/tree", out)
	first := filepath.Join(out, "f0.txt")
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(first); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first file never downloaded; stderr=%s", stderr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	lines, code, took := interruptAndWait(t, cmd, sc)
	assertCancelled(t, lines, code, took, stderr.String())

	cancelled := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "cancelled")
	tr := findTransfer(t, cancelled, "recursive_download")
	if tr["finished_at"] == nil || tr["error_code"] != nil {
		t.Fatalf("transfer after SIGINT = %v, want the recursive download cancelled without an error", tr)
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) >= files {
		t.Fatalf("downloaded %d entries, want the download stopped before all %d files", len(entries), files)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("partial download %s left behind", e.Name())
		}
	}
}

// TestE2EInterruptedPrompt: SIGINT while td waits at an interactive prompt
// (the login code) exits promptly with ERR_CANCELLED instead of waiting for
// input that never comes.
func TestE2EInterruptedPrompt(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, _ := e2eSetup(t, dir)

	cmd := exec.Command(bin, "--config", cfgPath, "--db", dbPath, "--json", "auth", "login")
	cmd.Env = append(os.Environ(), "TD_FAKE_TELEGRAM=1", "TD_FAKE_TELEGRAM_STATE="+statePath,
		"TD_SESSION="+filepath.Join(filepath.Dir(statePath), "session.json"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(stderr.String(), "code: "); {
		if time.Now().After(deadline) {
			t.Fatalf("login never prompted for the code; stderr=%s", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	sent := time.Now()
	interrupt(t, cmd)
	waitErr := cmd.Wait()
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	assertCancelled(t, lines, exitCode(t, waitErr), time.Since(sent), stderr.String())
}

// syncBuffer is a bytes.Buffer safe for a child process writer and a
// polling reader.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
