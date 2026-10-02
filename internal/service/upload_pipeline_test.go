package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// One upload pipeline serves single files, albums, recursive folders, and
// imports (ADR 0027). These tests pin the policies that used to differ
// between the single-file and album copies of the pipeline, driven through
// the public use cases against the fake.

// rowsAt counts files rows of any status at the given canonical paths.
func rowsAt(t *testing.T, app *App, paths ...string) int {
	t.Helper()
	n := 0
	for _, p := range paths {
		var c int
		if err := app.DB.Raw().QueryRow(`select count(*) from files where canonical_path=?`, p).Scan(&c); err != nil {
			t.Fatal(err)
		}
		n += c
	}
	return n
}

// TestAlbumPendingRowsStagedUnderLock covers the lock scope of a batch:
// pending rows are staged only once every destination lock is held. Failure
// modes: (1) rows staged before a lock conflict linger as pending, so a later
// td repair --pending re-uploads them as loose single files; (2) a lingering
// row blocks or is silently adopted by the next batch; (3) anything reaches
// Telegram despite the conflict. Both batch entry points (multi-file cp and
// recursive cp) are covered.
func TestAlbumPendingRowsStagedUnderLock(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(ctx context.Context, app *App, locals []string) error
	}{
		{name: "files", run: func(ctx context.Context, app *App, locals []string) error {
			_, err := app.UploadFilesAs(ctx, locals, "/locked/", ConflictFail, false, Presentation{})
			return err
		}},
		{name: "recursive", run: func(ctx context.Context, app *App, locals []string) error {
			_, err := app.UploadRecursive(ctx, filepath.Dir(locals[0]), "/locked/", ConflictFail, false, false, false)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			ctx := context.Background()
			channelID, _, err := app.channelID(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := app.DB.AcquireLock(ctx, sqlitestore.LockKey(channelID, "/locked/b.bin"), "other-op", time.Minute); err != nil {
				t.Fatal(err)
			}

			locals := writeLocals(t, 3)
			err = tc.run(ctx, app, locals)
			if code := appErrCode(t, err); code != apperr.ErrOperationLocked {
				t.Fatalf("code = %s, want ERR_OPERATION_LOCKED (%v)", code, err)
			}
			if n := rowsAt(t, app, "/locked/a.bin", "/locked/b.bin", "/locked/c.bin"); n != 0 {
				t.Fatalf("lock conflict left %d staged rows", n)
			}
			if n := len(tg.Messages(mustChannel(t, app))); n != 0 {
				t.Fatalf("lock conflict sent %d messages", n)
			}

			// Once the other operation is gone the batch publishes cleanly.
			if err := app.DB.ReleaseLock(ctx, sqlitestore.LockKey(channelID, "/locked/b.bin"), "other-op"); err != nil {
				t.Fatal(err)
			}
			if err := tc.run(ctx, app, locals); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{"/locked/a.bin", "/locked/b.bin", "/locked/c.bin"} {
				if got := fileStatus(t, app, p); got != "active" {
					t.Fatalf("%s = %q after retry, want active", p, got)
				}
			}
		})
	}
}

// TestAlbumCrashWindowReportsOrphansExactly pins the one crash-window policy
// on media groups: after the group reached Telegram, any failure abandons the
// whole group, and the error says ERR_ORPHANED_UPLOAD exactly when something
// could not be rolled back. Failure modes: (1) a clean rollback still reports
// orphans, sending users to a repair with nothing to do; (2) an unrollable
// group reports the underlying error, hiding the orphans repair must handle;
// (3) the rollback leaves media, inventory, or rows behind.
func TestAlbumCrashWindowReportsOrphansExactly(t *testing.T) {
	paths := []string{"/gal/a.bin", "/gal/b.bin", "/gal/c.bin"}

	t.Run("index-failure-clean-rollback", func(t *testing.T) {
		app, tg := testApp(t)
		loginAndInit(t, app, tg)
		ctx := context.Background()
		app.Index = failIndex{err: errors.New("index boom")}
		_, err := app.UploadFilesAs(ctx, writeLocals(t, 3), "/gal/", ConflictFail, false, Presentation{})
		if code := appErrCode(t, err); code != apperr.ErrDB {
			t.Fatalf("code = %s, want ERR_DB after a clean rollback (%v)", code, err)
		}
		if n := len(tg.Messages(mustChannel(t, app))); n != 0 {
			t.Fatalf("rolled-back group left %d messages", n)
		}
		if n := len(albumInventoryComments(t, app, ctx)); n != 0 {
			t.Fatalf("rolled-back group left %d inventories", n)
		}
		if n := rowsAt(t, app, paths...); n != 0 {
			t.Fatalf("rolled-back group left %d rows", n)
		}
	})

	t.Run("inventory-failure-delete-fails", func(t *testing.T) {
		app, tg := testApp(t)
		loginAndInit(t, app, tg)
		ctx := context.Background()
		tg.SetFailReply(true)
		tg.SetFailDelete(true)
		_, err := app.UploadFilesAs(ctx, writeLocals(t, 3), "/gal/", ConflictFail, false, Presentation{})
		if code := appErrCode(t, err); code != apperr.ErrOrphanedUpload {
			t.Fatalf("code = %s, want ERR_ORPHANED_UPLOAD (%v)", code, err)
		}
		for _, p := range paths {
			if got := fileStatus(t, app, p); got != "orphaned" {
				t.Fatalf("%s = %q, want orphaned", p, got)
			}
		}
		tg.SetFailReply(false)
		tg.SetFailDelete(false)
		res, err := app.RepairOrphaned(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
		if res.Deleted != len(paths) {
			t.Fatalf("repair deleted %d orphans, want %d", res.Deleted, len(paths))
		}
		if n := len(tg.Messages(mustChannel(t, app))); n != 0 {
			t.Fatalf("orphans left %d messages after repair", n)
		}
	})
}

// TestAlbumIndexingIsAllOrNothing pins the index commit of a media group:
// every member row is indexed in one transaction, so a database failure on
// the last member leaves no member indexed. The failure is injected inside
// the real index (a trigger aborting the last member's row write), because
// a failing port double cannot fail halfway through a transaction. Failure
// modes: (1) earlier members are committed active before the failure, so
// their derived directory survives the rollback as a phantom directory;
// (2) when the media cannot be deleted, the orphaned members still carry the
// path tags of an index entry that never completed.
func TestAlbumIndexingIsAllOrNothing(t *testing.T) {
	paths := []string{"/gal/a.bin", "/gal/b.bin", "/gal/c.bin"}
	failLastMember := func(t *testing.T, app *App) {
		t.Helper()
		if _, err := app.DB.Raw().Exec(`create trigger fail_last_member before update of status on files
			when new.status='active' and new.canonical_path='/gal/c.bin'
			begin select raise(abort, 'index boom'); end`); err != nil {
			t.Fatal(err)
		}
	}
	countWhere := func(t *testing.T, app *App, query string) int {
		t.Helper()
		var n int
		if err := app.DB.Raw().QueryRow(query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("clean-rollback", func(t *testing.T) {
		app, tg := testApp(t)
		loginAndInit(t, app, tg)
		ctx := context.Background()
		failLastMember(t, app)
		_, err := app.UploadFilesAs(ctx, writeLocals(t, 3), "/gal/", ConflictFail, false, Presentation{})
		if code := appErrCode(t, err); code != apperr.ErrDB {
			t.Fatalf("code = %s, want ERR_DB (%v)", code, err)
		}
		if n := rowsAt(t, app, paths...); n != 0 {
			t.Fatalf("rolled-back group left %d rows", n)
		}
		if n := countWhere(t, app, `select count(*) from nodes where canonical_path='/gal'`); n != 0 {
			t.Fatal("rolled-back group left a derived /gal directory")
		}
		if n := len(tg.Messages(mustChannel(t, app))); n != 0 {
			t.Fatalf("rolled-back group left %d messages", n)
		}
		if n := len(albumInventoryComments(t, app, ctx)); n != 0 {
			t.Fatalf("rolled-back group left %d inventories", n)
		}
	})

	t.Run("orphaned", func(t *testing.T) {
		app, tg := testApp(t)
		loginAndInit(t, app, tg)
		ctx := context.Background()
		failLastMember(t, app)
		tg.SetFailDelete(true)
		_, err := app.UploadFilesAs(ctx, writeLocals(t, 3), "/gal/", ConflictFail, false, Presentation{})
		if code := appErrCode(t, err); code != apperr.ErrOrphanedUpload {
			t.Fatalf("code = %s, want ERR_ORPHANED_UPLOAD (%v)", code, err)
		}
		for _, p := range paths {
			if got := fileStatus(t, app, p); got != "orphaned" {
				t.Fatalf("%s = %q, want orphaned", p, got)
			}
		}
		if n := countWhere(t, app, `select count(*) from path_tags`); n != 0 {
			t.Fatalf("orphaned members carry %d path tags of a partial index", n)
		}
	})
}

// TestBatchFailureDiscardsUnsentMembers covers a batch larger than one media
// group whose first group send fails: no member reached Telegram, so every
// small member's pending row is dropped, not only the failed group's.
// Failure modes: (1) later groups' rows linger as pending and td repair
// --pending re-uploads them as loose singles; (2) the retry trips over them.
func TestBatchFailureDiscardsUnsentMembers(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	dir := t.TempDir()
	var locals, dests []string
	for i := 0; i < telegram.MaxMediaGroupMembers+2; i++ {
		name := fmt.Sprintf("f%02d.bin", i)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("payload "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		locals = append(locals, p)
		dests = append(dests, "/many/"+name)
	}
	tg.SetFailUpload(true)
	if _, err := app.UploadFilesAs(ctx, locals, "/many/", ConflictFail, false, Presentation{}); err == nil {
		t.Fatal("expected the group send to fail")
	}
	if n := rowsAt(t, app, dests...); n != 0 {
		t.Fatalf("failed batch left %d pending rows", n)
	}

	tg.SetFailUpload(false)
	data, err := app.UploadFilesAs(ctx, locals, "/many/", ConflictFail, false, Presentation{})
	if err != nil {
		t.Fatal(err)
	}
	if data.Uploaded != len(locals) || data.Resumed {
		t.Fatalf("retry = uploaded %d resumed %v, want %d fresh", data.Uploaded, data.Resumed, len(locals))
	}
}

// TestVideoAlbumCarriesThumbnail pins the thumbnail rule shared by single and
// group sends: the member's presentation decides, and only photos take none
// (rejected up front, never silently dropped).
func TestVideoAlbumCarriesThumbnail(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	pres := Presentation{Kind: telegram.KindVideo, ThumbPath: writeThumb(t)}
	data, err := app.UploadFilesAs(ctx, writeLocals(t, 2), "/clips/", ConflictFail, false, pres)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Albums) != 1 {
		t.Fatalf("albums = %+v, want one group", data.Albums)
	}
	for _, id := range data.Albums[0].MessageIDs {
		if m := messageByID(t, tg, mustChannel(t, app), id); string(m.Thumb) != string(jpegHeader) {
			t.Fatalf("member %d thumb = %v, want the supplied thumbnail", id, m.Thumb)
		}
	}

	_, err = app.UploadFilesAs(ctx, writeLocals(t, 2), "/pics/", ConflictFail, false,
		Presentation{Kind: telegram.KindPhoto, ThumbPath: writeThumb(t)})
	if code := appErrCode(t, err); code != apperr.ErrUsage {
		t.Fatalf("photo album with thumbnail code = %s, want ERR_USAGE", code)
	}
}
