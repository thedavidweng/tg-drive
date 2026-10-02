package app

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestE2EAlbumMemberRecordLifecycle moves, repairs, and removes individual
// album members through the real binary (rm in both delete modes), then wipes
// the database and rebuilds from the channel: every surviving sibling must
// come back from the shared td-album:v1 inventory at its current path, and no
// removed member may reappear.
func TestE2EAlbumMemberRecordLifecycle(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)

	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	content := map[string]string{
		"a.bin": "alpha",
		"b.bin": "bravo",
		"c.bin": "charlie",
		"d.bin": "delta",
		"e.bin": "echo",
	}
	names := make([]string, 0, len(content))
	for name := range content {
		names = append(names, name)
	}
	sort.Strings(names)
	args := []string{"cp"}
	for _, name := range names {
		args = append(args, e2eLocalFile(t, dir, name, content[name]))
	}
	args = append(args, "/album/")
	up := runE2EJSON(t, bin, cfgPath, dbPath, statePath, args...)
	if groups, _ := up["albums"].([]any); len(groups) != 1 {
		t.Fatalf("cp albums = %v, want one group", up["albums"])
	}

	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "mv", "--confirm", "/album/b.bin", "/moved/b2.bin")
	if rep := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "repair", "/album/c.bin"); rep["repaired"] != "/album/c.bin" {
		t.Fatalf("repair = %v", rep)
	}
	// Album members are deleted rather than tombstoned in every mode: a scan
	// indexes inventory members before it reads caption tombstones.
	if rm := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "rm", "--confirm", "--tombstone", "/album/a.bin"); rm["mode"] != "delete" || rm["stale_manifest"] != nil {
		t.Fatalf("rm --tombstone album member = %v", rm)
	}
	if rm := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "rm", "--confirm", "/album/d.bin"); rm["mode"] != "delete" || rm["stale_manifest"] != nil {
		t.Fatalf("rm album member = %v", rm)
	}

	survivors := map[string]string{
		"/moved/b2.bin": "bravo",
		"/album/c.bin":  "charlie",
		"/album/e.bin":  "echo",
	}
	assertTree := func(stage string) {
		t.Helper()
		got := map[string]bool{}
		for _, d := range []string{"/album", "/moved"} {
			ls := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", d)
			entries, _ := ls["entries"].([]any)
			for _, e := range entries {
				m, _ := e.(map[string]any)
				p, _ := m["path"].(string)
				got[p] = true
			}
		}
		if len(got) != len(survivors) {
			t.Fatalf("%s: files = %v, want %v", stage, got, survivors)
		}
		for p := range survivors {
			if !got[p] {
				t.Fatalf("%s: missing %s in %v", stage, p, got)
			}
		}
	}
	assertTree("before rebuild")

	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--bind-channel=Drive")
	scan := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "scan", "--full")
	if n, _ := scan["active"].(float64); n != float64(len(survivors)) {
		t.Fatalf("scan --full = %v, want %d active", scan, len(survivors))
	}
	assertTree("after rebuild")
	for p, want := range survivors {
		dest := filepath.Join(dir, "rebuilt", filepath.Base(p))
		runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", p, dest)
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("rebuilt %s = %q, want %q", p, got, want)
		}
	}

	// Removing the last members deletes the inventory with them; nothing
	// comes back on a second rebuild.
	for p := range survivors {
		runE2EJSON(t, bin, cfgPath, dbPath, statePath, "rm", "--confirm", p)
	}
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--bind-channel=Drive")
	scan = runE2EJSON(t, bin, cfgPath, dbPath, statePath, "scan", "--full")
	if n, _ := scan["active"].(float64); n != 0 {
		t.Fatalf("scan --full after removing every member = %v, want 0 active", scan)
	}
}
