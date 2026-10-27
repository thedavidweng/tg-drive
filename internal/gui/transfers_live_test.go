//go:build gui

package gui_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
	"github.com/thedavidweng/tg-drive/internal/transfer"
)

// TestTransfersSurviveAChannelSwitch switches the browsed channel while an
// upload runs on another one: the upload keeps running to completion on the
// channel it was submitted to.
func TestTransfersSurviveAChannelSwitch(t *testing.T) {
	backupsID := seedDriveAndChannel(t, nil, "Backups")
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "200ms")
	svc := openGUI(t)
	ctx := context.Background()
	backups := strconv.FormatInt(backupsID, 10)

	// Bind Backups (which activates it), then go back to Drive.
	if _, err := svc.Channels.Bind(ctx, gui.BindRequest{ChannelID: backups}); err != nil {
		t.Fatal(err)
	}
	channels, err := svc.Channels.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	drive := boundChannel(t, channels, "Drive")
	if _, err := svc.Channels.Select(ctx, drive.ChannelID); err != nil {
		t.Fatal(err)
	}

	const size = 12 * 1024 * 1024
	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "big.bin", size)}, "/uploads", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	running := waitForStage(t, svc, ids[0], "uploading")
	if running.Channel != drive.ChannelID {
		t.Fatalf("running transfer channel = %s, want Drive %s", running.Channel, drive.ChannelID)
	}

	if _, err := svc.Channels.Select(ctx, backups); err != nil {
		t.Fatal(err)
	}
	// The Drive view follows the switch at once.
	if entries, err := svc.Drive.List(ctx, "/"); err != nil || len(entries) != 0 {
		t.Fatalf("List(/) on Backups = %+v, %v; want an empty drive", entries, err)
	}

	done := waitForStage(t, svc, ids[0], "completed")
	if done.BytesDone != size || done.Channel != drive.ChannelID {
		t.Fatalf("completed transfer = %+v, want %d bytes on Drive", done, size)
	}
	if entries, err := svc.Drive.List(ctx, "/"); err != nil || len(entries) != 0 {
		t.Fatalf("List(/) on Backups after the upload = %+v, %v; want it still empty", entries, err)
	}
	if _, err := svc.Channels.Select(ctx, drive.ChannelID); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.Drive.List(ctx, "/uploads")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "big.bin" {
		t.Fatalf("List(/uploads) on Drive = %+v, want big.bin", entries)
	}
}

// TestRunningTransfersCountOnlyThisGUI: the quit prompt warns about the
// Transfers quitting would interrupt, which are this GUI's own; a td
// process's upload keeps running when the GUI quits, so it does not count.
func TestRunningTransfersCountOnlyThisGUI(t *testing.T) {
	seedDrive(t, nil)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "100ms")
	svc := openGUI(t)
	ctx := context.Background()

	cli, closeCLI, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeCLI()
	h, err := transfer.New(cli, transfer.Options{FrontEnd: transfer.FrontEndCLI}).SubmitUpload(ctx, transfer.Upload{
		Source: bigFile(t, "cli.bin", 12*1024*1024), Dest: "/cli.bin", Policy: service.ConflictFail,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, h.ID(), "uploading")
	if n := svc.RunningTransfers(); n != 0 {
		t.Fatalf("RunningTransfers with only a CLI upload = %d, want 0", n)
	}
	// Two in-process fakes over one state file would hand out the same
	// message IDs, so the CLI upload is cancelled (from the GUI) before it
	// publishes, and only then does the GUI upload start.
	if _, err := svc.Transfers.Cancel(ctx, h.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Wait(); err == nil {
		t.Fatal("the CLI upload cancelled from the GUI must end cancelled")
	}
	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "gui.bin", 12*1024*1024)}, "/", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := svc.RunningTransfers(); n != 1 {
		t.Fatalf("RunningTransfers with one GUI upload = %d, want 1", n)
	}
	waitForStage(t, svc, ids[0], "completed")
	if n := svc.RunningTransfers(); n != 0 {
		t.Fatalf("RunningTransfers after the GUI upload finished = %d, want 0", n)
	}
}

// TestTransfersConcurrencySettingAppliesLive lowers and raises
// transfers.concurrency through Settings while the GUI runs: the running
// queue follows each saved value without a restart.
func TestTransfersConcurrencySettingAppliesLive(t *testing.T) {
	seedDrive(t, nil)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "200ms")
	svc := openGUI(t)
	ctx := context.Background()

	entry, err := svc.Settings.Set(ctx, "transfers.concurrency", "1")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Value != 1 {
		t.Fatalf("Set returned %+v, want value 1", entry)
	}

	const size = 12 * 1024 * 1024
	first, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "one.bin", size)}, "/", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, first[0], "uploading")
	second, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "two.bin", size)}, "/", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// With one slot the second upload stays queued while the first runs.
	time.Sleep(time.Second)
	if st := lastSeen(t, svc, first[0]).Stage; st != "uploading" {
		t.Fatalf("first upload is %s, want still uploading", st)
	}
	if st := lastSeen(t, svc, second[0]).Stage; st != "queued" {
		t.Fatalf("second upload is %s while the only slot is taken, want queued", st)
	}

	// Raising the limit starts the queued upload while the first still runs.
	if _, err := svc.Settings.Set(ctx, "transfers.concurrency", "2"); err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, second[0], "hashing", "uploading", "publishing")
	if st := lastSeen(t, svc, first[0]).Stage; st != "uploading" && st != "publishing" {
		t.Fatalf("first upload is %s once the second started, want still running", st)
	}
	waitForStage(t, svc, first[0], "completed")
	waitForStage(t, svc, second[0], "completed")
}
