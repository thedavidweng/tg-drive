//go:build !windows

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2ERetryFailedUploadResumes: a failed large upload leaves its
// confirmed parts in upload_progress; td transfers retry re-runs the
// recorded request, sends only the unconfirmed parts, and ends the same
// Transfer completed — its ID and created_at unchanged.
func TestE2ERetryFailedUploadResumes(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	// 12 parts of 1 MiB; the fake confirms 2, then fails the upload once.
	_, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_FAIL_UPLOAD_AFTER_PARTS=2"}, "",
		"--json", "cp", big, "/big.bin", "--upload-part-size-kb", "1024", "--upload-threads", "1")
	if err == nil {
		t.Fatal("expected cp to fail")
	}

	failed := findTransfer(t, listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "failed"), "upload")
	id, _ := failed["id"].(string)
	created, _ := failed["created_at"].(string)
	if failed["error_code"] == nil {
		t.Fatalf("failed transfer = %v, want the error recorded; stderr=%s", failed, stderr)
	}

	lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath, "transfers", "retry", id, "--events")

	var parts []int
	var stages []string
	var final map[string]any
	for _, ev := range lines {
		switch ev.Meta["command"] {
		case "cp.progress":
			var p struct {
				Part int `json:"Part"`
			}
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatal(err)
			}
			parts = append(parts, p.Part)
		case "transfer.stage":
			var tr map[string]any
			if err := json.Unmarshal(ev.Data, &tr); err != nil {
				t.Fatal(err)
			}
			if tr["id"] != id {
				t.Fatalf("transfer.stage %v is not the retried Transfer %s", tr, id)
			}
			stages = append(stages, fmt.Sprint(tr["stage"]))
		case "transfers.retry":
			if err := json.Unmarshal(ev.Data, &final); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected event %q in %v", ev.Meta["command"], lines)
		}
	}
	// Resume: the 2 confirmed parts are not sent again, so the stream
	// carries parts 2..11 only — a fresh upload would send 0..11.
	if len(parts) != 10 || parts[0] != 2 || parts[len(parts)-1] != 11 {
		t.Fatalf("retried upload sent parts %v, want 2..11 (10 parts, the confirmed two resumed)", parts)
	}
	if want := []string{"queued", "hashing", "uploading", "publishing", "completed"}; !jsonEqual(stages, want) {
		t.Fatalf("retry stages = %v, want %v", stages, want)
	}
	if final == nil {
		t.Fatalf("no final transfers.retry event in %v", lines)
	}
	if final["stage"] != "completed" || final["id"] != id || final["created_at"] != created ||
		final["bytes_done"] != float64(12*mib) || final["bytes_total"] != float64(12*mib) ||
		final["finished_at"] == nil || final["error_code"] != nil || final["cancel_requested"] != false {
		t.Fatalf("retried transfer = %v, want the same Transfer completed without its old error", final)
	}

	// The bytes round-trip: what landed is what the file holds.
	restore := filepath.Join(dir, "restored.bin")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/big.bin", restore)
	restored, err := os.ReadFile(restore)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(big)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(blake3Hex(restored), blake3Hex(source)) {
		t.Fatal("restored bytes differ from the source")
	}
}

// TestE2ERetryInterruptedUploadResumes: a td cp killed with SIGKILL leaves
// its Transfer to be marked interrupted once the lease expires, and another
// process's td transfers retry takes the Transfer over — it becomes the
// owner — and resumes the upload from the parts upload_progress saved: the
// retried upload sends only the parts that were not confirmed, and the
// Transfer ends completed.
func TestE2ERetryInterruptedUploadResumes(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	// A one-second lease expires about a second after the owner dies.
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "locks.ttl_seconds", "1")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	cmd, sc, stderr := startTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=1s"},
		"--json", "cp", big, "/big.bin", "--events", "--upload-part-size-kb", "1024", "--upload-threads", "1")

	// Wait until two parts are confirmed (their progress lines arrived), so
	// the kill leaves saved parts to resume from.
	confirmed := 0
	for confirmed < 2 && sc.Scan() {
		if strings.Contains(sc.Text(), `"command":"cp.progress"`) {
			confirmed++
		}
	}
	if confirmed < 2 {
		t.Fatalf("cp confirmed %d parts before the read ended; stderr=%s", confirmed, stderr)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	killed := time.Now()
	_ = cmd.Wait()

	var interrupted map[string]any
	for deadline := time.Now().Add(15 * time.Second); ; {
		if list := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "interrupted"); len(list) > 0 {
			interrupted = list[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfer never marked interrupted; stderr=%s", stderr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	id, _ := interrupted["id"].(string)

	// The interrupted marking watches the Transfer lease; the dead cp's
	// operation lock on /big.bin lapses on the same 1s TTL, but its
	// expires_at compares at second precision, so it is stale at worst two
	// seconds after the kill. Wait it out so the retry takes the lock over
	// instead of racing its expiry.
	time.Sleep(time.Until(killed.Add(2300 * time.Millisecond)))

	lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath, "transfers", "retry", id, "--events")
	var parts []int
	var final map[string]any
	for _, ev := range lines {
		switch ev.Meta["command"] {
		case "cp.progress":
			var p struct {
				Part int `json:"Part"`
			}
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatal(err)
			}
			parts = append(parts, p.Part)
		case "transfers.retry":
			if err := json.Unmarshal(ev.Data, &final); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The confirmed parts were not sent again: the retry's first part is
	// the count already confirmed, and fewer than the full 12 went out.
	if len(parts) == 0 || len(parts) >= 12 || parts[0] != 12-len(parts) {
		t.Fatalf("resumed upload sent parts %v, want parts %d..11 after %d confirmed", parts, 12-len(parts), 12-len(parts))
	}
	if final["stage"] != "completed" || final["id"] != id {
		t.Fatalf("retried transfer = %v, want the interrupted Transfer completed", final)
	}
	shown := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "show", id)
	if shown["stage"] != "completed" || shown["bytes_done"] != float64(12*mib) {
		t.Fatalf("transfers show = %v, want completed with all bytes", shown)
	}

	restore := filepath.Join(dir, "restored.bin")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "get", "/big.bin", restore)
	restored, err := os.ReadFile(restore)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(big)
	if err != nil {
		t.Fatal(err)
	}
	if blake3Hex(restored) != blake3Hex(source) {
		t.Fatal("restored bytes differ from the source")
	}
}

// cancelRunningTransfer starts args (a long transfer) in the background,
// waits for one Transfer in wantStage, cancels it from a second process,
// and returns the cancelled Transfer's ID once the owning command exited.
func cancelRunningTransfer(t *testing.T, bin, cfgPath, dbPath, statePath, wantStage string, args ...string) string {
	t.Helper()
	cmd, sc, stderr := startTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=2s"}, args...)
	var id string
	for deadline := time.Now().Add(10 * time.Second); ; {
		if running := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", wantStage); len(running) > 0 {
			id, _ = running[0]["id"].(string)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s transfer while the command runs; stderr=%s", wantStage, stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "cancel", id)
	for sc.Scan() {
	}
	if code := exitCode(t, cmd.Wait()); code != 130 {
		t.Fatalf("cancelled command exit = %d, want 130", code)
	}
	cancelled := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "show", id)
	if cancelled["stage"] != "cancelled" || cancelled["cancel_requested"] != true {
		t.Fatalf("transfer after cancel = %v, want cancelled with the flag recorded", cancelled)
	}
	return id
}

// TestE2ERetryCancelledTransfers: a cancelled Transfer retries from its
// recorded request. The cancel-requested flag the first run recorded must
// not leak into the retry: with the lease TTL at one second, a lingering
// flag would make the new owner's first heartbeat cancel the retry, which
// instead runs to completed. The upload resumes from its saved parts; the
// download starts over, keeping no partial state, and still lands the full
// file.
func TestE2ERetryCancelledTransfers(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "locks.ttl_seconds", "1")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	upID := cancelRunningTransfer(t, bin, cfgPath, dbPath, statePath, "uploading",
		"--json", "cp", big, "/big.bin", "--upload-part-size-kb", "1024", "--upload-threads", "1")

	// Slow the retry down (half a second per part over up to 12 parts), so
	// it outlives several heartbeat ticks: completing proves the recorded
	// cancel request was cleared on takeover.
	stdout, stderr2, err := runTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=500ms"}, "",
		"--json", "transfers", "retry", upID)
	if err != nil {
		t.Fatalf("retry of cancelled upload: %v stdout=%s stderr=%s", err, stdout, stderr2)
	}
	up := decodeData(t, stdout)
	if up["stage"] != "completed" || up["id"] != upID || up["cancel_requested"] != false {
		t.Fatalf("retried upload transfer = %v, want completed with the flag cleared", up)
	}

	// A cancelled download retries from scratch and lands the whole file.
	dlDest := filepath.Join(dir, "out", "big.bin")
	dlID := cancelRunningTransfer(t, bin, cfgPath, dbPath, statePath, "downloading",
		"--json", "get", "/big.bin", dlDest)
	dl := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "retry", dlID)
	if dl["stage"] != "completed" || dl["id"] != dlID || dl["bytes_done"] != float64(12*mib) {
		t.Fatalf("retried download transfer = %v, want completed with all bytes", dl)
	}
	restored, err := os.ReadFile(dlDest)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(big)
	if err != nil {
		t.Fatal(err)
	}
	if blake3Hex(restored) != blake3Hex(source) {
		t.Fatal("retried download content differs from the source")
	}
}

// TestE2ERetryRejectsActiveCompletedUnknown: only a failed, cancelled, or
// interrupted Transfer retries. A completed one is done; a running one
// still has its owner — its fresh lease is what keeps it active — so the
// retry is a usage error answered from the index, fast, while the owning
// td cp keeps running undisturbed. An unknown ID is not found.
func TestE2ERetryRejectsActiveCompletedUnknown(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	// The owner's cancel poll rides the lease heartbeat: shorten the TTL so
	// the cancelled cp below stops within the test.
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "config", "set", "locks.ttl_seconds", "3")

	small := e2eLocalFile(t, dir, "small.txt", "small")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", small, "/small.txt")
	completed := findTransfer(t, listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "completed"), "upload")
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE",
		"transfers", "retry", fmt.Sprint(completed["id"]))

	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_TRANSFER_NOT_FOUND",
		"transfers", "retry", "00000000-0000-4000-8000-000000000000")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "big.bin"), 12*mib)
	cmd, sc, stderr := startTD(t, bin, cfgPath, dbPath, statePath, []string{"TD_FAKE_TRANSFER_DELAY=2s"},
		"--json", "cp", big, "/big.bin", "--upload-part-size-kb", "1024", "--upload-threads", "1")
	var id string
	for deadline := time.Now().Add(10 * time.Second); ; {
		if running := listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "uploading"); len(running) > 0 {
			id, _ = running[0]["id"].(string)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cp never reached uploading; stderr=%s", stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	sent := time.Now()
	runE2EExpectError(t, bin, cfgPath, dbPath, statePath, 2, "ERR_USAGE", "transfers", "retry", id)
	if took := time.Since(sent); took > 10*time.Second {
		t.Fatalf("rejecting the running Transfer took %s, want a fast answer from the index, not the Session-lock wait", took)
	}

	// The rejected retry touched nothing: the owner keeps the Transfer and
	// finishes it once asked to stop.
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "cancel", id)
	for sc.Scan() {
	}
	if code := exitCode(t, cmd.Wait()); code != 130 {
		t.Fatalf("owner exit after cancel = %d, want 130", code)
	}

	// Human output prints the ended Transfer in the list's row format.
	failedEnv := []string{"TD_FAKE_FAIL_UPLOAD_AFTER_PARTS=1"}
	if _, _, err := runTD(t, bin, cfgPath, dbPath, statePath, failedEnv, "",
		"--json", "cp", big, "/failed.bin", "--upload-part-size-kb", "1024", "--upload-threads", "1"); err == nil {
		t.Fatal("expected cp to fail")
	}
	failed := findTransfer(t, listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "failed"), "upload")
	failedID, _ := failed["id"].(string)
	human, stderrHuman, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", "transfers", "retry", failedID)
	if err != nil {
		t.Fatalf("human retry: %v stderr=%s", err, stderrHuman)
	}
	want := fmt.Sprintf("%s  completed  upload  12.0 MB/12.0 MB  %s -> /failed.bin\n", failedID, big)
	if human != want {
		t.Fatalf("human retry = %q, want %q", human, want)
	}
}

// TestE2ERetryFailedAlbumAndRecursiveUpload: the multi-item kinds retry
// from their recorded options too — an album upload's member list lives in
// options.sources, a recursive upload re-walks its recorded source — each
// ending its Transfer completed once the cause of the failure is gone. The
// first member is the unreadable one, so the failed runs published nothing
// the retry could conflict with.
func TestE2ERetryFailedAlbumAndRecursiveUpload(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	unreadable := e2eLocalFile(t, dir, "a.txt", "a")
	b := e2eLocalFile(t, dir, "b.txt", "b")
	c := e2eLocalFile(t, dir, "c.txt", "c")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "",
		"--json", "cp", unreadable, b, c, "/albums/"); err == nil {
		t.Fatal("expected the album cp to fail on the unreadable member")
	}
	album := findTransfer(t, listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "failed"), "album_upload")
	if err := os.Chmod(unreadable, 0o644); err != nil {
		t.Fatal(err)
	}
	retried := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "retry", fmt.Sprint(album["id"]))
	if retried["stage"] != "completed" || retried["items_done"] != float64(3) || retried["items_total"] != float64(3) {
		t.Fatalf("retried album transfer = %v, want completed with 3/3 items", retried)
	}
	ls := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "ls", "/albums")
	if entries, _ := ls["entries"].([]any); len(entries) != 3 {
		t.Fatalf("ls /albums = %v, want the 3 retried members", ls)
	}

	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	blocked := e2eLocalFile(t, tree, "a.txt", "a")
	e2eLocalFile(t, tree, "b.txt", "b")
	e2eLocalFile(t, tree, "c.txt", "c")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "",
		"--json", "cp", "--recursive", tree, "/tree"); err == nil {
		t.Fatal("expected the recursive cp to fail on the unreadable file")
	}
	rec := findTransfer(t, listTransfers(t, bin, cfgPath, dbPath, statePath, "--stage", "failed"), "recursive_upload")
	if err := os.Chmod(blocked, 0o644); err != nil {
		t.Fatal(err)
	}
	retriedRec := runE2EJSON(t, bin, cfgPath, dbPath, statePath, "transfers", "retry", fmt.Sprint(rec["id"]))
	if retriedRec["stage"] != "completed" || retriedRec["items_done"] != float64(3) ||
		retriedRec["source"] != tree || retriedRec["dest"] != "/tree" {
		t.Fatalf("retried recursive transfer = %v, want completed with 3 items from %s to /tree", retriedRec, tree)
	}
}
