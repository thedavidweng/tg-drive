package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/manifest"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// writeLocals writes n files named a.bin, b.bin, … into one temp dir and
// returns their paths in order.
func writeLocals(t *testing.T, n int) []string {
	t.Helper()
	dir := t.TempDir()
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, string(rune('a'+i))+".bin")
		if err := os.WriteFile(p, []byte("payload "+string(rune('a'+i))), 0o644); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// writeNamedLocal writes one file with a specific basename.
func writeNamedLocal(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("content of "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// albumInventoryComments collects every td-album:v1 record across the drive
// channel and its discussion thread, keyed by the first member's message id
// (ADR 0018: the inventory is a comment on the first member's post).
func albumInventoryComments(t *testing.T, app *App, ctx context.Context) map[int]telegram.Message {
	t.Helper()
	out := map[int]telegram.Message{}
	for _, m := range machineRecords(t, app, ctx) {
		if !manifest.IsAlbumReply(m.Text) {
			continue
		}
		meta, err := manifest.ParseAlbumReply(m.Text)
		if err != nil {
			t.Fatal(err)
		}
		first := meta.Files[0].MessageID
		for _, f := range meta.Files[1:] {
			if f.MessageID < first {
				first = f.MessageID
			}
		}
		out[first] = m
	}
	return out
}

// groupedMembers returns the media-group members of a channel.
func groupedMembers(msgs []telegram.Message) []telegram.Message {
	var out []telegram.Message
	for _, m := range msgs {
		if m.GroupedID != 0 {
			out = append(out, m)
		}
	}
	return out
}

// TestAlbumUploadNativeGroup asserts the observable shape of a three-file
// album: one native media group, the machine caption on the first member
// only, and exactly one td-album:v1 inventory reply covering every member.
func TestAlbumUploadNativeGroup(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	locals := writeLocals(t, 3)
	data, err := app.UploadFilesAs(ctx, locals, "/albums/", ConflictFail, false, Presentation{}, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tgChID, _ := app.tgChannelID(ctx)
	msgs := tg.Messages(tgChID)

	members := groupedMembers(msgs)
	if len(members) != 3 {
		t.Fatalf("grouped messages = %d, want 3 (%+v)", len(members), msgs)
	}
	gid := members[0].GroupedID
	if gid == 0 {
		t.Fatal("grouped id must not be zero")
	}
	for _, m := range members[1:] {
		if m.GroupedID != gid {
			t.Fatalf("grouped id mismatch: %d vs %d", m.GroupedID, gid)
		}
	}

	// ADR 0018: album captions are human-only; the inventory is a comment.
	if strings.Contains(members[0].Caption, "td:v1") {
		t.Fatalf("first member caption carries machine meta: %q", members[0].Caption)
	}
	for _, m := range members[1:] {
		if m.Caption != "" {
			t.Fatalf("sibling caption not empty: %q", m.Caption)
		}
	}

	inventories := albumInventoryComments(t, app, ctx)
	if len(inventories) != 1 {
		t.Fatalf("album inventory comments = %d, want 1", len(inventories))
	}
	firstReply, ok := inventories[members[0].ID]
	if !ok {
		t.Fatalf("inventory comment does not cover the first member: %+v", inventories)
	}
	meta, err := manifest.ParseAlbumReply(firstReply.Text)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GroupedID != gid || len(meta.Files) != 3 {
		t.Fatalf("parsed inventory = %+v", meta)
	}
	gotPaths := map[string]bool{}
	for _, f := range meta.Files {
		gotPaths[f.CanonicalPath] = true
	}
	for _, p := range []string{"/albums/a.bin", "/albums/b.bin", "/albums/c.bin"} {
		if !gotPaths[p] {
			t.Fatalf("inventory missing %s: %+v", p, meta.Files)
		}
	}

	// Index rows active, bound to the shared inventory reply, hashes intact.
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{
		"/albums/a.bin": []byte("payload a"),
		"/albums/b.bin": []byte("payload b"),
		"/albums/c.bin": []byte("payload c"),
	} {
		var status string
		var manID int
		var size int64
		var hash string
		if err := app.DB.Raw().QueryRowContext(ctx,
			`select status, coalesce(manifest_message_id,0), coalesce(size,0), coalesce(content_hash,'') from files where channel_id=? and canonical_path=?`,
			channelID, path).Scan(&status, &manID, &size, &hash); err != nil {
			t.Fatalf("%s missing from index: %v", path, err)
		}
		if status != "active" || manID != firstReply.ID {
			t.Fatalf("%s indexed as status=%q manifest=%d, want active/%d", path, status, manID, firstReply.ID)
		}
		if size != int64(len(want)) || hash != blake3Hex(want) {
			t.Fatalf("%s indexed as size=%d hash=%q, want %d %s", path, size, hash, len(want), blake3Hex(want))
		}
	}
	if data.Uploaded != 3 || data.Skipped != 0 {
		t.Fatalf("result counters = uploaded %v skipped %v", data.Uploaded, data.Skipped)
	}
	groups := data.Albums
	if len(groups) != 1 || groups[0].GroupedID != gid || groups[0].ReplyMessageID != firstReply.ID || len(groups[0].Paths) != 3 {
		t.Fatalf("envelope albums = %+v", groups)
	}

	// Browsing accepts the trailing-slash form the upload destination used.
	withSlash, err := app.ListDir(ctx, "/albums/")
	if err != nil {
		t.Fatalf("ls with trailing slash: %v", err)
	}
	bare, err := app.ListDir(ctx, "/albums")
	if err != nil {
		t.Fatal(err)
	}
	if len(withSlash) != len(bare) {
		t.Fatalf("ls /albums/ = %d entries, ls /albums = %d", len(withSlash), len(bare))
	}
}

// TestAlbumUploadSplitsLargeSets covers >10 files: consecutive groups of ten,
// one inventory per group, caption only on the overall first member, and a
// full scan rebuild purely from Telegram.
func TestAlbumUploadSplitsLargeSets(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)

	dir := t.TempDir()
	var locals []string
	wantContent := map[string]string{}
	for i := 0; i < 13; i++ {
		name := "f" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + ".bin"
		content := strings.Repeat("x", i+1)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		locals = append(locals, p)
		wantContent["/big/"+name] = content
	}
	data, err := app.UploadFilesAs(ctx, locals, "/big/", ConflictFail, false, Presentation{}, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	groups := data.Albums
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2 (10+3): %+v", len(groups), data)
	}
	if len(groups[0].Paths) != 10 || len(groups[1].Paths) != 3 {
		t.Fatalf("group sizes = %d/%d, want 10/3", len(groups[0].Paths), len(groups[1].Paths))
	}
	if groups[0].GroupedID == groups[1].GroupedID {
		t.Fatal("split groups must carry distinct grouped ids")
	}
	if inventories := albumInventoryComments(t, app, ctx); len(inventories) != 2 {
		t.Fatalf("album inventory comments = %d, want 2", len(inventories))
	}

	// ADR 0018: exactly one member carries the human caption; captions hold
	// no machine text.
	captioned := 0
	for _, m := range groupedMembers(tg.Messages(tgChID)) {
		if m.Caption != "" {
			captioned++
		}
		if strings.Contains(m.Caption, "td:v1") {
			t.Fatalf("member caption carries machine meta: %q", m.Caption)
		}
	}
	if captioned != 1 {
		t.Fatalf("captioned members = %d, want 1", captioned)
	}

	// Wipe the index and rebuild purely from the channel.
	for _, table := range []string{"path_tags", "path_segment_slugs", "files", "nodes", "scan_state"} {
		if _, err := app.DB.Raw().Exec(`delete from ` + table); err != nil {
			t.Fatal(err)
		}
	}
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Active != 13 {
		t.Fatalf("scan active = %v, want 13", res.Active)
	}
	dest := filepath.Join(t.TempDir(), "out.bin")
	dl, err := app.DownloadFile(ctx, "/big/f12.bin", dest, ConflictFail, DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(wantContent["/big/f12.bin"])) || dl.Size != 13 {
		t.Fatalf("rebuilt f12.bin mismatch (%d bytes)", len(got))
	}
}

// TestAlbumUploadConflictPolicies pins per-file conflict handling: fail
// aborts pre-flight with nothing sent, skip drops the occupant's file,
// rename publishes under a fresh name, replace is refused outright.
func TestAlbumUploadConflictPolicies(t *testing.T) {
	ctx := context.Background()

	// Pre-flight failure: nothing reaches Telegram, nothing lingers.
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	if _, err := app.UploadFile(ctx, writeLocal(t, "existing"), "/dest/taken.bin", ConflictReplace, false, UploadOptions{ConfirmReplace: true}); err != nil {
		t.Fatal(err)
	}
	before := len(tg.Messages(mustChannel(t, app)))
	_, conflictErr := app.UploadFilesAs(ctx,
		[]string{writeLocal(t, "three"), writeNamedLocal(t, "taken.bin")},
		"/dest/", ConflictFail, false, Presentation{}, UploadOptions{})
	if code := appErrCode(t, conflictErr); code != apperr.ErrPathExists {
		t.Fatalf("fail policy code = %s, want ERR_PATH_EXISTS", code)
	}
	if got := len(tg.Messages(mustChannel(t, app))); got != before {
		t.Fatalf("pre-flight failure sent messages: %d -> %d", before, got)
	}
	if got := fileStatus(t, app, "/dest/three.bin"); got != "" {
		t.Fatalf("pending row survived pre-flight failure: %q", got)
	}

	// Skip: the taken name is skipped, the rest uploads.
	appS, tgS := testApp(t)
	loginAndInit(t, appS, tgS)
	if _, err := appS.UploadFile(ctx, writeLocal(t, "existing"), "/dest/taken.bin", ConflictReplace, false, UploadOptions{ConfirmReplace: true}); err != nil {
		t.Fatal(err)
	}
	skipData, err := appS.UploadFilesAs(ctx,
		[]string{writeNamedLocal(t, "taken.bin"), writeNamedLocal(t, "fresh.bin")},
		"/dest/", ConflictSkip, false, Presentation{}, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if skipData.Skipped != 1 || skipData.Uploaded != 1 {
		t.Fatalf("skip result = uploaded %v skipped %v, want 1/1", skipData.Uploaded, skipData.Skipped)
	}

	// Rename: collision lands under a candidate name.
	appR, tgR := testApp(t)
	loginAndInit(t, appR, tgR)
	if _, err := appR.UploadFile(ctx, writeLocal(t, "existing"), "/dest/dup.bin", ConflictReplace, false, UploadOptions{ConfirmReplace: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := appR.UploadFilesAs(ctx,
		[]string{writeNamedLocal(t, "dup.bin"), writeNamedLocal(t, "other.bin")},
		"/dest/", ConflictRename, false, Presentation{}, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := fileStatus(t, appR, "/dest/dup (1).bin"); got != "active" {
		t.Fatalf("auto-renamed member status = %q, want active", got)
	}

	// Replace is refused without touching Telegram.
	appW, tgW := testApp(t)
	loginAndInit(t, appW, tgW)
	_, repErr := appW.UploadFilesAs(ctx, []string{writeLocal(t, "a"), writeLocal(t, "b")}, "/r/", ConflictReplace, false, Presentation{}, UploadOptions{ConfirmReplace: true})
	if code := appErrCode(t, repErr); code != apperr.ErrUsage {
		t.Fatalf("replace code = %s, want ERR_USAGE", code)
	}
	if got := len(tgW.Messages(mustChannel(t, appW))); got != 0 {
		t.Fatalf("refused upload sent %d messages", got)
	}

	// Destination must be directory-shaped.
	appD, tgD := testApp(t)
	loginAndInit(t, appD, tgD)
	_, dirErr := appD.UploadFilesAs(ctx, []string{writeLocal(t, "a"), writeLocal(t, "b")}, "/not-a-dir", ConflictFail, false, Presentation{}, UploadOptions{})
	if code := appErrCode(t, dirErr); code != apperr.ErrUsage {
		t.Fatalf("destination code = %s, want ERR_USAGE", code)
	}
}

// TestAlbumUploadPendingAdoptionRetry covers an interrupted resumable member:
// the failed batch leaves an adoptable pending row, and the retry adopts it
// and completes the whole album.
func TestAlbumUploadPendingAdoptionRetry(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	big := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(big, bytes.Repeat([]byte{0x5A}, 11*1024*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	tg.SetPartSize(1024 * 1024)
	tg.SetFailUploadAfterParts(2)

	small := writeNamedLocal(t, "small.bin")
	if _, err := app.UploadFilesAs(ctx, []string{big, small}, "/media/", ConflictFail, false, Presentation{}, UploadOptions{}); err == nil {
		t.Fatal("expected interrupted batch to fail")
	}
	if got := fileStatus(t, app, "/media/big.bin"); got != "pending" {
		t.Fatalf("big member status after interruption = %q, want pending", got)
	}

	tg.SetFailUploadAfterParts(0)
	tg.ResetPartSubmissions()
	data, err := app.UploadFilesAs(ctx, []string{big, small}, "/media/", ConflictFail, false, Presentation{}, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !data.Resumed {
		t.Fatalf("retry data = %v, want resumed:true", data)
	}
	if fileStatus(t, app, "/media/big.bin") != "active" || fileStatus(t, app, "/media/small.bin") != "active" {
		t.Fatal("retry did not activate both members")
	}
	if inventories := albumInventoryComments(t, app, ctx); len(inventories) != 1 {
		t.Fatalf("album inventory comments after retry = %d, want 1", len(inventories))
	}
}

// TestAlbumUploadPublishFailureAbandonsGroup pins the crash-window rollback:
// when the inventory reply cannot be posted, the whole group is deleted from
// Telegram and its pending rows cleaned up; a retry then succeeds cleanly.
func TestAlbumUploadPublishFailureAbandonsGroup(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	locals := writeLocals(t, 3)
	tg.SetFailReply(true)
	if _, err := app.UploadFilesAs(ctx, locals, "/gal/", ConflictFail, false, Presentation{}, UploadOptions{}); err == nil {
		t.Fatal("expected publish failure to surface")
	}
	if got := len(tg.Messages(mustChannel(t, app))); got != 0 {
		t.Fatalf("abandoned group left %d messages on Telegram", got)
	}
	for _, p := range []string{"/gal/a.bin", "/gal/b.bin", "/gal/c.bin"} {
		if got := fileStatus(t, app, p); got != "" {
			t.Fatalf("%s row survived abandonment: %q", p, got)
		}
	}

	tg.SetFailReply(false)
	data, err := app.UploadFilesAs(ctx, locals, "/gal/", ConflictFail, false, Presentation{}, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if data.Uploaded != 3 {
		t.Fatalf("retry uploaded = %v, want 3", data.Uploaded)
	}
	if fileStatus(t, app, "/gal/b.bin") != "active" {
		t.Fatal("retry member not active")
	}
}

// TestSingleMemberBatchUploadsAlone proves the platform-constraint fallback:
// when skips leave one survivor, it goes out as a normal single message with
// its own caption — never as a one-member group.
func TestSingleMemberBatchUploadsAlone(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	if _, err := app.UploadFile(ctx, writeLocal(t, "occupied"), "/solo/kept.bin", ConflictReplace, false, UploadOptions{ConfirmReplace: true}); err != nil {
		t.Fatal(err)
	}
	data, err := app.UploadFilesAs(ctx,
		[]string{writeNamedLocal(t, "kept.bin"), writeNamedLocal(t, "new.bin")},
		"/solo/", ConflictSkip, false, Presentation{}, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if data.Uploaded != 1 {
		t.Fatalf("uploaded = %v, want 1", data.Uploaded)
	}
	if albums := data.Albums; len(albums) != 0 {
		t.Fatalf("single survivor must not form an album: %+v", albums)
	}
	for _, m := range tg.Messages(mustChannel(t, app)) {
		if m.FileName == "new.bin" {
			if m.GroupedID != 0 {
				t.Fatal("lone upload must not be grouped")
			}
			if strings.Contains(m.Caption, "td:v1") {
				t.Fatalf("lone upload caption carries machine meta: %q", m.Caption)
			}
			return
		}
	}
	t.Fatal("lone upload message not found")
}

// TestRecursiveFolderUploadGroupsByDirectory pins the folder semantics: each
// source directory's children form one album; nested dirs recurse and their
// lone files stay ordinary uploads.
func TestRecursiveFolderUploadGroupsByDirectory(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(root, "top1.txt"):        "t1",
		filepath.Join(root, "top2.txt"):        "t2",
		filepath.Join(root, "sub", "deep.txt"): "d",
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := app.UploadRecursive(ctx, root, "/tree/", ConflictFail, false, false, false, UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if data.Uploaded != 3 {
		t.Fatalf("recursive uploaded = %v, want 3", data.Uploaded)
	}
	albums := data.Albums
	if len(albums) != 1 || len(albums[0].Paths) != 2 {
		t.Fatalf("albums = %+v, want one two-member group", albums)
	}
	inventories := albumInventoryComments(t, app, ctx)
	if len(inventories) != 1 {
		t.Fatalf("album inventory comments = %d, want 1", len(inventories))
	}
	var replyText string
	for _, m := range inventories {
		replyText = m.Text
	}
	meta, err := manifest.ParseAlbumReply(replyText)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Files) != 2 {
		t.Fatalf("album members = %d, want 2", len(meta.Files))
	}
	for _, p := range []string{"/tree/top1.txt", "/tree/top2.txt", "/tree/sub/deep.txt"} {
		if fileStatus(t, app, p) != "active" {
			t.Fatalf("%s not active after recursive album upload", p)
		}
	}
}

// mustChannel returns the fake channel id of the app's configured channel.
func mustChannel(t *testing.T, app *App) int64 {
	t.Helper()
	ch, err := app.tgChannelID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// TestReplaceAlbumMemberKeepsInventory replaces one member of a three-file
// album and checks the group's shared td-album:v1 inventory survives: the
// siblings stay listed, the old member is dropped, and a full scan from an
// empty index rebuilds the siblings plus the replacement. The failing-edit
// case covers an inventory rewrite that cannot reach Telegram.
func TestReplaceAlbumMemberKeepsInventory(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		failEdit bool
	}{
		{name: "delete", mode: "delete"},
		{name: "tombstone", mode: "tombstone"},
		{name: "tombstone-edit-fails", mode: "tombstone", failEdit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			app.Cfg.Delete.Mode = tc.mode
			ctx := context.Background()
			tgChID, _ := app.tgChannelID(ctx)

			if _, err := app.UploadFilesAs(ctx, writeLocals(t, 3), "/albums/", ConflictFail, false, Presentation{}, UploadOptions{}); err != nil {
				t.Fatal(err)
			}
			var oldMsgID int
			if err := app.DB.Raw().QueryRow(`select message_id from files where canonical_path='/albums/b.bin' and status='active'`).Scan(&oldMsgID); err != nil {
				t.Fatal(err)
			}

			tg.SetFailEditText(tc.failEdit)
			replacement := writeLocal(t, "replacement b")
			data, err := app.UploadFile(ctx, replacement, "/albums/b.bin", ConflictReplace, false, UploadOptions{ConfirmReplace: true})
			if err != nil {
				t.Fatalf("replace album member: %v", err)
			}
			tg.SetFailEditText(false)
			newMsgID := data.MessageID
			if newMsgID == 0 || newMsgID == oldMsgID {
				t.Fatalf("replace message id = %v, old %d", newMsgID, oldMsgID)
			}

			want := map[string]string{
				"/albums/a.bin": "payload a",
				"/albums/b.bin": "replacement b",
				"/albums/c.bin": "payload c",
			}
			assertContents := func(stage string) {
				t.Helper()
				for p, content := range want {
					if got := fileStatus(t, app, p); got != "active" {
						t.Fatalf("%s: %s status = %q, want active", stage, p, got)
					}
					dest := filepath.Join(t.TempDir(), "out.bin")
					if _, err := app.DownloadFile(ctx, p, dest, ConflictFail, DownloadOptions{}); err != nil {
						t.Fatalf("%s: download %s: %v", stage, p, err)
					}
					got, err := os.ReadFile(dest)
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != content {
						t.Fatalf("%s: %s content = %q, want %q", stage, p, got, content)
					}
				}
			}
			assertContents("after replace")

			if !tc.failEdit {
				inventories := albumInventoryComments(t, app, ctx)
				if len(inventories) != 1 {
					t.Fatalf("album inventories = %d, want 1 surviving the replace", len(inventories))
				}
				for _, inv := range inventories {
					meta, err := manifest.ParseAlbumReply(inv.Text)
					if err != nil {
						t.Fatal(err)
					}
					paths := map[string]bool{}
					for _, f := range meta.Files {
						if f.MessageID == oldMsgID {
							t.Fatalf("inventory still lists the replaced member %d: %+v", oldMsgID, meta.Files)
						}
						paths[f.CanonicalPath] = true
					}
					if len(meta.Files) != 2 || !paths["/albums/a.bin"] || !paths["/albums/c.bin"] {
						t.Fatalf("inventory = %+v, want the two siblings", meta.Files)
					}
				}
			}

			// The replaced member must not stay claimable on Telegram.
			if old, err := tg.GetMessage(ctx, tgChID, oldMsgID); err == nil && !strings.Contains(old.Caption, "deleted=true") {
				t.Fatalf("replaced member %d still live: caption %q", oldMsgID, old.Caption)
			}

			for _, table := range []string{"path_tags", "path_segment_slugs", "files", "nodes", "scan_state"} {
				if _, err := app.DB.Raw().Exec(`delete from ` + table); err != nil {
					t.Fatal(err)
				}
			}
			res, err := app.Scan(ctx, ScanOptions{Full: true})
			if err != nil {
				t.Fatalf("full scan after album member replace: %v", err)
			}
			if res.Active != len(want) {
				t.Fatalf("scan active = %v, want %d (%+v)", res.Active, len(want), res)
			}
			assertContents("after rebuild")
		})
	}
}

// TestMoveAlbumMemberKeepsCommentCarrier moves one member of an album whose
// inventory is a discussion comment, then removes it. The moved row must keep
// pointing at the comment carrier; otherwise the follow-up rm resolves the
// inventory id against the channel and rewrites the wrong record.
func TestMoveAlbumMemberKeepsCommentCarrier(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	if _, err := app.UploadFilesAs(ctx, writeLocals(t, 3), "/albums/", ConflictFail, false, Presentation{}, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	chatOf := func(p string) string {
		t.Helper()
		var chat string
		if err := app.DB.Raw().QueryRow(`select coalesce(manifest_chat_tg_id,'') from files where canonical_path=? and status='active'`, p).Scan(&chat); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return chat
	}
	before := chatOf("/albums/b.bin")
	if before == "" {
		t.Fatal("album inventory is not a discussion comment")
	}

	if err := app.MoveFile(ctx, "/albums/b.bin", "/moved/b.bin", MoveOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if got := chatOf("/moved/b.bin"); got != before {
		t.Fatalf("moved member manifest chat = %q, want %q", got, before)
	}

	if _, err := app.DeleteFile(ctx, "/moved/b.bin", DeleteOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	var inventories []manifest.AlbumMeta
	for _, m := range machineRecords(t, app, ctx) {
		if strings.Contains(m.Text, "td-album:v1") {
			meta, err := manifest.ParseAlbumReply(m.Text)
			if err != nil {
				t.Fatal(err)
			}
			inventories = append(inventories, meta)
		}
	}
	if len(inventories) != 1 || len(inventories[0].Files) != 2 {
		t.Fatalf("inventories after mv+rm = %+v, want one listing two members", inventories)
	}
	for _, f := range inventories[0].Files {
		if f.CanonicalPath != "/albums/a.bin" && f.CanonicalPath != "/albums/c.bin" {
			t.Fatalf("inventory lists %q after removing the moved member", f.CanonicalPath)
		}
	}
	for _, p := range []string{"/albums/a.bin", "/albums/c.bin"} {
		if fileStatus(t, app, p) != "active" {
			t.Fatalf("%s not active after removing a moved sibling", p)
		}
	}
}
