package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestE2EStatusBeforeAndAfterInit: status is the first thing a new user
// runs, so it must succeed before init and name the bound channel and local
// root afterwards.
func TestE2EStatusBeforeAndAfterInit(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)

	before := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "status")
	if before["initialized"] != false {
		t.Fatalf("status before init = %v, want initialized=false", before)
	}
	if chans, _ := before["channels"].([]any); len(chans) != 0 {
		t.Fatalf("status before init channels = %v, want empty", before["channels"])
	}
	stdout, _, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "status")
	if err != nil {
		t.Fatalf("human status before init: %v", err)
	}
	if !strings.Contains(stdout, "td init") {
		t.Fatalf("human status before init should point at td init, got:\n%s", stdout)
	}

	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	after := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "status")
	if after["initialized"] != true {
		t.Fatalf("status after init = %v", after)
	}
	ch, _ := after["channel"].(map[string]any)
	if ch["title"] != "Drive" || ch["local_root"] != root {
		t.Fatalf("status channel = %v, want title Drive and local_root %s", ch, root)
	}
	if chans, _ := after["channels"].([]any); len(chans) != 1 {
		t.Fatalf("status channels = %v, want 1", after["channels"])
	}
	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "status")
	if err != nil {
		t.Fatalf("human status: %v", err)
	}
	for _, want := range []string{"Drive", root} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("human status missing %q:\n%s", want, stdout)
		}
	}

	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 4, "ERR_CHANNEL_NOT_FOUND", "--channel", "nope", "status")
}

// TestE2EStatusAbbreviatesHome: human output is what users paste into bug
// reports, so it must not leak the home directory (and so the username).
func TestE2EStatusAbbreviatesHome(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, _, statePath, root := e2eSetup(t, dir)
	dbPath := filepath.Join(dir, ".local", "share", "td.db")
	env := []string{"HOME=" + dir}
	if _, _, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "12345\n", "auth", "login"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, _, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "", "init", root, "--create-channel=Drive"); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout, _, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(stdout, dir) {
		t.Fatalf("human status leaks home dir %s:\n%s", dir, stdout)
	}
	if !strings.Contains(stdout, "~/.local/share/td.db") {
		t.Fatalf("human status should show ~/.local/share/td.db:\n%s", stdout)
	}
}

// TestE2EVerboseDiagnostics: --verbose and TD_VERBOSE write diagnostics to
// stderr only, never secrets, and never disturb the JSON on stdout.
func TestE2EVerboseDiagnostics(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, _ := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)

	for name, tc := range map[string]struct {
		env  []string
		args []string
	}{
		"flag": {nil, []string{"--verbose", "--json", "auth", "status"}},
		"env":  {[]string{"TD_VERBOSE=1"}, []string{"--json", "auth", "status"}},
	} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, tc.env, "", tc.args...)
			if err != nil {
				t.Fatalf("%v: %v stderr=%s", tc.args, err, stderr)
			}
			var env e2eEnvelope
			if err := json.Unmarshal([]byte(stdout), &env); err != nil || !env.OK {
				t.Fatalf("stdout must stay one JSON envelope, got %q", stdout)
			}
			if !strings.Contains(stderr, "debug:") || !strings.Contains(stderr, "config") || !strings.Contains(stderr, "db") {
				t.Fatalf("verbose stderr should describe config and db, got:\n%s", stderr)
			}
			for _, secret := range []string{"deadbeef", "+15551234567"} {
				if strings.Contains(stderr, secret) || strings.Contains(stdout, secret) {
					t.Fatalf("verbose output leaks %q:\nstdout=%s\nstderr=%s", secret, stdout, stderr)
				}
			}
		})
	}

	_, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "--json", "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "debug:") {
		t.Fatalf("non-verbose run printed diagnostics:\n%s", stderr)
	}
}

// TestE2EPrivateFilePermissions: the DB holds local paths and the file tree,
// so it gets the same owner-only treatment as the session and config.
func TestE2EPrivateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes only")
	}
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, "a.txt", "a"), "/a.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "hash.enabled", "true")

	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", cfgPath} {
		info, err := os.Stat(p)
		if os.IsNotExist(err) && p != dbPath && p != cfgPath {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("%s mode = %o, want owner-only", p, perm)
		}
	}

	doctor := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "doctor")
	checks, _ := doctor["checks"].(map[string]any)
	if checks["file_permissions"] != "pass" {
		t.Fatalf("doctor file_permissions = %v", checks["file_permissions"])
	}
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatal(err)
	}
	// Opening the DB re-tightens it, so a loosened file is repaired by any
	// command, doctor included.
	doctor = runE2EJSON(t, bin, cfgPath, dbPath, statePath, "doctor")
	checks, _ = doctor["checks"].(map[string]any)
	if checks["file_permissions"] != "pass" {
		t.Fatalf("doctor after chmod = %v", checks["file_permissions"])
	}
	if info, _ := os.Stat(dbPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("db not re-tightened: %o", info.Mode().Perm())
	}
}

// TestE2EDoctorChecks covers the doctor checks the product spec requires
// beyond capabilities: WAL mode, a history read, and a readable summary.
func TestE2EDoctorChecks(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	doctor := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "doctor")
	checks, _ := doctor["checks"].(map[string]any)
	for _, name := range []string{"db_wal", "history_read"} {
		if checks[name] != "pass" {
			t.Fatalf("doctor %s = %v (all: %v)", name, checks[name], checks)
		}
	}
	// A free-tier account is the normal case, not a warning.
	if checks["file_size_limit"] != "pass" {
		t.Fatalf("file_size_limit = %v", checks["file_size_limit"])
	}

	stdout, _, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "passed") {
		t.Fatalf("human doctor needs a summary line:\n%s", stdout)
	}
}

// TestE2EConfigGetListsEveryKey: every settable key must be visible.
func TestE2EConfigGetListsEveryKey(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, _ := e2eSetup(t, dir)
	all := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "get")
	for _, k := range []string{
		"caption.safe_media_caption_utf16_units", "caption.safe_text_message_utf16_units", "caption.margin_utf16_units",
		"limits.free_upload_bytes", "limits.premium_upload_bytes",
		"locks.ttl_seconds", "locks.session_wait_seconds", "upload.threads", "upload.part_size_kb",
		"rate_limit.default_wait", "rate_limit.max_wait_seconds",
	} {
		if _, ok := all[k]; !ok {
			t.Fatalf("config get missing %s: %v", k, all)
		}
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "locks.ttl_seconds", "600")
	if got := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "get", "locks.ttl_seconds"); got["locks.ttl_seconds"] != float64(600) {
		t.Fatalf("locks.ttl_seconds = %v", got)
	}
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 3, "ERR_CONFIG_INVALID", "config", "set", "locks.ttl_seconds", "0")
}

// TestE2EShowSecretsConfirm: --confirm answers the prompt, and a
// non-interactive run without it fails instead of silently redacting.
func TestE2EShowSecretsConfirm(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, _ := e2eSetup(t, dir)

	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "config", "get", "telegram.api_hash", "--show-secrets", "--confirm")
	if err != nil {
		t.Fatalf("%v stderr=%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "deadbeef" || strings.Contains(stderr, "[y/N]") {
		t.Fatalf("--confirm should print the secret without prompting: stdout=%q stderr=%q", stdout, stderr)
	}

	_, _, err = runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "config", "get", "telegram.api_hash", "--show-secrets")
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 10 {
		t.Fatalf("non-interactive --show-secrets without --confirm: err=%v, want exit 10", err)
	}
}

// TestE2EFriendlyFileErrors covers the messages a user sees most often when
// they reach for familiar file-manager habits.
func TestE2EFriendlyFileErrors(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	e2eLocalFile(t, src, "x.txt", "x")
	if _, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "cp", "-r", src, "/dir"); err != nil {
		t.Fatalf("cp -r: %v %s", err, stderr)
	}
	out := filepath.Join(dir, "out")
	if _, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "get", "-r", "/dir", out); err != nil {
		t.Fatalf("get -r: %v %s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "src", "x.txt")); err != nil {
		if _, err2 := os.Stat(filepath.Join(out, "x.txt")); err2 != nil {
			t.Fatalf("get -r produced no x.txt: %v", err)
		}
	}

	for _, args := range [][]string{{"mv", "--confirm", "/dir", "/other"}, {"rm", "--confirm", "/dir"}} {
		_, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", args...)
		if err == nil {
			t.Fatalf("%v should fail", args)
		}
		if !strings.Contains(stderr, "directory") {
			t.Fatalf("%v error should explain directories are unsupported, got %q", args, stderr)
		}
	}

	// The discussion group is created by init and is never a drive candidate.
	chans := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "channels", "list")
	for _, c := range chans["channels"].([]any) {
		if title, _ := c.(map[string]any)["title"].(string); strings.Contains(title, "Discussion") {
			t.Fatalf("channels list includes discussion group: %v", chans)
		}
	}
}

// TestE2EInitBindRebuildsIndex: binding an existing drive after losing the
// DB is the documented recovery path, and init must scan the channel it just
// bound even when another channel is already bound.
func TestE2EInitBindRebuildsIndex(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=A")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, "a.txt", "a"), "/a.txt")

	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		_ = os.Remove(p)
	}
	bound := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--bind-channel=A")
	if bound["indexed_files"] != float64(1) {
		t.Fatalf("bind init = %v, want indexed_files 1", bound)
	}

	rootB := filepath.Join(dir, "rootB")
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", rootB, "--create-channel=B")
	statusB := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", "B", "status")
	if statusB["last_full_scan_at"] == "" {
		t.Fatalf("second init did not scan its own channel: %v", statusB)
	}
	if chans, _ := statusB["channels"].([]any); len(chans) != 2 {
		t.Fatalf("channels = %v, want 2", statusB["channels"])
	}

	// A --channel selector naming another drive does not redirect init's
	// scan away from the channel init just created.
	rootC := filepath.Join(dir, "rootC")
	if err := os.MkdirAll(rootC, 0o755); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", "A", "init", rootC, "--create-channel=C")
	statusC := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", "C", "status")
	if statusC["last_full_scan_at"] == "" {
		t.Fatalf("init under --channel A did not scan its new channel C: %v", statusC)
	}
}
