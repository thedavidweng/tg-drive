package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestE2EFilesystemWatchCheckpointAndDelete(t *testing.T) {
	dir := t.TempDir()
	bin, cfg, db, state, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfg, db, state)
	runE2EJSON(t, bin, cfg, db, state, "init", root, "--create-channel=Drive")
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	local := e2eLocalFile(t, source, "file.txt", "watch initial")
	runE2EExpectError(t, bin, cfg, db, state, 10, "ERR_CONFIRMATION_REQUIRED", "watch", source, "/sync", "--once")
	first := runE2EJSON(t, bin, cfg, db, state, "watch", source, "/sync", "--confirm", "--once")
	if first["uploaded"] != float64(1) {
		t.Fatalf("first=%v", first)
	}
	restarted := runE2EJSON(t, bin, cfg, db, state, "watch", source, "/sync", "--confirm", "--once")
	if restarted["uploaded"] != float64(0) || restarted["unchanged"] != float64(1) {
		t.Fatalf("restart=%v", restarted)
	}
	if err := os.WriteFile(local, []byte("watch edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfg, db, state, "watch", source, "/sync", "--confirm", "--once")
	target := filepath.Join(dir, "download.txt")
	runE2EJSON(t, bin, cfg, db, state, "get", "/sync/file.txt", target)
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "watch edited" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	retained := runE2EJSON(t, bin, cfg, db, state, "watch", source, "/sync", "--confirm", "--once")
	if retained["retained"] != float64(1) {
		t.Fatalf("retained=%v", retained)
	}
	removed := runE2EJSON(t, bin, cfg, db, state, "watch", source, "/sync", "--confirm", "--delete", "--once")
	if removed["deleted"] != float64(1) {
		t.Fatalf("removed=%v", removed)
	}
	runE2EExpectError(t, bin, cfg, db, state, 2, "ERR_REMOTE_NOT_FOUND", "get", "/sync/file.txt", filepath.Join(dir, "absent.txt"))
}
