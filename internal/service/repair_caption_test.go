package service

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive/core/pathcodec"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

func oldRenderedCaption(t *testing.T, app *App, channelID int64, path, display, prefix string) string {
	t.Helper()
	tags, _, err := pathcodec.GenerateChain(path, app.loadSlugMap(context.Background(), channelID))
	if err != nil {
		t.Fatal(err)
	}
	parts := []string{display, strings.TrimPrefix(path[:len(path)-len(display)], "/")}
	if parts[1] != "" {
		parts[1] = strings.TrimSuffix(parts[1], "/") + "/"
	}
	parts = append(parts, "", strings.Join(tags, "\n"))
	scaffold := strings.Join(parts, "\n")
	if prefix == "" {
		return scaffold
	}
	return prefix + "\n\n" + scaffold
}

func TestRepairCaptionsRemovesModernScaffold(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "caption body")
	result, err := app.UploadFile(ctx, local, "/stash-browse/832/clip.mp4", ConflictFail, false, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tgChannelID, _ := app.tgChannelID(ctx)
	messageID := result.MessageID
	if err := tg.EditCaption(ctx, tgChannelID, messageID,
		oldRenderedCaption(t, app, channelID, "/stash-browse/832/clip.mp4", "clip.mp4", "source title")); err != nil {
		t.Fatal(err)
	}

	dry, err := app.RepairCaptions(ctx, "/stash-browse/832", true, false, Observer{})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Planned != 1 || dry.Cleaned != 0 {
		t.Fatalf("dry run = %+v", dry)
	}
	before, err := tg.GetMessage(ctx, tgChannelID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before.Caption, "#td_") {
		t.Fatalf("dry run changed caption: %q", before.Caption)
	}

	res, err := app.RepairCaptions(ctx, "/stash-browse/832", false, false, Observer{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cleaned != 1 || res.Failed != 0 {
		t.Fatalf("cleanup = %+v", res)
	}
	after, err := tg.GetMessage(ctx, tgChannelID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Caption != "source title" {
		t.Fatalf("cleaned caption = %q", after.Caption)
	}
	foundManifest := false
	for _, message := range machineRecords(t, app, ctx) {
		if strings.Contains(message.Text, "td-manifest:v1") && message.ReplyTo != nil {
			foundManifest = true
			break
		}
	}
	if !foundManifest {
		t.Fatal("caption cleanup removed or failed to preserve discussion manifest")
	}

	idempotent, err := app.RepairCaptions(ctx, "/stash-browse/832", false, false, Observer{})
	if err != nil {
		t.Fatal(err)
	}
	if idempotent.Cleaned != 0 || idempotent.Skipped != 1 {
		t.Fatalf("idempotent cleanup = %+v", idempotent)
	}
}

func TestRepairCaptionsCleansOnlyCaptionedAlbumMember(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	locals := writeLocals(t, 3)
	if _, err := app.UploadFilesAs(ctx, locals, "/albums/", ConflictFail, false, Presentation{}, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tgChannelID, _ := app.tgChannelID(ctx)
	members := groupedMembers(tg.Messages(tgChannelID))
	if len(members) != 3 {
		t.Fatalf("members = %d", len(members))
	}
	if err := tg.EditCaption(ctx, tgChannelID, members[0].ID,
		oldRenderedCaption(t, app, channelID, "/albums/a.bin", "a.bin", "album title")); err != nil {
		t.Fatal(err)
	}

	res, err := app.RepairCaptions(ctx, "/albums", false, false, Observer{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cleaned != 1 || res.Failed != 0 {
		t.Fatalf("cleanup = %+v", res)
	}
	for _, member := range members {
		got, err := tg.GetMessage(ctx, tgChannelID, member.ID)
		if err != nil {
			t.Fatal(err)
		}
		if member.ID == members[0].ID && got.Caption != "album title" {
			t.Fatalf("first caption = %q", got.Caption)
		}
		if member.ID != members[0].ID && got.Caption != "" {
			t.Fatalf("sibling %d caption = %q", member.ID, got.Caption)
		}
	}
}

func TestRepairCaptionsSkipsUneditableMessage(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "caption body")
	result, err := app.UploadFile(ctx, local, "/old/clip.mp4", ConflictFail, false, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tgChannelID, _ := app.tgChannelID(ctx)
	messageID := result.MessageID
	if err := tg.EditCaption(ctx, tgChannelID, messageID,
		oldRenderedCaption(t, app, channelID, "/old/clip.mp4", "clip.mp4", "")); err != nil {
		t.Fatal(err)
	}
	tg.SetNotEditable(tgChannelID, messageID, true)

	res, err := app.RepairCaptions(ctx, "/old", false, false, Observer{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Cleaned != 0 || res.Failed != 0 {
		t.Fatalf("cleanup = %+v", res)
	}
}

// slowCaptionEdit holds every EditCaption until release closes, after
// signalling entered, so a test can act while a caption edit is in flight.
type slowCaptionEdit struct {
	telegram.Client
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *slowCaptionEdit) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Client.EditCaption(ctx, channelID, messageID, caption)
}

// A caption edit in flight holds no SQLite write lock: another process's
// writer commits while Telegram is still answering, instead of queueing on
// busy_timeout and failing with SQLITE_BUSY.
func TestRepairCaptionsEditDoesNotBlockOtherWriters(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	result, err := app.UploadFile(ctx, writeLocal(t, "caption body"), "/old/clip.mp4", ConflictFail, false, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tgChannelID, _ := app.tgChannelID(ctx)
	if err := tg.EditCaption(ctx, tgChannelID, result.MessageID,
		oldRenderedCaption(t, app, channelID, "/old/clip.mp4", "clip.mp4", "")); err != nil {
		t.Fatal(err)
	}

	// A second handle on the database stands in for another process.
	other, err := sqlitestore.Open(app.Cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	slow := &slowCaptionEdit{Client: app.TG, entered: make(chan struct{}), release: make(chan struct{})}
	app.TG = slow
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(slow.release) }) }
	t.Cleanup(release)

	type repairOutcome struct {
		res *RepairCaptionsResult
		err error
	}
	repaired := make(chan repairOutcome, 1)
	go func() {
		res, err := app.RepairCaptions(ctx, "/old", false, false, Observer{})
		repaired <- repairOutcome{res, err}
	}()
	select {
	case <-slow.entered:
	case out := <-repaired:
		t.Fatalf("repair finished before editing a caption: %+v, %v", out.res, out.err)
	case <-time.After(10 * time.Second):
		t.Fatal("caption edit never started")
	}

	wrote := make(chan error, 1)
	go func() {
		wrote <- other.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `update channels set updated_at=? where id=?`,
				time.Now().UTC().Format(time.RFC3339), channelID)
			return err
		})
	}()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatalf("writer during caption edit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer blocked while a caption edit was in flight")
	}

	release()
	out := <-repaired
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.res.Cleaned != 1 || out.res.Failed != 0 {
		t.Fatalf("cleanup = %+v", out.res)
	}
	msg, err := tg.GetMessage(ctx, tgChannelID, result.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Caption != "clip.mp4" {
		t.Fatalf("caption = %q", msg.Caption)
	}
}
