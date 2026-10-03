//go:build gui

package gui_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// fakeVideoMessage is the persisted shape of one fake-Telegram message with
// its video attribute block and thumbnail.
type fakeVideoMessage struct {
	FileName string
	Kind     string
	Video    *telegram.VideoAttributes
	Thumb    []byte
}

func fakeVideoMessages(t *testing.T) []fakeVideoMessage {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("TD_FAKE_TELEGRAM_STATE"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Messages map[string][]fakeVideoMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	var out []fakeVideoMessage
	for _, msgs := range state.Messages {
		out = append(out, msgs...)
	}
	return out
}

// storedOptions reads a Transfer's recorded request from the shared index,
// the record td transfers retry re-runs.
func storedOptions(t *testing.T, id string) map[string]any {
	t.Helper()
	app, closeApp, err := service.Open(service.Options{Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	row, err := app.DB.GetTransfer(context.Background(), id)
	if err != nil || row == nil {
		t.Fatalf("GetTransfer(%s) = %v, %v", id, row, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(row.Options), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTransfersUploadVideoCarriesEveryCpOption uploads a video with the
// options td cp --as video --duration --width --height --streaming --thumb
// --upload-threads --upload-part-size-kb takes: the attributes and the
// thumbnail reach Telegram, and the per-call tuning is recorded for retry.
func TestTransfersUploadVideoCarriesEveryCpOption(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()
	thumb := filepath.Join(t.TempDir(), "thumb.jpg")
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'J', 'F', 'I', 'F'}
	if err := os.WriteFile(thumb, jpeg, 0o600); err != nil {
		t.Fatal(err)
	}

	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "clip.mp4", 2048)}, "/videos", gui.UploadOptions{
		Kind:              "video",
		DurationSeconds:   12.5,
		Width:             1920,
		Height:            1080,
		SupportsStreaming: true,
		ThumbPath:         thumb,
		UploadThreads:     3,
		UploadPartSizeKB:  256,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, ids[0], "completed")

	var clip *fakeVideoMessage
	for _, m := range fakeVideoMessages(t) {
		if m.Kind == "video" {
			clip = &m
		}
	}
	want := telegram.VideoAttributes{DurationSeconds: 12.5, Width: 1920, Height: 1080, SupportsStreaming: true}
	if clip == nil || clip.Video == nil || *clip.Video != want {
		t.Fatalf("video message = %+v, want attributes %+v", clip, want)
	}
	if string(clip.Thumb) != string(jpeg) {
		t.Fatalf("video thumb = %v, want the picked JPEG", clip.Thumb)
	}
	opts := storedOptions(t, ids[0])
	if opts["threads"] != 3.0 || opts["part_size_kb"] != 256.0 {
		t.Fatalf("stored options = %v, want threads 3 and part_size_kb 256", opts)
	}
}

func TestTransfersUploadAutoRenamesOnConflict(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)
	ctx := context.Background()

	ids, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "notes.txt", 64)}, "/", gui.UploadOptions{Policy: "rename"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForStage(t, svc, ids[0], "completed")
	if done.Dest != "/notes (1).txt" {
		t.Fatalf("auto-renamed upload landed at %s, want /notes (1).txt", done.Dest)
	}
	entries, err := svc.Drive.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("List(/) = %+v, want the original and the renamed file", entries)
	}
}

func TestTransfersUploadRejectsVideoOptionsForFolders(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)
	ctx := context.Background()

	for _, opts := range []gui.UploadOptions{{DurationSeconds: 3}, {ThumbPath: "x.jpg"}, {SupportsStreaming: true}} {
		_, err := svc.Transfers.Upload(ctx, []string{t.TempDir()}, "/tree", opts)
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
			t.Fatalf("folder Upload(%+v) error = %v, want ERR_USAGE", opts, err)
		}
	}
	// Video attributes without the video kind are the cp usage error too.
	_, err := svc.Transfers.Upload(ctx, []string{bigFile(t, "a.bin", 10)}, "/", gui.UploadOptions{Width: 640})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
		t.Fatalf("Upload(width without video) error = %v, want ERR_USAGE", err)
	}
}

func TestTransfersDownloadAutoRenamesOnConflict(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)
	ctx := context.Background()
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "notes.txt"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}

	id, err := svc.Transfers.Download(ctx, "/notes.txt", dest, gui.DownloadOptions{Policy: "rename"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForStage(t, svc, id, "completed")
	if done.Dest != filepath.Join(dest, "notes (1).txt") {
		t.Fatalf("auto-renamed download landed at %s, want notes (1).txt", done.Dest)
	}
	if body, _ := os.ReadFile(filepath.Join(dest, "notes.txt")); string(body) != "local" {
		t.Fatalf("auto-rename rewrote the local file to %q", body)
	}
	if body, _ := os.ReadFile(done.Dest); string(body) != "hello" {
		t.Fatalf("renamed download holds %q, want hello", body)
	}
}

// TestTransfersRecursiveDownloadContinuesPastFailures runs td get -r
// --continue-on-error through the facade: one file collides with a local
// file under the fail policy, the other still lands.
func TestTransfersRecursiveDownloadContinuesPastFailures(t *testing.T) {
	seedDrive(t, map[string]string{
		"/photos/a.jpg": "aaa",
		"/photos/b.jpg": "bbb",
	})
	svc := openGUI(t)
	ctx := context.Background()
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "photos", "a.jpg"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}

	id, err := svc.Transfers.Download(ctx, "/photos", dest, gui.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if failed := waitForStage(t, svc, id, "failed"); failed.ErrorCode == "" {
		t.Fatalf("recursive download without continue-on-error ended %+v, want failed", failed)
	}

	id, err = svc.Transfers.Download(ctx, "/photos", dest, gui.DownloadOptions{ContinueOnError: true})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForStage(t, svc, id, "completed")
	if done.ItemsFailed != 1 || done.ItemsDone != 1 {
		t.Fatalf("completed transfer = %+v, want 1 done and 1 failed item", done)
	}
	if body, _ := os.ReadFile(filepath.Join(dest, "photos", "b.jpg")); string(body) != "bbb" {
		t.Fatalf("photos/b.jpg = %q, want bbb", body)
	}
}
