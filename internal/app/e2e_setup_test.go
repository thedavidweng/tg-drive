package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// errorEnvelope is the failure half of docs/contracts/json-contract.md,
// including details.
type errorEnvelope struct {
	OK    bool `json:"ok"`
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run: %v", err)
	}
	return exit.ExitCode()
}

func decodeError(t *testing.T, stdout string) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("bad JSON %q: %v", stdout, err)
	}
	if env.OK {
		t.Fatalf("want failure envelope, got %s", stdout)
	}
	return env
}

func decodeData(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var env e2eEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("bad JSON %q: %v", stdout, err)
	}
	if !env.OK {
		t.Fatalf("want success envelope, got %s", stdout)
	}
	return env.Data
}

// TestE2ECpDryRunPlan: the plan names the sources, destination, and conflict
// policy, flags a replace target, needs no --confirm, and makes no upload.
func TestE2ECpDryRunPlan(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	a := e2eLocalFile(t, dir, "a.txt", "a")
	b := e2eLocalFile(t, dir, "b.txt", "b")

	plan := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", a, b, "/docs", "--dry-run")
	want := map[string]any{"local": []any{a, b}, "remote": "/docs", "policy": "fail"}
	if !jsonEqual(plan, want) {
		t.Fatalf("plan = %v, want %v", plan, want)
	}

	plan = runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", a, "/a.txt", "--dry-run", "--replace")
	want = map[string]any{"local": []any{a}, "remote": "/a.txt", "policy": "replace", "would_replace": "/a.txt"}
	if !jsonEqual(plan, want) {
		t.Fatalf("replace plan = %v, want %v", plan, want)
	}

	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "cp", a, "/a.txt", "--dry-run", "--skip-existing", "--events")
	if err != nil {
		t.Fatalf("events dry-run: %v stderr=%s", err, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("events dry-run wants one event, got %q", stdout)
	}
	var ev struct {
		Data map[string]any `json:"data"`
		Meta struct {
			Command string `json:"command"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Meta.Command != "cp.dry-run" || !jsonEqual(ev.Data, map[string]any{"local": []any{a}, "remote": "/a.txt", "policy": "skip"}) {
		t.Fatalf("dry-run event = %s", lines[0])
	}

	stdout, stderr, err = runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "cp", a, "/a.txt", "--dry-run", "--auto-rename")
	if err != nil {
		t.Fatalf("human dry-run: %v stderr=%s", err, stderr)
	}
	wantHuman := `{"local":["` + a + `"],"policy":"rename","remote":"/a.txt"}` + "\n"
	if stdout != wantHuman {
		t.Fatalf("human dry-run = %q, want %q", stdout, wantHuman)
	}

	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_FLAG_CONFLICT", "cp", a, "/a.txt", "--dry-run", "--replace", "--skip-existing")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_REMOTE_NOT_FOUND", "ls", "/a.txt")
}

// credentialFreeEnv keeps the session out of the real home directory and
// stops ambient TD_* credentials from answering setup prompts.
func credentialFreeEnv(sessionPath string) []string {
	return []string{"TD_SESSION=" + sessionPath, "TD_API_ID=", "TD_API_HASH=", "TD_PHONE="}
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// TestE2EAuthSetup: setup asks only for missing credentials, saves them,
// and prepares the database; a bad api_id fails before anything else is
// asked.
func TestE2EAuthSetup(t *testing.T) {
	dir := t.TempDir()
	bin := buildBinary(t)
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "db.sqlite")
	statePath := filepath.Join(dir, "fake-state.json")
	sessionPath := filepath.Join(dir, "sess", "session.json")
	env := credentialFreeEnv(sessionPath)

	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "x\n", "--json", "auth", "setup")
	if code := exitCode(t, err); code != 3 {
		t.Fatalf("bad api_id exit = %d, stdout=%s", code, stdout)
	}
	if e := decodeError(t, stdout); e.Error.Code != "ERR_CONFIG_INVALID" {
		t.Fatalf("bad api_id error = %+v", e)
	}
	if strings.Contains(stderr, "api_hash") {
		t.Fatalf("bad api_id must fail before asking api_hash: %q", stderr)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("bad api_id must not write config: %v", err)
	}

	stdout, stderr, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "777\nabc\n", "--json", "auth", "setup")
	if err != nil {
		t.Fatalf("setup: %v stdout=%s stderr=%s", err, stdout, stderr)
	}
	if strings.Contains(stderr, "phone") {
		t.Fatalf("setup must not ask for the phone: %q", stderr)
	}
	got := decodeData(t, stdout)
	want := map[string]any{
		"api_id": float64(777), "config_path": cfgPath, "db_path": dbPath,
		"session_path": sessionPath, "status": "configured",
	}
	if !jsonEqual(got, want) {
		t.Fatalf("setup = %v, want %v", got, want)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("setup must create the database: %v", err)
	}
	if st, err := os.Stat(filepath.Dir(sessionPath)); err != nil || !st.IsDir() {
		t.Fatalf("setup must create the session dir: %v", err)
	}
	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "--json", "config", "get", "--show-secrets", "--confirm")
	if err != nil {
		t.Fatal(err)
	}
	all := decodeData(t, stdout)
	if all["telegram.api_id"] != float64(777) || all["telegram.api_hash"] != "abc" {
		t.Fatalf("setup did not persist credentials: %v", all)
	}

	// Complete credentials: nothing is asked, human output names the config.
	stdout, stderr, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "auth", "setup")
	if err != nil {
		t.Fatalf("re-run setup: %v stderr=%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("complete config must not prompt: %q", stderr)
	}
	wantHuman := "saved Telegram API credentials to " + cfgPath + "\nnext: td auth login\n"
	if stdout != wantHuman {
		t.Fatalf("human setup = %q, want %q", stdout, wantHuman)
	}
}

// TestE2ELoginAsksForMissingPhone: login collects missing credentials,
// including the phone, and saves them before it contacts Telegram.
func TestE2ELoginAsksForMissingPhone(t *testing.T) {
	dir := t.TempDir()
	bin := buildBinary(t)
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "db.sqlite")
	statePath := filepath.Join(dir, "fake-state.json")
	env := credentialFreeEnv(filepath.Join(dir, "session.json"))

	_, stderr, _ := runTD(t, bin, cfgPath, dbPath, statePath, env, "777\nabc\n+15550001111\n", "--json", "auth", "login")
	if !strings.Contains(stderr, "phone") {
		t.Fatalf("login must ask for the phone: %q", stderr)
	}
	stdout, _, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "", "--json", "config", "get", "telegram.phone", "--show-secrets", "--confirm")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeData(t, stdout); got["telegram.phone"] != "+15550001111" {
		t.Fatalf("login did not persist the phone: %v", got)
	}
}

// TestE2EConfigRedaction: secrets are redacted unless explicitly shown, in
// every output form, and set reports what it changed.
func TestE2EConfigRedaction(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, _ := e2eSetup(t, dir)
	env := credentialFreeEnv(filepath.Join(dir, "session.json"))

	stdout, _, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "", "--json", "config", "get")
	if err != nil {
		t.Fatal(err)
	}
	all := decodeData(t, stdout)
	if all["telegram.api_hash"] != "redacted" || all["telegram.phone"] != "+1********67" || all["telegram.api_id"] != float64(12345) {
		t.Fatalf("config get must redact secrets: %v", all)
	}
	if _, ok := all["config_path"]; ok {
		t.Fatalf("config get (all) has no config_path: %v", all)
	}

	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "--json", "config", "get", "telegram.api_hash")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeData(t, stdout); !jsonEqual(got, map[string]any{"telegram.api_hash": "redacted", "config_path": cfgPath}) {
		t.Fatalf("config get key = %v", got)
	}

	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "--json", "config", "get", "--show-secrets", "--confirm")
	if err != nil {
		t.Fatal(err)
	}
	if all := decodeData(t, stdout); all["telegram.api_hash"] != "deadbeef" || all["telegram.phone"] != "+15551234567" {
		t.Fatalf("--show-secrets --confirm must reveal: %v", all)
	}

	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "--json", "config", "get", "--show-secrets")
	if code := exitCode(t, err); code != 10 {
		t.Fatalf("--json --show-secrets without --confirm exit = %d", code)
	}
	if e := decodeError(t, stdout); e.Error.Code != "ERR_CONFIRMATION_REQUIRED" {
		t.Fatalf("--show-secrets without --confirm = %+v", e)
	}

	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "config", "get")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 20 || lines[0] != "telegram.api_id: 12345" || lines[1] != "telegram.api_hash: redacted" ||
		lines[2] != "telegram.phone: +1********67" || lines[len(lines)-1] != "rate_limit.max_wait_seconds: 300" {
		t.Fatalf("human config get = %q", stdout)
	}
	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "config", "get", "telegram.phone")
	if err != nil || stdout != "+1********67\n" {
		t.Fatalf("human config get key = %q err=%v", stdout, err)
	}

	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "--json", "config", "set", "delete.mode", "delete")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeData(t, stdout); !jsonEqual(got, map[string]any{"key": "delete.mode", "status": "set"}) {
		t.Fatalf("config set = %v", got)
	}
	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "config", "set", "upload.threads", "3")
	if err != nil || stdout != "set upload.threads\n" {
		t.Fatalf("human config set = %q err=%v", stdout, err)
	}
	if got := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "get", "upload.threads"); got["upload.threads"] != float64(3) {
		t.Fatalf("config set did not persist: %v", got)
	}
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE", "config", "get", "no.such.key")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE", "config", "set", "no.such.key", "1")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 3, "ERR_CONFIG_INVALID", "config", "set", "delete.mode", "shred")
}

// TestE2EConfigSetKeepsOverridesOutOfTheFile: config set saves the one key
// it was given. Paths and credentials that came from flags or the
// environment for this run (--db, TD_SESSION, TD_API_HASH) are not written
// into the config file, so a front end with its own session path (the GUI)
// or a secret supplied by the environment never leaks into every later run.
func TestE2EConfigSetKeepsOverridesOutOfTheFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "override.db")
	statePath := filepath.Join(dir, "fake-state.json")
	if err := os.WriteFile(cfgPath, []byte("[telegram]\napi_id = 12345\napi_hash = \"deadbeef\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := buildBinary(t)
	env := []string{"TD_API_HASH=from-the-environment"}
	if _, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "", "config", "set", "upload.threads", "3"); err != nil {
		t.Fatalf("config set: %v stderr=%s", err, stderr)
	}
	if _, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "", "auth", "setup"); err != nil {
		t.Fatalf("auth setup: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	saved := string(data)
	for _, leaked := range []string{"from-the-environment", dbPath, filepath.Join(dir, "session.json")} {
		if strings.Contains(saved, leaked) {
			t.Fatalf("config file saved the run's override %q:\n%s", leaked, saved)
		}
	}
	if !strings.Contains(saved, "deadbeef") || !strings.Contains(saved, "threads = 3") {
		t.Fatalf("config file lost its own values or the set key:\n%s", saved)
	}
}

// TestE2EInitChannelChoice: without a channel, init offers the existing
// channels (as error details in --json, as a picker otherwise), and a bare
// --create-channel names the channel after --channel or the root directory.
func TestE2EInitChannelChoice(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	env := []string{"TD_CHANNEL="}

	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, env, "", "init", root)
	if code := exitCode(t, err); code != 4 {
		t.Fatalf("init with no channels exit = %d stderr=%s", code, stderr)
	}
	if stdout != "" || !strings.Contains(stderr, "no existing channels to bind") {
		t.Fatalf("init with no channels: stdout=%q stderr=%q", stdout, stderr)
	}

	other := filepath.Join(dir, "Photos")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	created := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", other, "--create-channel")
	if created["channel_title"] != "Photos" {
		t.Fatalf("bare --create-channel title = %v, want the root's name", created["channel_title"])
	}

	stdout, _, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "", "--json", "init", root)
	if code := exitCode(t, err); code != 4 {
		t.Fatalf("--json init without channel exit = %d", code)
	}
	e := decodeError(t, stdout)
	chans, _ := e.Error.Details["channels"].([]any)
	if e.Error.Code != "ERR_CHANNEL_NOT_FOUND" || len(chans) != 1 {
		t.Fatalf("--json init without channel = %s", stdout)
	}
	ch, _ := chans[0].(map[string]any)
	if ch["title"] != "Photos" || ch["id"] != created["channel_id"] || !equalKeys(keys(ch), []string{"id", "invite_link", "title", "username"}) {
		t.Fatalf("candidate = %v", ch)
	}

	_, stderr, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "9\n", "init", root, "--bind-channel")
	if code := exitCode(t, err); code != 2 || !strings.Contains(stderr, "invalid channel selection") {
		t.Fatalf("invalid selection exit = %d stderr=%q", code, stderr)
	}

	stdout, stderr, err = runTD(t, bin, cfgPath, dbPath, statePath, env, "1\n", "init", root)
	if err != nil {
		t.Fatalf("picker: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(stderr, "1. Photos (id ") || !strings.Contains(stdout, `-> channel "Photos"`) {
		t.Fatalf("picker: stdout=%q stderr=%q", stdout, stderr)
	}

	third := filepath.Join(dir, "third")
	if err := os.MkdirAll(third, 0o755); err != nil {
		t.Fatal(err)
	}
	named := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", "Named", "init", third, "--create-channel")
	if named["channel_title"] != "Named" {
		t.Fatalf("bare --create-channel with --channel title = %v", named["channel_title"])
	}
}
