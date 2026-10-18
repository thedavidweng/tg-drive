package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestE2EDedupeAlbumRootScopeAndRebuild(t *testing.T) {
	dir := t.TempDir()
	bin, cfg, db, state, firstRoot := e2eSetup(t, dir)
	e2eLogin(t, bin, cfg, db, state)
	runE2EJSON(t, bin, cfg, db, state, "init", firstRoot, "--create-channel=First")
	secondRoot := filepath.Join(dir, "second")
	runE2EJSON(t, bin, cfg, db, state, "init", secondRoot, "--create-channel=Second")
	local := e2eLocalFile(t, dir, "same.txt", "duplicate bytes")
	albumArgs := []string{"--root", firstRoot, "cp"}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		albumArgs = append(albumArgs, e2eLocalFile(t, dir, name, "duplicate bytes"))
	}
	albumArgs = append(albumArgs, "/copies/")
	runE2EJSON(t, bin, cfg, db, state, albumArgs...)
	runE2EJSON(t, bin, cfg, db, state, "--root", firstRoot, "cp", local, "/copies/unhashed.txt", "--no-hash")
	runE2EJSON(t, bin, cfg, db, state, "--root", firstRoot, "cp", local, "/outside.txt")
	for _, path := range []string{"/copies/a.txt", "/copies/b.txt"} {
		runE2EJSON(t, bin, cfg, db, state, "--root", secondRoot, "cp", local, path)
	}
	// Album members use the existing real-delete protocol even when the
	// configured single-file policy is tombstone.
	runE2EJSON(t, bin, cfg, db, state, "config", "set", "delete.mode", "tombstone")
	audit := runE2EJSON(t, bin, cfg, db, state, "--root", firstRoot, "dedupe", "/copies")
	if audit["hashed_files"] != float64(3) || audit["unhashed_files"] != float64(1) {
		t.Fatalf("scoped audit = %v", audit)
	}
	cleanup := runE2EJSON(t, bin, cfg, db, state, "--root", firstRoot, "dedupe", "/copies", "--delete", "--confirm")
	if cleanup["deleted"] != float64(2) || cleanup["retained"] != float64(1) {
		t.Fatalf("cleanup = %v", cleanup)
	}
	retry := runE2EJSON(t, bin, cfg, db, state, "--root", firstRoot, "dedupe", "/copies", "--delete", "--confirm")
	if retry["deleted"] != float64(0) {
		t.Fatalf("repeat cleanup = %v", retry)
	}
	other := runE2EJSON(t, bin, cfg, db, state, "--root", secondRoot, "dedupe", "/copies")
	if other["hashed_files"] != float64(2) {
		t.Fatalf("other root changed = %v", other)
	}
	if err := os.Remove(db); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfg, db, state, "init", firstRoot, "--bind-channel=First")
	scan := runE2EJSON(t, bin, cfg, db, state, "scan", "--full")
	if scan["active"] != float64(3) {
		t.Fatalf("rebuilt tree = %v", scan)
	}
	for _, path := range []string{"/copies/a.txt", "/copies/unhashed.txt", "/outside.txt"} {
		dst := filepath.Join(dir, "downloads", filepath.Base(path))
		runE2EJSON(t, bin, cfg, db, state, "get", path, dst)
		if got, err := os.ReadFile(dst); err != nil || string(got) != "duplicate bytes" {
			t.Fatalf("surviving bytes at %s = %q, %v", path, got, err)
		}
	}
}
