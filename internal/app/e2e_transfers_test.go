package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// listTransfers runs td transfers list with args in its own process and
// returns the transfers it reports.
func listTransfers(t *testing.T, bin, cfgPath, dbPath, statePath string, args ...string) []map[string]any {
	t.Helper()
	data := runE2EJSON(t, bin, cfgPath, dbPath, statePath, append([]string{"transfers", "list"}, args...)...)
	raw, ok := data["transfers"].([]any)
	if !ok {
		t.Fatalf("transfers list data = %v, want a transfers array", data)
	}
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i], _ = r.(map[string]any)
	}
	return out
}

// TestE2ECpEventsTransferStages: td cp --events reports the Transfer's
// stages as transfer.stage events, in stage order and interleaved where
// they happen, with a transfer.progress line for each confirmed part.
func TestE2ECpEventsTransferStages(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath,
		"cp", big, "/big.bin", "--events", "--upload-part-size-kb", "4096")

	var commands []string
	for _, ev := range lines {
		commands = append(commands, fmt.Sprint(ev.Meta["command"]))
	}
	wantCommands := []string{
		"transfer.stage", "transfer.stage", "transfer.stage",
		"transfer.progress", "transfer.progress", "transfer.progress",
		"transfer.stage", "transfer.stage", "cp",
	}
	if !jsonEqual(commands, wantCommands) {
		t.Fatalf("event commands = %v, want %v", commands, wantCommands)
	}

	var stages []string
	var first, last map[string]any
	for _, ev := range lines {
		if ev.Meta["command"] != "transfer.stage" {
			continue
		}
		var tr map[string]any
		if err := json.Unmarshal(ev.Data, &tr); err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = tr
		}
		if tr["id"] != first["id"] || tr["kind"] != "upload" || tr["source"] != big {
			t.Fatalf("transfer.stage %v does not describe the one upload %v", tr, first)
		}
		stages = append(stages, fmt.Sprint(tr["stage"]))
		last = tr
	}
	if want := []string{"queued", "hashing", "uploading", "publishing", "completed"}; !jsonEqual(stages, want) {
		t.Fatalf("stages = %v, want %v", stages, want)
	}
	if last["bytes_done"] != float64(12*mib) || last["bytes_total"] != float64(12*mib) || last["dest"] != "/big.bin" {
		t.Fatalf("completed transfer.stage = %v", last)
	}
	shown := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "show", fmt.Sprint(first["id"]))
	if !jsonEqual(shown, last) {
		t.Fatalf("transfers show = %v, want the last transfer.stage payload %v", shown, last)
	}
}

// TestE2ECpIsATransfer: a single-file td cp runs as a Transfer that a second
// td process sees through the index, live while it uploads and as history
// once it completed. The second process reads only the index, so it answers
// while the uploading process holds the Session lock.
func TestE2ECpIsATransfer(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	cmd := tdCommand(bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=200ms"},
		"--json", "cp", big, "/big.bin", "--events", "--upload-part-size-kb", "1024", "--upload-threads", "1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	// The second part's report comes after a throttled progress write.
	for parts := 0; parts < 2 && sc.Scan(); {
		if strings.Contains(sc.Text(), `"command":"transfer.progress"`) {
			parts++
		}
	}

	active := listTransfers(t, bin, cfgPath, dbPath, statePath)
	if len(active) != 1 {
		t.Fatalf("active transfers while uploading = %v, want the running cp", active)
	}
	running := active[0]
	if running["stage"] != "uploading" || running["kind"] != "upload" || running["front_end"] != "cli" ||
		running["bytes_total"] != float64(12*mib) || running["bytes_done"].(float64) <= 0 {
		t.Fatalf("running transfer = %v, want an uploading cli upload with byte progress", running)
	}

	var last string
	for sc.Scan() {
		last = sc.Text()
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("cp: %v stderr=%s", err, stderr.String())
	}
	result := decodeData(t, last)

	if got := listTransfers(t, bin, cfgPath, dbPath, statePath); len(got) != 0 {
		t.Fatalf("active transfers after cp = %v, want none", got)
	}
	all := listTransfers(t, bin, cfgPath, dbPath, statePath, "--all")
	if len(all) != 1 {
		t.Fatalf("all transfers = %v, want the one cp", all)
	}
	done := all[0]
	id, _ := done["id"].(string)
	if !uuidPattern.MatchString(id) {
		t.Fatalf("transfer id %q is not a UUID", id)
	}
	want := map[string]any{
		"id": id, "kind": "upload", "stage": "completed", "channel": result["channel_id"],
		"source": big, "dest": "/big.bin", "bytes_done": float64(12 * mib), "bytes_total": float64(12 * mib),
		"items_done": float64(1), "items_total": float64(1), "front_end": "cli", "cancel_requested": false,
		"created_at": done["created_at"], "updated_at": done["updated_at"], "finished_at": done["finished_at"],
	}
	if !jsonEqual(done, want) {
		t.Fatalf("completed transfer = %v, want %v", done, want)
	}
	if done["finished_at"] == nil {
		t.Fatalf("completed transfer has no finished_at: %v", done)
	}

	if got := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "completed"); !jsonEqual(got, all) {
		t.Fatalf("--stage completed = %v, want %v", got, all)
	}
	if got := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "failed"); len(got) != 0 {
		t.Fatalf("--stage failed = %v, want none", got)
	}
	if shown := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "show", id); !jsonEqual(shown, done) {
		t.Fatalf("transfers show = %v, want %v", shown, done)
	}
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_TRANSFER_NOT_FOUND",
		"transfers", "show", "00000000-0000-4000-8000-000000000000")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_FLAG_CONFLICT", "transfers", "list", "--active", "--all")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE", "transfers", "list", "--stage", "sideways")

	human, stderrHuman, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "transfers", "list", "--all")
	if err != nil {
		t.Fatalf("human list: %v stderr=%s", err, stderrHuman)
	}
	wantHuman := fmt.Sprintf("%s  completed  upload  12.0 MB/12.0 MB  %s -> /big.bin\n", id, big)
	if human != wantHuman {
		t.Fatalf("human list = %q, want %q", human, wantHuman)
	}
}

// TestE2EFailedCpTransfer: an upload that fails ends its Transfer failed,
// recording the error code and message td cp reported.
func TestE2EFailedCpTransfer(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_FAIL_UPLOAD_AFTER_PARTS=2"}, "",
		"--json", "cp", big, "/big.bin", "--upload-part-size-kb", "1024")
	if err == nil {
		t.Fatalf("cp with a failing upload succeeded: %s", stdout)
	}
	reported := decodeError(t, stdout)

	failed := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "failed")
	if len(failed) != 1 {
		t.Fatalf("failed transfers = %v, want the failed cp; stderr=%s", failed, stderr)
	}
	got := failed[0]
	if got["error_code"] != reported.Error.Code || got["error_message"] != reported.Error.Message ||
		got["finished_at"] == nil || got["items_done"] != float64(0) {
		t.Fatalf("failed transfer = %v, want it ended with %s: %s", got, reported.Error.Code, reported.Error.Message)
	}
}
