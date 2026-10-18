package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
)

func TestE2EChannelIdentityIsolation(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	first := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=First")
	second := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", filepath.Join(dir, "second"), "--create-channel=Second")
	firstID, secondID := fmt.Sprint(first["channel_id"]), fmt.Sprint(second["channel_id"])
	a := e2eLocalFile(t, dir, "a.txt", "first drive")
	b := e2eLocalFile(t, dir, "b.txt", "second drive")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", firstID, "cp", a, "/same.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", secondID, "cp", b, "/same.txt")

	editIndex := func(query string, args ...any) {
		t.Helper()
		db, err := sqlitestore.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if _, err := db.Raw().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	editIndex(`update channels set title=? where tg_channel_id=?`, secondID, firstID)
	// Model a pre-Transfer v3 database. Reopening through the binary must
	// retain both bindings and their same-path files while repairing v4.
	editIndex(`drop table transfers; delete from schema_version where version>=4`)
	for i, id := range []string{firstID, secondID} {
		dest := filepath.Join(dir, fmt.Sprintf("download-%d.txt", i))
		runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", id, "get", "/same.txt", dest)
		got, err := os.ReadFile(dest)
		want := []string{"first drive", "second drive"}[i]
		if err != nil || string(got) != want {
			t.Fatalf("channel %s download = %q, %v; want %q", id, got, err, want)
		}
	}

	editIndex(`update channels set title='Same'`)
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"--channel", "Same", "cp", a, "/ambiguous.txt")
	for _, id := range []string{firstID, secondID} {
		runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_REMOTE_NOT_FOUND",
			"--channel", id, "get", "/ambiguous.txt", filepath.Join(dir, "unexpected.txt"))
	}
	editIndex(`update channels set title='999999' where tg_channel_id=?`, firstID)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", "999999", "ls", "/")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 4, "ERR_CHANNEL_NOT_FOUND",
		"--channel", "unbound", "ls", "/")
}
