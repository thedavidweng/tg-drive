package app

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// findTransfer returns the listed transfer of kind kind, failing when there
// is not exactly one.
func findTransfer(t *testing.T, list []map[string]any, kind string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, tr := range list {
		if tr["kind"] == kind {
			found = append(found, tr)
		}
	}
	if len(found) != 1 {
		t.Fatalf("transfers of kind %s = %v, want exactly one (all: %v)", kind, found, list)
	}
	return found[0]
}

// TestE2EAlbumCpIsATransfer: a multi-file td cp runs as one album_upload
// Transfer whose item counts track the member files and which records no
// per-item byte progress. The command's result output is unchanged.
func TestE2EAlbumCpIsATransfer(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	var locals []string
	for i, name := range []string{"a.txt", "b.txt", "c.txt"} {
		locals = append(locals, e2eLocalFile(t, dir, name, fmt.Sprintf("file %d", i)))
	}
	args := append([]string{"cp"}, locals...)
	args = append(args, "/albums/")
	up := runE2EJSON(t, bin, cfgPath, dbPath, statePath, args...)
	if n, _ := up["uploaded"].(float64); n != 3 {
		t.Fatalf("uploaded = %v, want 3", up["uploaded"])
	}
	if groups, _ := up["albums"].([]any); len(groups) != 1 {
		t.Fatalf("albums = %v, want 1 group", up["albums"])
	}

	done := findTransfer(t, listTransfers(t, bin, cfgPath, dbPath, statePath, "--all"), "album_upload")
	if done["stage"] != "completed" || done["items_done"] != float64(3) || done["items_total"] != float64(3) ||
		done["dest"] != "/albums/" || done["finished_at"] == nil {
		t.Fatalf("completed album transfer = %v, want 3/3 items to /albums/", done)
	}
	if done["bytes_done"] != float64(0) || done["bytes_total"] != float64(0) {
		t.Fatalf("album transfer bytes = %v/%v, want none: item counts track it",
			done["bytes_done"], done["bytes_total"])
	}
}

// TestE2EGetIsATransfer: td get runs as a download Transfer: a second
// process sees it in the downloading stage while it runs, and afterwards the
// index holds it completed with the downloaded byte count. The command's
// result output is what it was.
func TestE2EGetIsATransfer(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	src := e2eSizedFile(t, filepath.Join(dir, "doc.bin"), 2048)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", src, "/doc.bin")

	dest := filepath.Join(dir, "out", "doc.bin")
	cmd := tdCommand(bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=2s"},
		"--json", "get", "/doc.bin", dest)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	// The fake delays the download 2s; the Transfer must show downloading
	// well within that window.
	var running map[string]any
	for deadline := time.Now().Add(10 * time.Second); ; {
		downloading := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "downloading")
		if len(downloading) > 0 {
			running = downloading[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no downloading transfer while get runs; stderr=%s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if running["kind"] != "download" || running["front_end"] != "cli" ||
		running["source"] != "/doc.bin" || running["dest"] != dest {
		t.Fatalf("running transfer = %v, want the cli download of /doc.bin to %s", running, dest)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("get: %v stderr=%s", err, stderr.String())
	}
	result := decodeData(t, stdout.String())
	if result["path"] != "/doc.bin" || result["local"] != dest || result["size"] != float64(2048) {
		t.Fatalf("get result = %v, want /doc.bin downloaded to %s (2048 bytes)", result, dest)
	}

	done := findTransfer(t, listTransfers(t, bin, cfgPath, dbPath, statePath, "--all"), "download")
	if done["stage"] != "completed" || done["bytes_done"] != float64(2048) || done["bytes_total"] != float64(2048) ||
		done["items_done"] != float64(1) || done["items_total"] != float64(1) || done["finished_at"] == nil {
		t.Fatalf("completed download transfer = %v, want completed with all 2048 bytes and 1/1 items", done)
	}
	if shown := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "show", fmt.Sprint(done["id"])); !jsonEqual(shown, done) {
		t.Fatalf("transfers show = %v, want %v", shown, done)
	}
}
