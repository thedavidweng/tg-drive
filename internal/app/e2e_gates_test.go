package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestE2ESafetyGates pins every ADR 0003 gate and the repair-mode rules to
// their contract envelope and exit code on a live drive, and checks that a
// refused call changed nothing.
func TestE2ESafetyGates(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	original := e2eLocalFile(t, dir, "keep.txt", "original")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", original, "/gate/keep.txt")
	replacement := e2eLocalFile(t, dir, "replacement.txt", "replacement bytes")
	other := e2eLocalFile(t, dir, "other.txt", "other")
	srcDir := filepath.Join(dir, "tree")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	e2eLocalFile(t, srcDir, "keep.txt", "tree copy")

	cases := []struct {
		exit int
		code string
		args []string
	}{
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"cp", "--replace", replacement, "/gate/keep.txt"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"cp", "--replace", "--recursive", srcDir, "/gate"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"cp", "--replace", replacement, other, "/gate/"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"mv", "/gate/keep.txt", "/gate/moved.txt"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"rm", "/gate/keep.txt"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"rm", "--tombstone", "/gate/keep.txt"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"adopt", "--unmanaged"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"adopt", "--rewrite-captions"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"import", "saved", "--photos-as", "document"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"import", "saved", "--photos-as", "document", "--dry-run", "--delete-source"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"repair", "--orphaned", "--delete-orphaned"}},
		{2, "ERR_USAGE", []string{"repair", "--pending", "--orphaned"}},
		{2, "ERR_USAGE", []string{"repair", "--hash", "--captions", "/gate/keep.txt"}},
		{2, "ERR_USAGE", []string{"repair", "--scan-errors", "--hash"}},
		{2, "ERR_USAGE", []string{"repair", "--delete-orphaned", "--confirm"}},
		{2, "ERR_USAGE", []string{"repair", "--dry-run"}},
		// An empty path argument is still a path: it repairs "/", which is
		// never a file.
		{2, "ERR_REMOTE_NOT_FOUND", []string{"repair", ""}},
	}
	for _, tc := range cases {
		runE2EExpectError(t, bin, cfgPath, dbPath, statePath, tc.exit, tc.code, tc.args...)
	}

	ls := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", "/gate")
	entries, _ := ls["entries"].([]any)
	keep := findEntry(entries, "keep.txt")
	if len(entries) != 1 || keep == nil || keep["size"] != float64(len("original")) {
		t.Fatalf("refused calls changed /gate: %v", ls)
	}
}

// TestE2EGatesPrecedeAppContext checks that a refused call fails before td
// opens the index or the Telegram client: no credentials are configured, yet
// each command reports the gate, and no database is created.
func TestE2EGatesPrecedeAppContext(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "db.sqlite")
	local := e2eLocalFile(t, dir, "a.txt", "a")

	cases := []struct {
		exit int
		code string
		args []string
	}{
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"cp", "--replace", local, "/a.txt"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"mv", "/a.txt", "/b.txt"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"rm", "/a.txt"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"adopt", "--unmanaged"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"import", "saved"}},
		{10, "ERR_CONFIRMATION_REQUIRED", []string{"repair", "--orphaned", "--delete-orphaned"}},
		{2, "ERR_USAGE", []string{"repair", "--pending", "--orphaned"}},
	}
	for _, tc := range cases {
		args := append([]string{"--json", "--config", cfgPath, "--db", dbPath}, tc.args...)
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "TD_FAKE_TELEGRAM=")
		out, err := cmd.Output()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != tc.exit {
			t.Fatalf("%v: err = %v, want exit %d (stdout=%s)", tc.args, err, tc.exit, out)
		}
		var env e2eEnvelope
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("%v: bad JSON %q: %v", tc.args, out, err)
		}
		if env.OK || env.Error == nil || env.Error.Code != tc.code {
			t.Fatalf("%v: want error %s, got %s", tc.args, tc.code, out)
		}
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("a refused call opened the index: stat err = %v", err)
	}
}
