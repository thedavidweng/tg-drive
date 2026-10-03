//go:build gui

package gui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive-cli/internal/gui"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/transfer"
)

// transferEvents is the test Emitter for the transfer events: it records
// every typed event the facade emits, in order.
type transferEvents struct {
	mu       sync.Mutex
	stages   []gui.Transfer
	progress []gui.Transfer
	removed  []gui.TransferRemoved
}

func (r *transferEvents) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch name {
	case gui.EventTransferStage:
		r.stages = append(r.stages, data.(gui.Transfer))
	case gui.EventTransferProgress:
		r.progress = append(r.progress, data.(gui.Transfer))
	case gui.EventTransferRemoved:
		r.removed = append(r.removed, data.(gui.TransferRemoved))
	}
}

func (r *transferEvents) stageSequence(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, t := range r.stages {
		if t.ID == id {
			out = append(out, t.Stage)
		}
	}
	return out
}

func (r *transferEvents) progressFor(id string) []gui.Transfer {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []gui.Transfer
	for _, t := range r.progress {
		if t.ID == id {
			out = append(out, t)
		}
	}
	return out
}

func (r *transferEvents) removedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.removed {
		out = append(out, e.ID)
	}
	return out
}

// waitForStage polls the facade's List until the Transfer with id reaches
// one of the given stages, and returns it.
func waitForStage(t *testing.T, svc *gui.Services, id string, stages ...string) gui.Transfer {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		list, err := svc.Transfers.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range list.Active {
			if tr.ID == id && slices.Contains(stages, tr.Stage) {
				return tr
			}
		}
		for _, tr := range list.History {
			if tr.ID != id {
				continue
			}
			if slices.Contains(stages, tr.Stage) {
				return tr
			}
			// A Transfer in a terminal stage never leaves it: fail now
			// instead of waiting out the deadline.
			t.Fatalf("transfer %s ended %s (%s %s), want one of %v", id, tr.Stage, tr.ErrorCode, tr.ErrorMessage, stages)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("transfer %s never reached one of %v; last seen %+v", id, stages, lastSeen(t, svc, id))
	return gui.Transfer{}
}

func lastSeen(t *testing.T, svc *gui.Services, id string) gui.Transfer {
	t.Helper()
	list, err := svc.Transfers.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range append(list.Active, list.History...) {
		if tr.ID == id {
			return tr
		}
	}
	return gui.Transfer{}
}

// shortTransferLeases shortens the owner's lease TTL (locks.ttl_seconds) so
// leases renew and expire within the test's deadlines. The config file is
// shared by the GUI and the CLI front end.
func shortTransferLeases(t *testing.T) {
	t.Helper()
	cfg := os.Getenv("TD_CONFIG")
	f, err := os.OpenFile(cfg, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[locks]\nttl_seconds = 3\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// bigFile writes a file that takes the fake's resumable path (uploads above
// 10 MiB), in 1 MiB parts, so transfers span several progress reports.
func bigFile(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i % 251)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTransfersUploadEmitsTheStageAndProgressSequence(t *testing.T) {
	seedDrive(t, nil)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "150ms")
	svc := openGUI(t)
	rec := &transferEvents{}
	svc.SetTransferEmitter(rec.emit)
	ctx := context.Background()

	const size = 12 * 1024 * 1024
	src := bigFile(t, "big.bin", size)
	ids, err := svc.Transfers.Upload(ctx, []string{src}, "/uploads", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("Upload of one file = %v ids, want 1", ids)
	}

	done := waitForStage(t, svc, ids[0], "completed")
	if done.Kind != "upload" || done.Dest != "/uploads/big.bin" || done.FrontEnd != "gui" {
		t.Fatalf("completed transfer = %+v, want a gui upload to /uploads/big.bin", done)
	}
	if done.BytesDone != size || done.BytesTotal != size {
		t.Fatalf("completed transfer bytes = %d/%d, want %d/%d", done.BytesDone, done.BytesTotal, size, size)
	}

	seq := rec.stageSequence(ids[0])
	if len(seq) < 2 || seq[0] != "queued" || seq[len(seq)-1] != "completed" {
		t.Fatalf("stage sequence = %v, want queued ... completed", seq)
	}
	if !slices.Contains(seq, "uploading") {
		t.Fatalf("stage sequence = %v, want an uploading stage", seq)
	}
	if prog := rec.progressFor(ids[0]); len(prog) == 0 {
		t.Fatal("no transfer-progress events for a multi-part upload")
	} else if last := prog[len(prog)-1]; last.BytesDone <= 0 || last.BytesDone > size {
		t.Fatalf("last progress event bytes_done = %d, want within (0, %d]", last.BytesDone, size)
	}
}

func TestTransfersUploadSeveralFilesIsOneAlbumTransfer(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	ids, err := svc.Transfers.Upload(ctx, []string{
		bigFile(t, "a.txt", 100),
		bigFile(t, "b.txt", 100),
	}, "/album", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("Upload of two files = %v ids, want one album transfer", ids)
	}
	done := waitForStage(t, svc, ids[0], "completed")
	if done.Kind != "album_upload" || done.ItemsTotal != 2 || done.ItemsDone != 2 {
		t.Fatalf("completed transfer = %+v, want an album_upload of 2 items", done)
	}
}

func TestTransfersUploadDirectoryIsARecursiveTransfer(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "mydir")
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"a.txt": "aaa", "sub/b.txt": "bbb"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A picked directory lands under the current remote directory by name,
	// the way a file manager copy would place it.
	ids, err := svc.Transfers.Upload(ctx, []string{dir}, "/tree", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("Upload of a directory = %v ids, want one recursive transfer", ids)
	}
	done := waitForStage(t, svc, ids[0], "completed")
	if done.Kind != "recursive_upload" || done.ItemsDone != 2 {
		t.Fatalf("completed transfer = %+v, want a recursive_upload with 2 items done", done)
	}
	entries, err := svc.Drive.List(ctx, "/tree/mydir/sub")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "b.txt" {
		t.Fatalf("List(/tree/mydir/sub) = %+v, want b.txt", entries)
	}
}

func TestTransfersDownloadWritesTheFile(t *testing.T) {
	seedDrive(t, map[string]string{
		"/notes.txt":      "hello",
		"/photos/cat.jpg": "meow",
	})
	svc := openGUI(t)
	ctx := context.Background()
	dest := t.TempDir()

	id, err := svc.Transfers.Download(ctx, "/notes.txt", dest, gui.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForStage(t, svc, id, "completed")
	if done.Kind != "download" || done.Dest != filepath.Join(dest, "notes.txt") {
		t.Fatalf("completed transfer = %+v, want a download into the picked directory", done)
	}
	body, err := os.ReadFile(filepath.Join(dest, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("downloaded notes.txt = %q, want hello", body)
	}

	id, err = svc.Transfers.Download(ctx, "/photos", dest, gui.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done = waitForStage(t, svc, id, "completed")
	if done.Kind != "recursive_download" {
		t.Fatalf("completed transfer = %+v, want a recursive_download for a directory", done)
	}
	body, err = os.ReadFile(filepath.Join(dest, "photos", "cat.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "meow" {
		t.Fatalf("downloaded photos/cat.jpg = %q, want meow", body)
	}
}

func TestTransfersCancelEndsARunningUpload(t *testing.T) {
	seedDrive(t, nil)
	shortTransferLeases(t)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "400ms")
	svc := openGUI(t)
	rec := &transferEvents{}
	svc.SetTransferEmitter(rec.emit)
	ctx := context.Background()

	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "slow.bin", 24*1024*1024)}, "/slow.bin", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "uploading")

	tr, err := svc.Transfers.Cancel(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if !tr.CancelRequested {
		t.Fatalf("Cancel returned %+v, want cancel_requested set", tr)
	}
	done := waitForStage(t, svc, ids[0], "cancelled")
	if done.Stage != "cancelled" {
		t.Fatalf("transfer ended %s, want cancelled", done.Stage)
	}
	if seq := rec.stageSequence(ids[0]); seq[len(seq)-1] != "cancelled" {
		t.Fatalf("stage sequence = %v, want it to end cancelled", seq)
	}
}

func TestTransfersRetryRerunsAFailedUpload(t *testing.T) {
	seedDrive(t, nil)
	shortTransferLeases(t)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_FAIL_UPLOAD_AFTER_PARTS", "2")
	svc := openGUI(t)
	rec := &transferEvents{}
	svc.SetTransferEmitter(rec.emit)
	ctx := context.Background()

	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "flake.bin", 12*1024*1024)}, "/flake.bin", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForStage(t, svc, ids[0], "failed")
	if failed.ErrorCode == "" || failed.ErrorMessage == "" {
		t.Fatalf("failed transfer = %+v, want an error code and message", failed)
	}

	tr, err := svc.Transfers.Retry(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if tr.Stage != "queued" {
		t.Fatalf("Retry returned stage %q, want queued", tr.Stage)
	}
	done := waitForStage(t, svc, ids[0], "completed")
	if done.ErrorCode != "" {
		t.Fatalf("retried transfer = %+v, want the error cleared", done)
	}
	// The retry resumes from the parts the failed run saved, so the
	// Transfer keeps its ID through both runs.
	seq := rec.stageSequence(ids[0])
	if !slices.Contains(seq, "failed") || seq[len(seq)-1] != "completed" {
		t.Fatalf("stage sequence = %v, want failed ... completed", seq)
	}

	if _, err := svc.Transfers.Retry(ctx, ids[0]); err == nil {
		t.Fatal("Retry of a completed transfer succeeded, want ERR_USAGE")
	} else {
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
			t.Fatalf("Retry of a completed transfer error = %v, want ERR_USAGE", err)
		}
	}
}

func TestTransfersRetryRejectsAnActiveTransfer(t *testing.T) {
	seedDrive(t, nil)
	shortTransferLeases(t)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "400ms")
	svc := openGUI(t)
	ctx := context.Background()

	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "slow.bin", 24*1024*1024)}, "/slow.bin", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "uploading")

	_, err = svc.Transfers.Retry(ctx, ids[0])
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
		t.Fatalf("Retry of a running transfer error = %v, want ERR_USAGE", err)
	}
	if _, err := svc.Transfers.Cancel(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "cancelled")
}

// A Transfer another front end started is picked up by the index poll and
// reported through the same typed events, and the facade cancels it through
// the shared index (ADR 0033).
func TestTransfersSyncSeesAndCancelsACLITransfer(t *testing.T) {
	seedDrive(t, nil)
	shortTransferLeases(t)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "400ms")
	svc := openGUI(t)
	rec := &transferEvents{}
	svc.SetTransferEmitter(rec.emit)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// The initial List baselines the snapshot, so the CLI Transfer's first
	// event is its discovery, not a replay of history.
	if _, err := svc.Transfers.List(ctx); err != nil {
		t.Fatal(err)
	}
	svc.StartSync(ctx, 20*time.Millisecond)

	// The CLI front end: the same index, its own session and Manager.
	cli, closeCLI, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeCLI()
	manager := transfer.New(cli, transfer.Options{FrontEnd: transfer.FrontEndCLI})
	src := bigFile(t, "cli.bin", 24*1024*1024)
	handle, err := manager.SubmitUpload(ctx, transfer.Upload{
		Source: src, Dest: "/cli.bin", Policy: service.ConflictFail,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The poll notices the new Transfer and reports its stages as events.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if seq := rec.stageSequence(handle.ID()); slices.Contains(seq, "uploading") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if seq := rec.stageSequence(handle.ID()); !slices.Contains(seq, "uploading") {
		t.Fatalf("no transfer-stage event reached uploading for the CLI transfer; events: %v", seq)
	}

	if _, err := svc.Transfers.Cancel(ctx, handle.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Wait(); err == nil {
		t.Fatal("the CLI transfer completed despite the GUI's cancel")
	}
	tr := waitForStage(t, svc, handle.ID(), "cancelled")
	if tr.FrontEnd != "cli" {
		t.Fatalf("cancelled transfer front_end = %q, want cli", tr.FrontEnd)
	}
}

func TestTransfersClearFinishedRemovesHistory(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	rec := &transferEvents{}
	svc.SetTransferEmitter(rec.emit)
	ctx := context.Background()

	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "a.txt", 10)}, "/a.txt", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "completed")
	if _, err := svc.Transfers.List(ctx); err != nil {
		t.Fatal(err)
	}

	n, err := svc.Transfers.ClearFinished(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ClearFinished = %d, want 1", n)
	}
	list, err := svc.Transfers.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Active) != 0 || len(list.History) != 0 {
		t.Fatalf("List after ClearFinished = %+v, want empty", list)
	}
	if !slices.Contains(rec.removedIDs(), ids[0]) {
		t.Fatalf("no transfer-removed event for %s; removed: %v", ids[0], rec.removedIDs())
	}
}

// scriptPicker answers the facade's file dialogs with fixed paths, the way
// cmd/td-gui's preview picker does.
type scriptPicker struct {
	files []string
	dir   string
}

func (p scriptPicker) PickFiles(context.Context) ([]string, error) { return p.files, nil }
func (p scriptPicker) PickDirectory(context.Context) (string, error) {
	return p.dir, nil
}

func TestTransfersPickersDelegateToTheConnectedDialog(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	if _, err := svc.Transfers.PickFiles(ctx); err == nil {
		t.Fatal("PickFiles without a connected dialog succeeded, want ERR_USAGE")
	} else {
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
			t.Fatalf("PickFiles error = %v, want ERR_USAGE", err)
		}
	}

	svc.SetFilePicker(scriptPicker{files: []string{"/tmp/a.txt", "/tmp/b.txt"}, dir: "/tmp/dir"})
	files, err := svc.Transfers.PickFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"/tmp/a.txt", "/tmp/b.txt"}) {
		t.Fatalf("PickFiles = %v, want the picker's answer", files)
	}
	dir, err := svc.Transfers.PickDirectory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/dir" {
		t.Fatalf("PickDirectory = %q, want /tmp/dir", dir)
	}
}
