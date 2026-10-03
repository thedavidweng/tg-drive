//go:build gui

package gui_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive-cli/internal/gui"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/transfer"
)

// The GUI holds one Telegram client for its session and one App per bound
// channel (ADR 0031): switching channels only changes which App the views
// read, so nothing running is torn down.

// TestSwitchingChannelsKeepsRunningTransfers: an upload running on one
// drive completes while the user switches to another drive mid-upload.
func TestSwitchingChannelsKeepsRunningTransfers(t *testing.T) {
	archiveID := seedDriveAndChannel(t, nil, "Archive")
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "100ms")
	svc := openGUI(t)
	ctx := context.Background()

	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "big.bin", 12*1024*1024)}, "/", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "uploading")
	if _, err := svc.Channels.Bind(ctx, gui.BindRequest{ChannelID: strconv.FormatInt(archiveID, 10)}); err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "completed")

	channels, err := svc.Channels.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Channels.Select(ctx, boundChannel(t, channels, "Drive").ChannelID); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.Drive.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "big.bin" {
		t.Fatalf("Drive after the switch = %+v, want the upload that ran through it", entries)
	}
}

// TestSettingsApplyWithoutRestart: a Transfer concurrency change in
// Settings governs the next uploads at once, and saving it leaves the GUI's
// own session path out of the config file the CLI shares.
func TestSettingsApplyWithoutRestart(t *testing.T) {
	seedDrive(t, nil)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "100ms")
	svc := openGUI(t)
	ctx := context.Background()

	if _, err := svc.Settings.Set(ctx, "transfers.concurrency", "1"); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "a.bin", 12*1024*1024)}, "/", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "b.bin", 12*1024*1024)}, "/", gui.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, first[0], "uploading")
	if got := lastSeen(t, svc, second[0]); got.Stage != "queued" {
		t.Fatalf("second upload = %s while the first runs, want queued under concurrency 1", got.Stage)
	}
	waitForStage(t, svc, second[0], "completed")

	data, err := os.ReadFile(os.Getenv("TD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), gui.SessionFileName) {
		t.Fatalf("config file saved the GUI's session path:\n%s", data)
	}
}

// TestRunningTransfersCountOnlyThisGUI: the quit prompt warns about the
// Transfers quitting would interrupt, which are this GUI's own; a td
// process's upload keeps running when the GUI quits.
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
}

// TestIndexSyncAnnouncesChannelsBoundElsewhere: td init binding a drive in
// a terminal reaches the GUI's channel switcher as a channels-changed
// event carrying the new list.
func TestIndexSyncAnnouncesChannelsBoundElsewhere(t *testing.T) {
	archiveID := seedDriveAndChannel(t, nil, "Archive")
	svc := openGUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	got := make(chan gui.ChannelsChanged, 16)
	svc.SetChannelsEmitter(func(name string, data any) {
		if name == gui.EventChannelsChanged {
			got <- data.(gui.ChannelsChanged)
		}
	})
	svc.StartSync(ctx, 20*time.Millisecond)

	cli, closeCLI, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeCLI()
	if _, err := cli.InitRoot(ctx, t.TempDir(), "", "", strconv.FormatInt(archiveID, 10)); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-got:
			for _, ch := range ev.Channels {
				if ch.Title == "Archive" {
					if !boundChannel(t, ev.Channels, "Drive").Active {
						t.Fatalf("channels = %+v, want Drive still the active one", ev.Channels)
					}
					return
				}
			}
		case <-deadline:
			t.Fatal("no channels-changed event lists the channel td init bound")
		}
	}
}

// TestLoginAgainReplacesAStuckLogin: a login whose prompt never got an
// answer (the webview reloaded and lost it) must not block every later
// login; logging in again takes over.
func TestLoginAgainReplacesAStuckLogin(t *testing.T) {
	freshMachine(t)
	svc := openGUI(t)
	ctx := context.Background()
	if _, err := svc.Auth.Setup(ctx, "1234", "hash", "+15550001"); err != nil {
		t.Fatal(err)
	}
	prompted := make(chan struct{}, 1)
	svc.SetPromptEmitter(func(gui.AuthPrompt) { prompted <- struct{}{} })
	stuck := make(chan error, 1)
	go func() {
		_, err := svc.Auth.Login(ctx, "", false)
		stuck <- err
	}()
	select {
	case <-prompted:
	case <-time.After(10 * time.Second):
		t.Fatal("the first login never prompted")
	}

	newPromptRecorder(svc, map[string]string{"code": "12345"})
	res, err := svc.Auth.Login(ctx, "", false)
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if res.User.UserID != 42 {
		t.Fatalf("second login = %+v, want Test User (42)", res)
	}
	select {
	case err := <-stuck:
		if err == nil {
			t.Fatal("the replaced login must end with an error, not succeed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the replaced login never returned")
	}
}
