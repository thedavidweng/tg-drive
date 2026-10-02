//go:build !windows

package app

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Cross-process cancel and interruption are driven by the Transfer owner's
// lease heartbeat, whose TTL is locks.ttl_seconds; the tests shorten it
// through the config so the owner polls within the test's deadlines.

// TestE2ECrossProcessCancel: td transfers cancel from a second process
// stops a running td cp: the command exits promptly with ERR_CANCELLED and
// the Transfer ends cancelled, its cancel flag recorded. The cancelling
// process reads and writes only the index, so it acts while the uploading
// process holds the Session lock.
func TestE2ECrossProcessCancel(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "locks.ttl_seconds", "3")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	cmd, sc, stderr := startTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=2s"},
		"--json", "cp", big, "/big.bin", "--upload-part-size-kb", "1024", "--upload-threads", "1")

	var running map[string]any
	for deadline := time.Now().Add(10 * time.Second); ; {
		active := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "uploading")
		if len(active) > 0 {
			running = active[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cp never reached uploading; stderr=%s", stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	id, _ := running["id"].(string)

	asked := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "cancel", id)
	if asked["id"] != id || asked["cancel_requested"] != true || asked["stage"] != "uploading" {
		t.Fatalf("transfers cancel = %v, want the running Transfer with cancel_requested set", asked)
	}
	// Cancelling again while the Transfer still runs is idempotent.
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "cancel", id)

	sent := time.Now()
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	code := exitCode(t, cmd.Wait())
	if took := time.Since(sent); took > 4*time.Second {
		t.Fatalf("td took %s to stop after cancel, want under 4s (about one cancel poll)", took)
	}
	if code != 130 {
		t.Fatalf("exit code = %d, want 130; stdout=%v stderr=%s", code, lines, stderr)
	}
	env := decodeError(t, lines[len(lines)-1])
	if env.Error.Code != "ERR_CANCELLED" {
		t.Fatalf("error code = %s, want ERR_CANCELLED (%s)", env.Error.Code, env.Error.Message)
	}

	done := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "show", id)
	if done["stage"] != "cancelled" || done["finished_at"] == nil || done["error_code"] != nil {
		t.Fatalf("transfer after cancel = %v, want cancelled without an error", done)
	}

	// A terminal Transfer cannot be cancelled; an unknown ID is not found.
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE", "transfers", "cancel", id)
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_TRANSFER_NOT_FOUND",
		"transfers", "cancel", "00000000-0000-4000-8000-000000000000")

	// Human output names the Transfer the cancel was requested for.
	cmd2, sc2, stderr2 := startTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=2s"},
		"cp", big, "/big2.bin", "--upload-part-size-kb", "1024", "--upload-threads", "1")
	defer func() {
		_ = cmd2.Process.Kill()
		for sc2.Scan() {
		}
		_ = cmd2.Wait()
	}()
	var id2 string
	for deadline := time.Now().Add(10 * time.Second); ; {
		active := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "uploading")
		if len(active) > 0 {
			id2, _ = active[0]["id"].(string)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("second cp never reached uploading; stderr=%s", stderr2)
		}
		time.Sleep(20 * time.Millisecond)
	}
	human, stderrHuman, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "transfers", "cancel", id2)
	if err != nil {
		t.Fatalf("human cancel: %v stderr=%s", err, stderrHuman)
	}
	if want := fmt.Sprintf("cancel requested for transfer %s\n", id2); human != want {
		t.Fatalf("human cancel = %q, want %q", human, want)
	}
}

// TestE2EInterruptAfterKill: a td cp killed with SIGKILL never renews its
// lease again; once the lease expires, another process's td transfers list
// marks the Transfer interrupted (its owner token left for a retry), and
// td transfers show agrees. While the owner lives, its heartbeat keeps the
// lease ahead of every read.
func TestE2EInterruptAfterKill(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	// A one-second lease expires about a second after the owner dies.
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "locks.ttl_seconds", "1")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	cmd, _, stderr := startTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=2s"},
		"--json", "cp", big, "/big.bin", "--upload-part-size-kb", "1024", "--upload-threads", "1")

	var id string
	for deadline := time.Now().Add(10 * time.Second); ; {
		active := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "uploading")
		if len(active) > 0 {
			id, _ = active[0]["id"].(string)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cp never reached uploading; stderr=%s", stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// SIGKILL: no cleanup runs, so the Transfer stays uploading until its
	// lease expires.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	var interrupted map[string]any
	for deadline := time.Now().Add(15 * time.Second); ; {
		list := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "interrupted")
		if len(list) > 0 {
			interrupted = list[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfer never marked interrupted; stderr=%s", stderr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if interrupted["id"] != id || interrupted["finished_at"] == nil {
		t.Fatalf("interrupted transfer = %v, want the killed cp's Transfer %s ended", interrupted, id)
	}
	shown := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "show", id)
	if shown["stage"] != "interrupted" {
		t.Fatalf("transfers show = %v, want interrupted", shown)
	}
	// Nothing is uploading any more, and the interrupted Transfer is
	// terminal: no list filter brings it back active.
	if active := listTransfers(t, bin, cfgPath, dbPath, statePath); len(active) != 0 {
		t.Fatalf("active transfers = %v, want none", active)
	}
}
