//go:build gui

package gui_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// fakeMessages reads every channel's messages from the fake Telegram's
// persisted state (TD_FAKE_TELEGRAM_STATE) — the artifact the CLI front end
// shares — after the uploads that wrote them completed.
func fakeMessages(t *testing.T) []fakeMessage {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("TD_FAKE_TELEGRAM_STATE"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Messages map[string][]fakeMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	var out []fakeMessage
	for _, msgs := range state.Messages {
		out = append(out, msgs...)
	}
	return out
}

// fakeMessage is the persisted shape of one fake-Telegram message.
type fakeMessage struct {
	FileName string
	Kind     string
	Caption  string
}

func TestTransfersUploadWithOptionsProducesAnAlbumTransfer(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	ids, err := svc.Transfers.Upload(ctx, []string{
		bigFile(t, "kyoto.jpg", 100),
		bigFile(t, "taipei.jpg", 100),
	}, "/album", gui.UploadOptions{
		Policy:  "skip",
		Kind:    "photo",
		Caption: "spring trip",
	})
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

	// The options reached Telegram: both members went out as native
	// photos, and the album's single human caption sits on the first
	// member (its machine block names kyoto.jpg) only.
	msgs := fakeMessages(t)
	photos := 0
	captioned := 0
	for _, m := range msgs {
		if m.Kind == "photo" {
			photos++
		}
		if strings.Contains(m.Caption, "spring trip") {
			captioned++
			if m.Kind != "photo" || !strings.Contains(m.Caption, "kyoto.jpg") {
				t.Fatalf("captioned message = %+v, want the first photo member", m)
			}
		}
	}
	if photos != 2 || captioned != 1 {
		t.Fatalf("fake messages = %+v, want 2 photo members with the caption on exactly one", msgs)
	}
}

func TestTransfersUploadReplaceNeedsConfirmation(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)
	ctx := context.Background()
	src := bigFile(t, "notes.txt", 64)

	// The gate fails fast, before any Transfer exists.
	_, err := svc.Transfers.Upload(ctx, []string{src}, "/", gui.UploadOptions{Policy: "replace"})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_CONFIRMATION_REQUIRED" {
		t.Fatalf("Upload replace without confirmation error = %v, want ERR_CONFIRMATION_REQUIRED", err)
	}
	list, err := svc.Transfers.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Active)+len(list.History) != 0 {
		t.Fatalf("List after the rejected upload = %+v, want no Transfer created", list)
	}

	// Confirmed, the replace runs and the new content lands.
	ids, err := svc.Transfers.Upload(ctx, []string{src}, "/", gui.UploadOptions{Policy: "replace", ConfirmReplace: true})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "completed")

	dest := t.TempDir()
	dl, err := svc.Transfers.Download(ctx, "/notes.txt", dest, gui.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, dl, "completed")
	got, err := os.ReadFile(filepath.Join(dest, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("downloaded notes.txt does not match the replacement upload")
	}
}

func TestTransfersPlanUploadMatchesTheCpDryRun(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()
	paths := []string{bigFile(t, "a.txt", 10), bigFile(t, "b.txt", 20)}

	// The facade's plan is the service's dry-run plan for the same input,
	// policy by policy — the plan td cp --dry-run prints.
	for _, tc := range []struct {
		guiPolicy string
		want      service.ConflictPolicy
	}{
		{"", service.ConflictFail},
		{"fail", service.ConflictFail},
		{"skip", service.ConflictSkip},
		{"replace", service.ConflictReplace},
	} {
		plan, err := svc.Transfers.PlanUpload(ctx, paths, "/docs", tc.guiPolicy)
		if err != nil {
			t.Fatalf("PlanUpload(%q) = %v", tc.guiPolicy, err)
		}
		want := service.PlanUpload(paths, "/docs", tc.want)
		if !slices.Equal(plan.Local, want.Local) || plan.Remote != want.Remote ||
			plan.Policy != string(want.Policy) || plan.WouldReplace != want.WouldReplace {
			t.Fatalf("PlanUpload(%q) = %+v, want the service plan %+v", tc.guiPolicy, plan, want)
		}
	}

	if _, err := svc.Transfers.PlanUpload(ctx, paths, "/docs", "rename"); err == nil {
		t.Fatal("PlanUpload with a policy the sheet does not offer succeeded, want ERR_USAGE")
	} else {
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
			t.Fatalf("PlanUpload(rename) error = %v, want ERR_USAGE", err)
		}
	}
	if _, err := svc.Transfers.PlanUpload(ctx, nil, "/docs", ""); err == nil {
		t.Fatal("PlanUpload without paths succeeded, want ERR_USAGE")
	}
}

func TestTransfersPlanUploadSurfacesTheUploadLimit(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	// A sparse 3 GiB file: stat reports its size without the disk cost.
	dir := t.TempDir()
	huge := filepath.Join(dir, "huge.bin")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(3 << 30); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	small := bigFile(t, "small.txt", 42)

	plan, err := svc.Transfers.PlanUpload(ctx, []string{huge, small, dir}, "/dest", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.UploadLimitBytes != 2147483648 {
		t.Fatalf("UploadLimitBytes = %d, want the fake's 2 GB free-tier limit", plan.UploadLimitBytes)
	}
	if len(plan.Files) != 3 {
		t.Fatalf("plan files = %+v, want one entry per path", plan.Files)
	}
	if got := plan.Files[0]; got.Size != 3<<30 || !got.OverLimit || got.Dir {
		t.Fatalf("plan file huge.bin = %+v, want 3 GiB over the limit", got)
	}
	if got := plan.Files[1]; got.Size != 42 || got.OverLimit || got.Dir {
		t.Fatalf("plan file small.txt = %+v, want 42 bytes under the limit", got)
	}
	if got := plan.Files[2]; !got.Dir {
		t.Fatalf("plan file for the directory = %+v, want it marked as a folder", got)
	}
}

func TestTransfersDownloadHonoursTheLocalConflictPolicy(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)
	ctx := context.Background()
	dest := t.TempDir()
	local := filepath.Join(dest, "notes.txt")

	id, err := svc.Transfers.Download(ctx, "/notes.txt", dest, gui.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, id, "completed")

	// The default policy fails on the existing local file.
	id, err = svc.Transfers.Download(ctx, "/notes.txt", dest, gui.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForStage(t, svc, id, "failed")
	if failed.ErrorCode != "ERR_LOCAL_PATH_EXISTS" {
		t.Fatalf("re-download under fail policy ended %+v, want ERR_LOCAL_PATH_EXISTS", failed)
	}

	// Skip keeps the local file.
	if err := os.WriteFile(local, []byte("local edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err = svc.Transfers.Download(ctx, "/notes.txt", dest, gui.DownloadOptions{Policy: "skip"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, id, "completed")
	if body, _ := os.ReadFile(local); string(body) != "local edit" {
		t.Fatalf("skipped download rewrote the local file to %q", body)
	}

	// Replace overwrites it.
	id, err = svc.Transfers.Download(ctx, "/notes.txt", dest, gui.DownloadOptions{Policy: "replace"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, id, "completed")
	if body, _ := os.ReadFile(local); string(body) != "hello" {
		t.Fatalf("replaced download left %q, want the remote content", body)
	}

	// A policy the sheet does not offer fails before anything starts.
	if _, err := svc.Transfers.Download(ctx, "/notes.txt", dest, gui.DownloadOptions{Policy: "rename"}); err == nil {
		t.Fatal("Download with an unknown policy succeeded, want ERR_USAGE")
	} else {
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
			t.Fatalf("Download(rename) error = %v, want ERR_USAGE", err)
		}
	}
}

func TestTransfersUploadRejectsPresentationAndCaptionForFolders(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()
	dir := t.TempDir()

	for _, opts := range []gui.UploadOptions{{Kind: "photo"}, {Caption: "hi"}} {
		if _, err := svc.Transfers.Upload(ctx, []string{dir}, "/tree", opts); err == nil {
			t.Fatalf("Upload(%+v) of a folder succeeded, want ERR_USAGE", opts)
		} else {
			var guiErr *gui.Error
			if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
				t.Fatalf("Upload(%+v) error = %v, want ERR_USAGE", opts, err)
			}
		}
	}
}

func TestTransfersAlbumUploadCannotReplace(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	// The service rejects replace for albums (UploadFilesAs); the facade
	// fails fast, before any Transfer exists, so the sheet's mistake never
	// enters the index.
	_, err := svc.Transfers.Upload(ctx, []string{
		bigFile(t, "a.jpg", 10),
		bigFile(t, "b.jpg", 10),
	}, "/album", gui.UploadOptions{Policy: "replace", ConfirmReplace: true})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
		t.Fatalf("album Upload with replace error = %v, want ERR_USAGE", err)
	}
	list, err := svc.Transfers.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Active)+len(list.History) != 0 {
		t.Fatalf("List after the rejected album = %+v, want no Transfer created", list)
	}
}

func TestTransfersRecursiveUploadContinuesPastFailures(t *testing.T) {
	seedDrive(t, nil)
	t.Setenv("TD_FAKE_PART_SIZE", "1048576")
	t.Setenv("TD_FAKE_FAIL_UPLOAD_AFTER_PARTS", "2")
	svc := openGUI(t)
	ctx := context.Background()

	// The failing file gets its own subdirectory: a source directory's
	// children go out as one album batch, so a failure next to good.txt
	// would take good.txt down with it.
	dir := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good.txt"), []byte("good"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The fake fails this resumable upload once, after two parts.
	if err := os.WriteFile(filepath.Join(dir, "sub", "bad.bin"), make([]byte, 12*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}

	// Continue past failures: the good file lands and the Transfer
	// completes with one failed item.
	ids, err := svc.Transfers.Upload(ctx, []string{dir}, "/tree", gui.UploadOptions{ContinueOnError: true})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForStage(t, svc, ids[0], "completed")
	if done.Kind != "recursive_upload" || done.ItemsFailed != 1 || done.ItemsDone != 1 {
		t.Fatalf("completed transfer = %+v, want a recursive_upload with 1 done and 1 failed item", done)
	}
	// Only the good file landed: the failed group published nothing, and
	// empty directories are not persisted in V1.
	entries, err := svc.Drive.List(ctx, "/tree/tree")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "good.txt" {
		t.Fatalf("List(/tree/tree) = %+v, want only good.txt", entries)
	}
}

func TestTransfersIncludeEmptyDirsSurfacesTheServiceError(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	// The CLI's --include-empty-dirs exists but the service answers with
	// its typed V1 error; the option passes the choice through and the
	// Transfer wears the service's answer.
	ids, err := svc.Transfers.Upload(ctx, []string{t.TempDir()}, "/tree", gui.UploadOptions{IncludeEmptyDirs: true})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForStage(t, svc, ids[0], "failed")
	if failed.ErrorCode != "ERR_EMPTY_DIRS_UNSUPPORTED" {
		t.Fatalf("include-empty-dirs transfer ended %+v, want ERR_EMPTY_DIRS_UNSUPPORTED", failed)
	}
}
