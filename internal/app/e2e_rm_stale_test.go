package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// rmEnvelope is the td rm --json envelope, success or error.
type rmEnvelope struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data"`
	Error *struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// fakeMessage is the persisted fake Telegram message, as the state file
// holds it.
type fakeMessage struct {
	ID       int
	Text     string
	Caption  string
	FileName string
}

// fakeMessages returns every message the fake Telegram account holds, in
// the drive channel and its discussion group alike.
func fakeMessages(t *testing.T, statePath string) []fakeMessage {
	t.Helper()
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Messages map[string][]fakeMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	var out []fakeMessage
	for _, msgs := range st.Messages {
		out = append(out, msgs...)
	}
	return out
}

// runRm runs td --json rm with extraEnv and returns its exit code and
// envelope.
func runRm(t *testing.T, bin, cfgPath, dbPath, statePath string, extraEnv []string, args ...string) (int, rmEnvelope) {
	t.Helper()
	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, extraEnv, "", prependJSON(append([]string{"rm", "--confirm"}, args...)...)...)
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("rm %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	var env rmEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("rm %v: bad JSON %q (stderr=%s): %v", args, stdout, stderr, err)
	}
	return code, env
}

// activeFiles lists the indexed files under dir.
func activeFiles(t *testing.T, bin, cfgPath, dbPath, statePath, dir string) map[string]bool {
	t.Helper()
	ls := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", dir)
	entries, _ := ls["entries"].([]any)
	got := map[string]bool{}
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if p, _ := m["path"].(string); p != "" {
			got[p] = true
		}
	}
	return got
}

// rebuildIndex wipes the database and rebuilds it from the channel alone.
func rebuildIndex(t *testing.T, bin, cfgPath, dbPath, statePath, root string) map[string]any {
	t.Helper()
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--bind-channel=Drive")
	return runE2EJSON(t, bin, cfgPath, dbPath, statePath, "scan", "--full")
}

func sameSet(got map[string]bool, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, p := range want {
		if !got[p] {
			return false
		}
	}
	return true
}

// TestE2ERmStaleManifest removes files whose machine record cannot be
// redacted or rewritten, because the fake Telegram account fails comment
// edits or deletes. td rm must fail with ERR_TELEGRAM_RPC (exit 4) and report
// the stale record in error.details, or succeed with stale_manifest: true
// under --allow-stale-manifest; either way the media is gone and the row is
// deleted. A full rebuild from the channel must then agree with the index.
func TestE2ERmStaleManifest(t *testing.T) {
	for _, allow := range []bool{false, true} {
		name := "fails"
		if allow {
			name = "allow-stale-manifest"
		}
		t.Run("per-file delete/"+name, func(t *testing.T) {
			testRmStalePerFileDelete(t, allow)
		})
		t.Run("per-file tombstone/"+name, func(t *testing.T) {
			testRmStalePerFileTombstone(t, allow)
		})
		t.Run("album member/"+name, func(t *testing.T) {
			testRmStaleAlbumMember(t, allow)
		})
		t.Run("last album member/"+name, func(t *testing.T) {
			testRmStaleLastAlbumMember(t, allow)
		})
	}
}

func rmStaleArgs(allow bool, args ...string) []string {
	if allow {
		return append([]string{"--allow-stale-manifest"}, args...)
	}
	return args
}

// assertStaleOutcome checks the rm outcome for a record left stale.
func assertStaleOutcome(t *testing.T, code int, env rmEnvelope, allow bool, path, mode, recordWord string) {
	t.Helper()
	if allow {
		if code != 0 || !env.OK || env.Error != nil {
			t.Fatalf("rm --allow-stale-manifest: exit %d, envelope %+v", code, env)
		}
		if env.Data["stale_manifest"] != true || env.Data["path"] != path || env.Data["mode"] != mode {
			t.Fatalf("rm --allow-stale-manifest data = %v", env.Data)
		}
		return
	}
	if code != 4 || env.OK || env.Error == nil || env.Error.Code != "ERR_TELEGRAM_RPC" {
		t.Fatalf("rm: exit %d, envelope %+v; want exit 4 ERR_TELEGRAM_RPC", code, env)
	}
	if !strings.Contains(env.Error.Message, recordWord) || !strings.Contains(env.Error.Message, "--allow-stale-manifest") {
		t.Fatalf("rm error message = %q", env.Error.Message)
	}
	d := env.Error.Details
	if d["stale_manifest"] != true || d["path"] != path || d["mode"] != mode {
		t.Fatalf("rm error details = %v", d)
	}
	if id, _ := d["manifest_message_id"].(float64); id <= 0 {
		t.Fatalf("rm error details.manifest_message_id = %v", d["manifest_message_id"])
	}
}

func testRmStalePerFileDelete(t *testing.T, allow bool) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, "gone.txt", "gone"), "/docs/gone.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, "kept.txt", "kept"), "/docs/kept.txt")

	code, env := runRm(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_FAIL_COMMENTS=delete"}, rmStaleArgs(allow, "/docs/gone.txt")...)
	assertStaleOutcome(t, code, env, allow, "/docs/gone.txt", "delete", "manifest reply")
	// The row is already deleted, so there is nothing left to rerun.
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_REMOTE_NOT_FOUND", "rm", "--confirm", "--allow-stale-manifest", "/docs/gone.txt")

	if got := activeFiles(t, bin, cfgPath, dbPath, statePath, "/docs"); !sameSet(got, "/docs/kept.txt") {
		t.Fatalf("ls after rm = %v, want only /docs/kept.txt", got)
	}
	// The media is gone; its unredacted record stays behind in the thread.
	staleRecords := 0
	for _, m := range fakeMessages(t, statePath) {
		if m.FileName == "gone.txt" {
			t.Fatalf("media message %d survived rm", m.ID)
		}
		if strings.HasPrefix(m.Text, "td-manifest:v1") && !strings.Contains(m.Text, "deleted=true") {
			staleRecords++
		}
	}
	if staleRecords != 2 {
		t.Fatalf("live td-manifest records = %d, want 2 (kept.txt and the stale one)", staleRecords)
	}

	rebuildIndex(t, bin, cfgPath, dbPath, statePath, root)
	if got := activeFiles(t, bin, cfgPath, dbPath, statePath, "/docs"); !sameSet(got, "/docs/kept.txt") {
		t.Fatalf("ls after rebuild = %v, want only /docs/kept.txt", got)
	}
}

// A failed comment edit in tombstone mode falls back to a tombstone caption
// on the media post. The caption tombstone outranks the live comment during
// scans (ADR 0014), so the delete is sticky and nothing is reported stale.
func testRmStalePerFileTombstone(t *testing.T, allow bool) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, "tomb.txt", "tomb"), "/docs/tomb.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, "kept.txt", "kept"), "/docs/kept.txt")

	code, env := runRm(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_FAIL_COMMENTS=edit"}, rmStaleArgs(allow, "--tombstone", "/docs/tomb.txt")...)
	if code != 0 || !env.OK || env.Error != nil {
		t.Fatalf("rm --tombstone: exit %d, envelope %+v", code, env)
	}
	if env.Data["mode"] != "tombstone" || env.Data["path"] != "/docs/tomb.txt" || env.Data["stale_manifest"] != nil {
		t.Fatalf("rm --tombstone data = %v, want tombstone without stale_manifest", env.Data)
	}

	if got := activeFiles(t, bin, cfgPath, dbPath, statePath, "/docs"); !sameSet(got, "/docs/kept.txt") {
		t.Fatalf("ls after rm = %v, want only /docs/kept.txt", got)
	}
	var media *fakeMessage
	for _, m := range fakeMessages(t, statePath) {
		if m.FileName == "tomb.txt" {
			media = &m
		}
		if strings.HasPrefix(m.Text, "td-manifest:v1") && strings.Contains(m.Text, "deleted=true") {
			t.Fatalf("comment %d was redacted although comment edits fail", m.ID)
		}
	}
	if media == nil || !strings.Contains(media.Caption, "td:v1 deleted=true") {
		t.Fatalf("tombstoned media = %+v, want a td:v1 tombstone caption", media)
	}

	rebuildIndex(t, bin, cfgPath, dbPath, statePath, root)
	if got := activeFiles(t, bin, cfgPath, dbPath, statePath, "/docs"); !sameSet(got, "/docs/kept.txt") {
		t.Fatalf("ls after rebuild = %v, want only /docs/kept.txt (caption tombstone wins)", got)
	}
}

func uploadAlbum(t *testing.T, bin, cfgPath, dbPath, statePath, dir string, names ...string) {
	t.Helper()
	args := []string{"cp"}
	for _, name := range names {
		args = append(args, e2eLocalFile(t, dir, name, "content of "+name))
	}
	args = append(args, "/album/")
	up := runE2EJSON(t, bin, cfgPath, dbPath, statePath, args...)
	if groups, _ := up["albums"].([]any); len(groups) != 1 {
		t.Fatalf("cp albums = %v, want one group", up["albums"])
	}
}

// Album members are deleted in every mode, so a failed inventory rewrite
// leaves the inventory listing a member whose media is gone.
func testRmStaleAlbumMember(t *testing.T, allow bool) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	uploadAlbum(t, bin, cfgPath, dbPath, statePath, dir, "a.bin", "b.bin", "c.bin")

	code, env := runRm(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_FAIL_COMMENTS=edit"}, rmStaleArgs(allow, "/album/b.bin")...)
	assertStaleOutcome(t, code, env, allow, "/album/b.bin", "delete", "album inventory")

	if got := activeFiles(t, bin, cfgPath, dbPath, statePath, "/album"); !sameSet(got, "/album/a.bin", "/album/c.bin") {
		t.Fatalf("ls after rm = %v, want the siblings a.bin and c.bin", got)
	}
	for _, m := range fakeMessages(t, statePath) {
		if m.FileName == "b.bin" {
			t.Fatalf("media message %d survived rm", m.ID)
		}
	}

	scan := rebuildIndex(t, bin, cfgPath, dbPath, statePath, root)
	if got := activeFiles(t, bin, cfgPath, dbPath, statePath, "/album"); !sameSet(got, "/album/a.bin", "/album/c.bin") {
		t.Fatalf("ls after rebuild = %v (scan %v), want the siblings a.bin and c.bin", got, scan)
	}
	for _, name := range []string{"a.bin", "c.bin"} {
		dest := dir + "/rebuilt-" + name
		runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/album/"+name, dest)
		if got, err := os.ReadFile(dest); err != nil || string(got) != "content of "+name {
			t.Fatalf("rebuilt /album/%s = %q, %v", name, got, err)
		}
	}
}

// Removing the last member deletes the inventory with it; a failed comment
// delete leaves an inventory naming no live media.
func testRmStaleLastAlbumMember(t *testing.T, allow bool) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	uploadAlbum(t, bin, cfgPath, dbPath, statePath, dir, "a.bin", "b.bin")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "rm", "--confirm", "/album/a.bin")

	code, env := runRm(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_FAIL_COMMENTS=delete"}, rmStaleArgs(allow, "/album/b.bin")...)
	assertStaleOutcome(t, code, env, allow, "/album/b.bin", "delete", "album inventory")

	inventories := 0
	for _, m := range fakeMessages(t, statePath) {
		if m.FileName == "a.bin" || m.FileName == "b.bin" {
			t.Fatalf("media message %d survived rm", m.ID)
		}
		if strings.HasPrefix(m.Text, "td-album:v1") {
			inventories++
		}
	}
	if inventories != 1 {
		t.Fatalf("td-album inventories = %d, want the 1 stale one", inventories)
	}

	scan := rebuildIndex(t, bin, cfgPath, dbPath, statePath, root)
	if n, _ := scan["active"].(float64); n != 0 {
		t.Fatalf("scan --full = %v, want 0 active", scan)
	}
}
