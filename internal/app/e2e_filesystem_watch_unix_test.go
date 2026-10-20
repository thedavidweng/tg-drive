//go:build !windows

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestE2EFilesystemWatchSIGINTAndRestart(t *testing.T) {
	dir := t.TempDir()
	bin, cfg, db, state, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfg, db, state)
	runE2EJSON(t, bin, cfg, db, state, "init", root, "--create-channel=Drive")
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	e2eLocalFile(t, source, "file.txt", "watch cancellation")
	cmd, scanner, stderr := startTD(t, bin, cfg, db, state, nil, "--json", "watch", source, "/sync", "--confirm", "--interval=1h")
	watchdog := time.AfterFunc(20*time.Second, func() { _ = cmd.Process.Kill() })
	defer watchdog.Stop()
	if !scanner.Scan() {
		t.Fatalf("no watch pass: %s", stderr)
	}
	var envelope e2eEnvelope
	if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil || !envelope.OK || envelope.Data["uploaded"] != float64(1) {
		t.Fatalf("first line=%s err=%v", scanner.Text(), err)
	}
	lines, code, took := interruptAndWait(t, cmd, scanner)
	assertCancelled(t, lines, code, took, stderr.String())
	result := runE2EJSON(t, bin, cfg, db, state, "watch", source, "/sync", "--confirm", "--once")
	if result["unchanged"] != float64(1) {
		t.Fatalf("resume=%v", result)
	}
}
