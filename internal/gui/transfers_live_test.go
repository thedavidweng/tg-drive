//go:build gui

package gui_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
	"github.com/thedavidweng/tg-drive/internal/transfer"
)

func TestLoginKeepsPhoneOffDiskWhileTransferRuns(t *testing.T) {
	seedDrive(t, nil)
	// seedDrive sets TD_PHONE; clear it so Login takes the missing-phone
	// persist path instead of skipping it.
	t.Setenv("TD_PHONE", "")
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "200ms")
	if _, err := service.SetConfig(service.Options{}, "upload.threads", "4"); err != nil {
		t.Fatal(err)
	}
	svc := openGUI(t)
	ctx := context.Background()
	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "login-guard.bin", 12*1024*1024)}, "/", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "uploading")
	before, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Auth.Login(ctx, "+15550001", false); err == nil {
		t.Fatal("Login succeeded while a GUI transfer was running")
	} else {
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) || guiErr.Code != "ERR_OPERATION_LOCKED" {
			t.Fatalf("Login error = %v, want ERR_OPERATION_LOCKED", err)
		}
	}
	after, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected Login changed the config file")
	}
	if n := svc.RunningTransfers(); n != 1 {
		t.Fatalf("RunningTransfers = %d, want 1", n)
	}
	waitForStage(t, svc, ids[0], "completed")
	if n := svc.RunningTransfers(); n != 0 {
		t.Fatalf("RunningTransfers after completion = %d, want 0", n)
	}
	newPromptRecorder(svc, map[string]string{"code": "12345"})
	res, err := svc.Auth.Login(ctx, "+15550001", false)
	if err != nil {
		t.Fatalf("idle Login: %v", err)
	}
	// The seeded fake already authenticated this account, so a fresh code
	// is unnecessary; either path proves the idle phone fill works.
	if res.User.DisplayName != "Test User" || res.User.UserID != 42 {
		t.Fatalf("idle Login = %+v, want Test User (42)", res)
	}
	idle, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(idle, []byte("+15550001")) {
		t.Fatal("idle Login did not persist the phone")
	}
}

func TestSetupKeepsRunningTransferAndConfig(t *testing.T) {
	seedDrive(t, nil)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "200ms")
	if _, err := service.SetConfig(service.Options{}, "upload.threads", "4"); err != nil {
		t.Fatal(err)
	}
	svc := openGUI(t)
	ctx := context.Background()
	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "running.bin", 12*1024*1024)}, "/", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "uploading")
	before, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001"); err == nil {
		t.Fatal("Setup succeeded while a GUI transfer was running")
	}
	after, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected Setup changed the config file")
	}
	if n := svc.RunningTransfers(); n != 1 {
		t.Fatalf("RunningTransfers = %d, want 1", n)
	}
	waitForStage(t, svc, ids[0], "completed")
	if n := svc.RunningTransfers(); n != 0 {
		t.Fatalf("RunningTransfers after completion = %d, want 0", n)
	}
	if _, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001"); err != nil {
		t.Fatalf("idle Setup: %v", err)
	}
}

// TestDownloadSetupInterleaveKeepsOneGeneration interleaves idle Downloads
// with Setup-driven reopens: Download resolves the App under the same
// lifecycle lock as the submit, so a listing never runs on an App whose
// database a concurrent reopen already closed.
func TestDownloadSetupInterleaveKeepsOneGeneration(t *testing.T) {
	seedDrive(t, map[string]string{"/shared.txt": "shared-bytes"})
	svc := openGUI(t)
	ctx := context.Background()

	const rounds = 10
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			dest := t.TempDir()
			id, err := svc.Transfers.Download(ctx, "/shared.txt", dest, gui.DownloadOptions{})
			if err != nil {
				var guiErr *gui.Error
				if errors.As(err, &guiErr) && (guiErr.Code == "ERR_OPERATION_LOCKED" || guiErr.Code == "ERR_CANCELLED") {
					return
				}
				t.Errorf("download call %T: %v", err, err)
				return
			}
			waitForStage(t, svc, id, "completed")
			got, err := os.ReadFile(filepath.Join(dest, "shared.txt"))
			if err != nil {
				t.Errorf("read result: %v", err)
				return
			}
			if !bytes.Equal(got, []byte("shared-bytes")) {
				t.Error("downloaded bytes mismatch")
			}
		}()
		go func() {
			defer wg.Done()
			_, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001")
			if err != nil {
				var guiErr *gui.Error
				if errors.As(err, &guiErr) && (guiErr.Code == "ERR_OPERATION_LOCKED" || guiErr.Code == "ERR_CANCELLED") {
					return
				}
				t.Errorf("setup: %T: %v", err, err)
			}
		}()
	}
	wg.Wait()
}

func TestSetupAndSubmitDoNotLoseRunningTransfer(t *testing.T) {
	seedDrive(t, nil)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "200ms")
	svc := openGUI(t)
	ctx := context.Background()
	file := bigFile(t, "concurrent.bin", 12*1024*1024)
	start := make(chan struct{})
	type submitted struct {
		ids []string
		err error
	}
	uploaded := make(chan submitted, 1)
	setup := make(chan error, 1)
	go func() {
		<-start
		ids, err := svc.Transfers.Upload(ctx, []string{file}, "/", gui.UploadOptions{})
		uploaded <- submitted{ids, err}
	}()
	go func() {
		<-start
		_, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001")
		setup <- err
	}()
	close(start)
	result := <-uploaded
	if result.err != nil {
		t.Fatalf("Upload: %v", result.err)
	}
	if err := <-setup; err != nil {
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) || guiErr.Code != "ERR_OPERATION_LOCKED" {
			t.Fatalf("Setup: %v", err)
		}
	}
	if n := svc.RunningTransfers(); n != 1 {
		t.Fatalf("RunningTransfers after concurrent setup and submission = %d, want 1", n)
	}
	waitForStage(t, svc, result.ids[0], "completed")
}

func TestClosedServicesRejectTransferSubmission(t *testing.T) {
	seedDrive(t, nil)
	svc, closeServices, err := gui.Open()
	if err != nil {
		t.Fatal(err)
	}
	file := bigFile(t, "after-close.bin", 1024)
	closeServices()
	ctx := context.Background()
	if _, err := svc.Transfers.Upload(ctx, []string{file}, "/", gui.UploadOptions{}); err == nil {
		t.Fatal("Upload succeeded after close")
	}
	if _, err := svc.Transfers.Retry(ctx, "missing"); err == nil {
		t.Fatal("Retry succeeded after close")
	}
}

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
