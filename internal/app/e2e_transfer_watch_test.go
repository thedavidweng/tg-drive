//go:build !windows

package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The watch tests send SIGINT to end the watch, which Windows cannot
// deliver to a child process.

// stageRanks orders the Transfer stages as the transfer package documents
// them, so the test can assert a watch stream only advances.
var stageRanks = map[string]int{
	"queued": 0, "hashing": 1, "uploading": 2, "downloading": 2,
	"publishing": 3, "completed": 4, "failed": 4,
}

// startCp starts a slowed single-file upload in its own process, so a watch
// in another process has a Transfer to follow.
func startCp(t *testing.T, bin, cfgPath, dbPath, statePath, big, dest string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := tdCommand(bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=200ms"},
		"--json", "cp", big, dest, "--upload-part-size-kb", "1024", "--upload-threads", "1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, &stderr
}

// collectWatchStages reads watch output until a terminal stage for one
// Transfer arrives, and returns every stage observed for it, its id, and
// its last payload. stageOf decodes one output line, returning an empty
// stage for lines that report no stage. A watch that never reports the
// terminal stage is killed by the watchdog, failing the test with what was
// collected.
func collectWatchStages(t *testing.T, cmd *exec.Cmd, stderr *bytes.Buffer, sc *bufio.Scanner,
	stageOf func(line string) (id, stage string, data map[string]any),
) (id string, stages []string, last map[string]any) {
	t.Helper()
	watchdog := time.AfterFunc(20*time.Second, func() { _ = cmd.Process.Kill() })
	t.Cleanup(func() { watchdog.Stop() })
	for sc.Scan() {
		lineID, stage, data := stageOf(sc.Text())
		if stage == "" {
			continue
		}
		if id == "" {
			id = lineID
		} else if lineID != id {
			t.Fatalf("watch reported a second Transfer %s; want only the cp's %s", lineID, id)
		}
		stages = append(stages, stage)
		last = data
		if stage == "completed" || stage == "failed" {
			return id, stages, last
		}
	}
	t.Fatalf("watch ended without a terminal stage; stages=%v stderr=%s", stages, stderr)
	return "", nil, nil
}

func assertAdvancing(t *testing.T, stages []string) {
	t.Helper()
	if len(stages) < 2 {
		t.Fatalf("watch stages = %v, want at least one active stage and the terminal one", stages)
	}
	for i := 1; i < len(stages); i++ {
		if stageRanks[stages[i]] <= stageRanks[stages[i-1]] {
			t.Fatalf("watch stages = %v, want strictly advancing", stages)
		}
	}
	if stages[len(stages)-1] != "completed" {
		t.Fatalf("watch stages = %v, want them ending with completed", stages)
	}
}

// TestE2ETransfersWatchEvents: td transfers watch --events (or --json) in
// process B streams advancing transfer.stage events for a td cp running in
// process A, ending with its terminal stage, whose payload is the Transfer
// as td transfers show returns it. Ctrl-C ends the watch with exit 130.
func TestE2ETransfersWatchEvents(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)

	// stageEvent decodes one stdout line of the watch stream. Strict mode
	// (the --events run) rejects any line that is not a transfer.stage
	// event: the stream must hold nothing else. The --json run is lenient
	// because Ctrl-C closes its stream with the ERR_CANCELLED envelope.
	stageEvent := func(strict bool) func(line string) (id, stage string, data map[string]any) {
		return func(line string) (id, stage string, data map[string]any) {
			if !strings.Contains(line, `"command":"transfer.stage"`) {
				if strict {
					t.Fatalf("non-event line on the watch stream: %q", line)
				}
				return "", "", nil
			}
			var ev eventLine
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("bad event line %q: %v", line, err)
			}
			var tr map[string]any
			if err := json.Unmarshal(ev.Data, &tr); err != nil {
				t.Fatal(err)
			}
			return fmt.Sprint(tr["id"]), fmt.Sprint(tr["stage"]), tr
		}
	}

	run := func(t *testing.T, dest string, jsonMode bool, watchArgs ...string) {
		watchCmd, watchSc, watchStderr := startTD(t, bin, cfgPath, dbPath, statePath, nil, watchArgs...)
		cp, cpStderr := startCp(t, bin, cfgPath, dbPath, statePath, big, dest)

		id, stages, last := collectWatchStages(t, watchCmd, watchStderr, watchSc, stageEvent(!jsonMode))
		if !uuidPattern.MatchString(id) {
			t.Fatalf("watched transfer id %q is not a UUID", id)
		}
		assertAdvancing(t, stages)
		if last["kind"] != "upload" || last["source"] != big || last["dest"] != dest ||
			last["bytes_done"] != float64(12*mib) || last["finished_at"] == nil {
			t.Fatalf("terminal transfer.stage = %v, want the completed upload", last)
		}
		if err := cp.Wait(); err != nil {
			t.Fatalf("cp: %v stderr=%s", err, cpStderr)
		}
		shown := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "show", id)
		if !jsonEqual(shown, last) {
			t.Fatalf("transfers show = %v, want the terminal transfer.stage payload %v", shown, last)
		}

		lines, code, took := interruptAndWait(t, watchCmd, watchSc)
		if jsonMode {
			// Ctrl-C closes the stream with the ERR_CANCELLED envelope.
			assertCancelled(t, lines, code, took, watchStderr.String())
			return
		}
		if took > cancelDeadline {
			t.Fatalf("watch took %s to exit after SIGINT, want under %s", took, cancelDeadline)
		}
		if code != 130 {
			t.Fatalf("watch exit = %d, want 130; lines=%v stderr=%s", code, lines, watchStderr)
		}
		if len(lines) != 0 {
			t.Fatalf("stdout past the terminal event = %v, want no further events for the ended cp", lines)
		}
	}

	// --events streams NDJSON; without --json the Ctrl-C error is human,
	// on stderr, so the stdout stream holds transfer.stage events only.
	t.Run("events", func(t *testing.T) {
		run(t, "/big.bin", false, "transfers", "watch", "--events")
	})
	// --json streams the same events and closes the stream with the
	// ERR_CANCELLED envelope on Ctrl-C.
	t.Run("json", func(t *testing.T) {
		run(t, "/big-json.bin", true, "--json", "transfers", "watch")
	})
}

// TestE2ETransfersWatchHuman: piped (not a terminal), td transfers watch
// degrades to one line per stage a Transfer enters, in the transfers list
// format — it refreshes on stage changes without flooding. Ctrl-C exits
// 130 with a human error on stderr.
func TestE2ETransfersWatchHuman(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)

	watchCmd, watchSc, watchStderr := startTD(t, bin, cfgPath, dbPath, statePath, nil, "transfers", "watch")
	cp, cpStderr := startCp(t, bin, cfgPath, dbPath, statePath, big, "/big.bin")

	stageLine := func(line string) (id, stage string, data map[string]any) {
		fields := strings.Split(line, "  ")
		if len(fields) != 5 || !uuidPattern.MatchString(fields[0]) {
			t.Fatalf("line on the piped watch stream is not a transfer line: %q", line)
		}
		if _, ok := stageRanks[fields[1]]; !ok {
			t.Fatalf("piped watch line %q has no stage in its second field", line)
		}
		return fields[0], fields[1], map[string]any{"line": line}
	}
	_, stages, last := collectWatchStages(t, watchCmd, watchStderr, watchSc, stageLine)
	assertAdvancing(t, stages)
	if len(stages) > 5 {
		t.Fatalf("watch printed %d stage lines for one upload, want at most one per stage it visits (5)", len(stages))
	}
	wantCompleted := "completed  upload  12.0 MB/12.0 MB  " + big + " -> /big.bin"
	if !strings.HasSuffix(fmt.Sprint(last["line"]), wantCompleted) {
		t.Fatalf("terminal watch line = %v, want it ending %q", last["line"], wantCompleted)
	}
	if err := cp.Wait(); err != nil {
		t.Fatalf("cp: %v stderr=%s", err, cpStderr)
	}

	_, code, took := interruptAndWait(t, watchCmd, watchSc)
	if took > cancelDeadline {
		t.Fatalf("watch took %s to exit after SIGINT, want under %s", took, cancelDeadline)
	}
	if code != 130 {
		t.Fatalf("watch exit = %d, want 130; stderr=%s", code, watchStderr)
	}
	if !strings.Contains(watchStderr.String(), "error: operation cancelled") {
		t.Fatalf("watch stderr = %q, want the human ERR_CANCELLED line", watchStderr)
	}
}
