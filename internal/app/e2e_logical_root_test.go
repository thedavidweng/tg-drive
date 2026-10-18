package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
)

func TestE2ELogicalRootSelection(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, firstRoot := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	first := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", firstRoot, "--create-channel=First")
	secondRoot := filepath.Join(dir, "second")
	second := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", secondRoot, "--create-channel=Second")
	firstID, secondID := fmt.Sprint(first["channel_id"]), fmt.Sprint(second["channel_id"])
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
	editIndex(`update channels set title='Same'`)
	for i, root := range []string{firstRoot, secondRoot} {
		content := fmt.Sprintf("drive %d", i)
		src := e2eLocalFile(t, dir, fmt.Sprintf("source-%d.txt", i), content)
		runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--root", root, "cp", src, "/same.txt")
		dst := filepath.Join(dir, fmt.Sprintf("download-%d.txt", i))
		runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--root", root, "get", "/same.txt", dst)
		got, err := os.ReadFile(dst)
		if err != nil || string(got) != content {
			t.Fatalf("root %s download = %q, %v; want %q", root, got, err, content)
		}
	}

	var envelope struct {
		Data struct {
			ChannelID string `json:"channel_id"`
		} `json:"data"`
	}
	// Run beside the binding so relative selection also works when Windows'
	// temporary directory and repository are on different drive letters.
	cmd := tdCommand(bin, cfgPath, dbPath, statePath, []string{"PWD=" + dir},
		"--json", "--root", "second", "status")
	cmd.Dir = dir
	relativeStatus, err := cmd.Output()
	if err != nil {
		t.Fatalf("relative root status: %v", err)
	}
	if err := json.Unmarshal(relativeStatus, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ChannelID != secondID {
		t.Fatalf("relative root selected %s, want channel %s", envelope.Data.ChannelID, secondID)
	}

	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath,
		[]string{"TD_CHANNEL=" + firstID}, "", "--json", "--root", secondRoot, "status")
	if err != nil {
		t.Fatalf("--root with TD_CHANNEL: %v %s", err, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ChannelID != secondID {
		t.Fatalf("--root did not override TD_CHANNEL: %s", stdout)
	}
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_FLAG_CONFLICT",
		"--root", secondRoot, "--channel", firstID, "rm", "--confirm", "/same.txt")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 4, "ERR_CHANNEL_NOT_FOUND",
		"--root", filepath.Join(dir, "unknown"), "rm", "--confirm", "/same.txt")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 4, "ERR_CHANNEL_NOT_FOUND",
		"--root", filepath.Join(dir, "unknown"), "status")

	// init's scan selects the newly bound channel even under another root.
	thirdRoot := filepath.Join(dir, "third")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--root", firstRoot,
		"init", thirdRoot, "--create-channel=Third")
	third := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--root", thirdRoot, "status")
	if scanned, ok := third["last_full_scan_at"].(string); !ok || scanned == "" {
		t.Fatalf("init under another root did not scan its own channel: %v", third)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--root", secondRoot, "mv", "--confirm", "/same.txt", "/moved.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--root", secondRoot, "rm", "--confirm", "/moved.txt")
	dst := filepath.Join(dir, "first-after-mutations.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--root", firstRoot, "get", "/same.txt", dst)
	if got, err := os.ReadFile(dst); err != nil || string(got) != "drive 0" {
		t.Fatalf("other root changed: %q, %v", got, err)
	}

	editIndex(`update channels set root_local_path=? where tg_channel_id=?`, firstRoot, secondID)
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"--root", firstRoot, "rm", "--confirm", "/same.txt")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "--channel", firstID,
		"get", "/same.txt", filepath.Join(dir, "after-ambiguous.txt"))
}
