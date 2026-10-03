//go:build gui

package gui_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// seedLogin points td at a temp machine and logs the fake account in through
// the CLI's service path, without binding a channel.
func seedLogin(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TD_FAKE_TELEGRAM", "1")
	t.Setenv("TD_FAKE_TELEGRAM_STATE", filepath.Join(dir, "fake.json"))
	t.Setenv("TD_CONFIG", filepath.Join(dir, "config.toml"))
	t.Setenv("TD_DB", filepath.Join(dir, "td.db"))
	t.Setenv("TD_SESSION", filepath.Join(dir, "session.json"))
	t.Setenv("TD_API_ID", "1")
	t.Setenv("TD_API_HASH", "hash")
	t.Setenv("TD_PHONE", "+1000")

	app, closeApp, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	if _, err := app.AuthLogin(context.Background(),
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{}); err != nil {
		t.Fatal(err)
	}
}

// seedDriveAndChannel seeds a bound "Drive" with files the way the CLI would,
// plus a second Telegram channel that exists but is bound to nothing, and
// returns the unbound channel's Telegram ID.
func seedDriveAndChannel(t *testing.T, files map[string]string, title string) int64 {
	t.Helper()
	seedDrive(t, files)
	app, closeApp, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	ch, err := app.TG.CreateChannel(context.Background(), title)
	if err != nil {
		t.Fatal(err)
	}
	return ch.ID
}

// boundChannel looks one channel up in a List result by title.
func boundChannel(t *testing.T, channels []gui.ChannelInfo, title string) gui.ChannelInfo {
	t.Helper()
	for _, ch := range channels {
		if ch.Title == title {
			return ch
		}
	}
	t.Fatalf("no bound channel %q in %+v", title, channels)
	return gui.ChannelInfo{}
}

func TestChannelsListMarksTheActiveChannel(t *testing.T) {
	seedDriveAndChannel(t, map[string]string{"/notes.txt": "hello"}, "Archive")
	svc := openGUI(t)

	channels, err := svc.Channels.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Only bound channels are listed; the unbound Telegram channel is a
	// bind choice, not a drive.
	if len(channels) != 1 {
		t.Fatalf("List = %+v, want only the bound Drive", channels)
	}
	drive := channels[0]
	if drive.Title != "Drive" || drive.ChannelID == "" || !drive.Active {
		t.Fatalf("List[0] = %+v, want the active Drive with its Telegram ID", drive)
	}
	if drive.LocalRoot == "" {
		t.Fatalf("List[0] = %+v, want the bound local root", drive)
	}
}

func TestChannelsChoicesOfferBindableChannelsAndTheDefaultTitle(t *testing.T) {
	archiveID := seedDriveAndChannel(t, nil, "Archive")
	svc := openGUI(t)

	choices, err := svc.Channels.Choices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if choices.DefaultTitle == "" {
		t.Fatalf("Choices = %+v, want a default title for a created channel", choices)
	}
	byTitle := map[string]gui.ChannelChoice{}
	for _, ch := range choices.Channels {
		byTitle[ch.Title] = ch
	}
	archive, ok := byTitle["Archive"]
	if !ok || archive.Bound {
		t.Fatalf("Choices = %+v, want Archive as an unbound choice", choices)
	}
	if archive.ChannelID != strconv.FormatInt(archiveID, 10) {
		t.Fatalf("Archive choice = %+v, want channel id %d", archive, archiveID)
	}
	if drive, ok := byTitle["Drive"]; !ok || !drive.Bound {
		t.Fatalf("Choices = %+v, want Drive marked bound", choices)
	}
}

func TestChannelsBindExistingChannelSwitchesTheDriveView(t *testing.T) {
	archiveID := seedDriveAndChannel(t, map[string]string{"/notes.txt": "hello"}, "Archive")
	svc := openGUI(t)
	ctx := context.Background()

	res, err := svc.Channels.Bind(ctx, gui.BindRequest{ChannelID: strconv.FormatInt(archiveID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ChannelID != strconv.FormatInt(archiveID, 10) || res.Title != "Archive" || res.Created {
		t.Fatalf("Bind = %+v, want the existing Archive channel", res)
	}

	// Binding switches the active channel: the Drive view follows.
	entries, err := svc.Drive.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("List(/) on Archive = %+v, want an empty drive", entries)
	}
	channels, err := svc.Channels.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 {
		t.Fatalf("List = %+v, want Drive and Archive", channels)
	}
	if !boundChannel(t, channels, "Archive").Active || boundChannel(t, channels, "Drive").Active {
		t.Fatalf("List = %+v, want Archive active", channels)
	}

	// Switching back brings the seeded files back.
	drive := boundChannel(t, channels, "Drive")
	if _, err := svc.Channels.Select(ctx, drive.ChannelID); err != nil {
		t.Fatal(err)
	}
	entries, err = svc.Drive.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "notes.txt" {
		t.Fatalf("List(/) on Drive = %+v, want notes.txt", entries)
	}
}

func TestChannelsBindTwoChannelsSharingATitle(t *testing.T) {
	seedDrive(t, nil)
	// Telegram titles are not unique: two unbound channels share one.
	app, closeApp, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.TG.CreateChannel(context.Background(), "Archive")
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.TG.CreateChannel(context.Background(), "Archive")
	if err != nil {
		t.Fatal(err)
	}
	closeApp()
	svc := openGUI(t)
	ctx := context.Background()

	if _, err := svc.Channels.Bind(ctx, gui.BindRequest{ChannelID: strconv.FormatInt(first.ID, 10)}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Channels.Bind(ctx, gui.BindRequest{ChannelID: strconv.FormatInt(second.ID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ChannelID != strconv.FormatInt(second.ID, 10) || res.AlreadyInitialized {
		t.Fatalf("Bind of the second Archive = %+v, want a fresh binding of channel %d", res, second.ID)
	}
	channels, err := svc.Channels.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 3 {
		t.Fatalf("List = %+v, want Drive and both Archives", channels)
	}
}

func TestChannelsCreateChannelWithTheDefaultTitle(t *testing.T) {
	seedLogin(t)
	svc := openGUI(t)
	ctx := context.Background()

	choices, err := svc.Channels.Choices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Channels.Bind(ctx, gui.BindRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Title != choices.DefaultTitle || res.ChannelID == "" {
		t.Fatalf("Bind = %+v, want a created channel titled %q", res, choices.DefaultTitle)
	}
	if res.AlreadyInitialized {
		t.Fatalf("Bind = %+v, want a fresh binding", res)
	}
	channels, err := svc.Channels.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || !channels[0].Active || channels[0].Title != choices.DefaultTitle {
		t.Fatalf("List = %+v, want the new channel active", channels)
	}
	// The new drive is browsable.
	entries, err := svc.Drive.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("List(/) = %+v, want an empty new drive", entries)
	}
}

func TestChannelsCreateChannelWithAnExplicitTitle(t *testing.T) {
	seedLogin(t)
	svc := openGUI(t)

	res, err := svc.Channels.Bind(context.Background(), gui.BindRequest{Title: "Photos"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Title != "Photos" {
		t.Fatalf("Bind = %+v, want the created Photos channel", res)
	}
}

func TestChannelsSelectRejectsAnUnboundChannel(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)
	ctx := context.Background()

	_, err := svc.Channels.Select(ctx, "999999")
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Select error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CHANNEL_NOT_FOUND" {
		t.Fatalf("Select error = %+v, want ERR_CHANNEL_NOT_FOUND", guiErr)
	}
	// The rejected switch changed nothing: Drive is still the channel served.
	entries, lerr := svc.Drive.List(ctx, "/")
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(entries) != 1 || entries[0].Name != "notes.txt" {
		t.Fatalf("List(/) = %+v, want notes.txt still in place", entries)
	}
}

func TestChannelsSelectSurvivesAnAuthReopen(t *testing.T) {
	archiveID := seedDriveAndChannel(t, nil, "Archive")
	svc := openGUI(t)
	ctx := context.Background()
	archive := strconv.FormatInt(archiveID, 10)

	if _, err := svc.Channels.Bind(ctx, gui.BindRequest{ChannelID: archive}); err != nil {
		t.Fatal(err)
	}
	// Setup rewrites the Telegram config and reopens the App; the channel
	// selection must survive the reopen.
	if _, err := svc.Auth.Setup(ctx, "1", "hash", "+1000"); err != nil {
		t.Fatal(err)
	}
	channels, err := svc.Channels.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !boundChannel(t, channels, "Archive").Active {
		t.Fatalf("List after reopen = %+v, want Archive still active", channels)
	}
}

func TestChannelsStatusReportsDiscussionCapabilitiesAndFreshness(t *testing.T) {
	seedDrive(t, map[string]string{
		"/notes.txt":      "hello",
		"/photos/cat.jpg": "meow",
	})
	svc := openGUI(t)

	st, err := svc.Channels.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Title != "Drive" || st.ChannelID == "" {
		t.Fatalf("Status = %+v, want the Drive channel", st)
	}
	if st.Files != 2 {
		t.Fatalf("Status = %+v, want 2 indexed files", st)
	}
	if !st.DiscussionLinked || st.DiscussionTitle == "" {
		t.Fatalf("Status = %+v, want the linked discussion group", st)
	}
	if st.UploadLimitBytes != 2147483648 {
		t.Fatalf("Status = %+v, want the fake's 2 GB upload limit", st)
	}
	// init scanned the channel when it was bound.
	if st.LastScanAt == "" {
		t.Fatalf("Status = %+v, want a last-scan time", st)
	}
	if _, err := time.Parse(time.RFC3339, st.LastScanAt); err != nil {
		t.Fatalf("LastScanAt is not RFC3339: %q (%v)", st.LastScanAt, err)
	}
}

func TestChannelsStatusWithoutABoundChannel(t *testing.T) {
	seedLogin(t)
	svc := openGUI(t)

	_, err := svc.Channels.Status(context.Background())
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Status error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CHANNEL_NOT_FOUND" {
		t.Fatalf("Status error = %+v, want ERR_CHANNEL_NOT_FOUND", guiErr)
	}
}

func TestChannelsLinkDiscussionGroup(t *testing.T) {
	seedDrive(t, nil)

	// Unlink the discussion group init created, the state of a channel
	// bound before ADR 0018.
	app, closeApp, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	var rowID int64
	if err := app.DB.Raw().QueryRow(`select id from channels limit 1`).Scan(&rowID); err != nil {
		t.Fatal(err)
	}
	if err := app.DB.SetDiscussionGroup(context.Background(), rowID, "", "", ""); err != nil {
		t.Fatal(err)
	}

	svc := openGUI(t)
	ctx := context.Background()

	st, err := svc.Channels.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.DiscussionLinked {
		t.Fatalf("Status = %+v, want the discussion group unlinked", st)
	}

	link, err := svc.Channels.LinkDiscussion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if link.DiscussionTitle == "" || link.DiscussionChannelID == "" {
		t.Fatalf("LinkDiscussion = %+v, want the linked group's identity", link)
	}

	st, err = svc.Channels.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.DiscussionLinked || st.DiscussionTitle != link.DiscussionTitle {
		t.Fatalf("Status after link = %+v, want %q linked", st, link.DiscussionTitle)
	}
}

func TestChannelsStatusReportsTheChannelPermissions(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)

	st, err := svc.Channels.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := gui.ChannelCapabilities{CanUpload: true, CanDelete: true, CanEditCaptions: true, CanInvite: true}
	if st.Capabilities != want {
		t.Fatalf("Status capabilities = %+v, want every permission of the channel's owner %+v", st.Capabilities, want)
	}
}

func TestChannelsStatusReportsMissingPermissions(t *testing.T) {
	seedDrive(t, nil)
	// The account lost its admin rights on the channel: it can still read,
	// but none of the writes the drive needs.
	t.Setenv("TD_FAKE_DENY_CAPABILITIES", "upload,delete,edit,invite")
	svc := openGUI(t)

	st, err := svc.Channels.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Capabilities != (gui.ChannelCapabilities{}) {
		t.Fatalf("Status capabilities = %+v, want every permission denied", st.Capabilities)
	}
}

// channelsChanges returns the recorded channels-changed events in order.
func (r *eventRecorder) channelsChanges() []gui.ChannelsChanged {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []gui.ChannelsChanged
	for _, e := range r.events {
		if e.name == gui.EventChannelsChanged {
			out = append(out, e.data.(gui.ChannelsChanged))
		}
	}
	return out
}

// A td process binds another channel into the shared index; index sync
// reports the new channel list as a typed channels-changed event.
func TestChannelsSyncEmitsChannelsChangedWhenTheCLIBinds(t *testing.T) {
	archiveID := seedDriveAndChannel(t, nil, "Archive")
	svc := openGUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// The frontend's own List is the baseline sync diffs against.
	if _, err := svc.Channels.List(ctx); err != nil {
		t.Fatal(err)
	}
	rec := &eventRecorder{}
	svc.SetChannelsEmitter(rec.emit)
	svc.StartSync(ctx, 20*time.Millisecond)

	cli, closeCLI, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeCLI()
	if _, err := cli.InitRoot(ctx, t.TempDir(), "", "", strconv.FormatInt(archiveID, 10)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range rec.channelsChanges() {
			if len(ev.Channels) == 2 {
				archive := boundChannel(t, ev.Channels, "Archive")
				if archive.Active || !boundChannel(t, ev.Channels, "Drive").Active {
					t.Fatalf("channels-changed = %+v, want Archive listed and Drive still active", ev.Channels)
				}
				// Nothing changed since: no duplicate event follows.
				time.Sleep(200 * time.Millisecond)
				if n := len(rec.channelsChanges()); n != 1 {
					t.Fatalf("got %d channels-changed events, want exactly one: %+v", n, rec.channelsChanges())
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no channels-changed event listed the CLI's binding; events: %+v", rec.channelsChanges())
}
