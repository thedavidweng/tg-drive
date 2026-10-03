//go:build gui

package gui_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/core/telegram/fake"
	"github.com/thedavidweng/tg-drive/internal/gui"
)

// seedSaved seeds a bound drive and, after the seeding front end closes,
// adds the given messages to the fake's Saved Messages chat.
func seedSaved(t *testing.T, files map[string]string, msgs ...telegram.Message) {
	t.Helper()
	seedDriveEnv(t, files, func(statePath string) {
		tg := fake.NewPersistent(statePath)
		for _, m := range msgs {
			tg.AddSavedMessage(m)
		}
	})
}

func savedDocument(id int, name, caption string, data []byte) telegram.Message {
	return telegram.Message{
		ID:       id,
		Kind:     telegram.KindDocument,
		FileName: name,
		MIME:     "application/octet-stream",
		FileSize: int64(len(data)),
		Data:     data,
		Caption:  caption,
		Date:     time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
	}
}

func importItemByMsg(t *testing.T, out *gui.ImportOutcome, id int) gui.ImportItem {
	t.Helper()
	for _, it := range out.Items {
		if it.MessageID == id {
			return it
		}
	}
	t.Fatalf("no import item for saved message %d: %+v", id, out.Items)
	return gui.ImportItem{}
}

// importPrompts returns the recorded import.prompt events in order.
func (r *eventRecorder) importPrompts() []gui.ImportPrompt {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []gui.ImportPrompt
	for _, e := range r.events {
		if e.name == gui.EventImportPrompt {
			out = append(out, e.data.(gui.ImportPrompt))
		}
	}
	return out
}

// importItems returns the recorded import.item events in order.
func (r *eventRecorder) itemEvents(name string) []gui.ItemEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []gui.ItemEvent
	for _, e := range r.events {
		if e.name == name {
			out = append(out, e.data.(gui.ItemEvent))
		}
	}
	return out
}

func TestImportPreviewPlansSavedItemsWithoutTouchingTheDrive(t *testing.T) {
	seedSaved(t, nil,
		savedDocument(101, "clip.mp4", "holiday clip", []byte("video-bytes")),
		savedDocument(102, "notes.txt", "", []byte("notes-bytes")),
	)
	svc := openGUI(t)

	out, err := svc.Import.Preview(context.Background(), gui.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.DryRun || out.Into != "/saved" || !out.HistoryComplete {
		t.Fatalf("Preview = %+v, want a dry run into /saved with a complete history read", out)
	}
	if len(out.Items) != 2 {
		t.Fatalf("Preview items = %+v, want both saved messages planned", out.Items)
	}
	for _, id := range []int{101, 102} {
		it := importItemByMsg(t, out, id)
		if it.Action != "import" || it.Path == "" {
			t.Fatalf("Preview item %d = %+v, want an import action with a destination path", id, it)
		}
	}
	// A dry run lands nothing in the drive.
	entries, err := svc.Drive.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Path == "/saved" {
			t.Fatalf("List(/) = %+v, want no /saved from a dry run", entries)
		}
	}
}

func TestImportRunRequiresConfirmation(t *testing.T) {
	seedSaved(t, nil, savedDocument(101, "clip.mp4", "", []byte("video-bytes")))
	svc := openGUI(t)

	_, err := svc.Import.Run(context.Background(), gui.ImportOptions{})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Run error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CONFIRMATION_REQUIRED" || guiErr.Category != "safety" {
		t.Fatalf("Run error = %+v, want ERR_CONFIRMATION_REQUIRED / safety", guiErr)
	}
}

func TestImportDeleteSourceRequiresConfirmation(t *testing.T) {
	seedSaved(t, nil, savedDocument(101, "clip.mp4", "", []byte("video-bytes")))
	svc := openGUI(t)

	// The gate holds even for a dry run: delete-source is never free.
	for _, call := range map[string]func() error{
		"preview": func() error {
			_, err := svc.Import.Preview(context.Background(), gui.ImportOptions{DeleteSource: true})
			return err
		},
		"run": func() error {
			_, err := svc.Import.Run(context.Background(), gui.ImportOptions{DeleteSource: true})
			return err
		},
	} {
		err := call()
		var guiErr *gui.Error
		if !errors.As(err, &guiErr) {
			t.Fatalf("error = %T %v, want *gui.Error", err, err)
		}
		if guiErr.Code != "ERR_CONFIRMATION_REQUIRED" {
			t.Fatalf("error = %+v, want ERR_CONFIRMATION_REQUIRED", guiErr)
		}
	}
}

func TestImportRunImportsItemsAndReportsProgress(t *testing.T) {
	seedSaved(t, nil,
		savedDocument(101, "clip.mp4", "holiday clip", []byte("video-bytes")),
		savedDocument(102, "notes.txt", "", []byte("notes-bytes")),
	)
	svc := openGUI(t)
	rec := &eventRecorder{}
	svc.SetImportEmitter(rec.emit)

	out, err := svc.Import.Run(context.Background(), gui.ImportOptions{Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.DryRun || out.Imported != 2 || out.Failed != 0 {
		t.Fatalf("Run = %+v, want 2 imported, 0 failed", out)
	}
	// The imported files landed in the drive.
	entries, err := svc.Drive.List(context.Background(), "/saved")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("List(/saved) = %+v, want the two imported files", entries)
	}
	// Each item reported its outcome as an import.item event.
	events := rec.itemEvents(gui.EventImportItem)
	if len(events) != 2 {
		t.Fatalf("import.item events = %+v, want one per item", events)
	}
	for _, e := range events {
		if e.Status != "completed" || e.Path == "" {
			t.Fatalf("import.item = %+v, want a completed item with its path", e)
		}
	}
	if events[len(events)-1].Completed != 2 {
		t.Fatalf("last import.item = %+v, want the tally at 2 completed", events[len(events)-1])
	}
}

func TestImportRunSkipsDuplicates(t *testing.T) {
	seedSaved(t,
		map[string]string{"/clip.mp4": "video-bytes"},
		savedDocument(101, "clip.mp4", "holiday clip", []byte("video-bytes")),
	)
	svc := openGUI(t)

	out, err := svc.Import.Run(context.Background(), gui.ImportOptions{Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Duplicates != 1 || out.Skipped != 1 || out.Imported != 0 {
		t.Fatalf("Run = %+v, want 1 skipped duplicate, 0 imported", out)
	}
	it := importItemByMsg(t, out, 101)
	if it.Action != "skip" || it.DuplicateOf != "/clip.mp4" {
		t.Fatalf("duplicate item = %+v, want a skip naming /clip.mp4", it)
	}
}

func TestImportRunDeletesSourcesOnlyWithConfirmation(t *testing.T) {
	seedSaved(t, nil, savedDocument(101, "clip.mp4", "", []byte("video-bytes")))
	svc := openGUI(t)

	out, err := svc.Import.Run(context.Background(), gui.ImportOptions{Confirm: true, DeleteSource: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.SourcesDeleted != 1 {
		t.Fatalf("Run = %+v, want 1 source deleted", out)
	}
	if it := importItemByMsg(t, out, 101); !it.SourceDeleted {
		t.Fatalf("item = %+v, want source_deleted", it)
	}
}

// The photo-storage choice arrives as an import.prompt event; the import
// waits for the frontend's AnswerPrompt.
func TestImportPhotoChoiceArrivesAsAPromptEvent(t *testing.T) {
	seedSaved(t, nil, telegram.Message{
		ID:       201,
		Kind:     telegram.KindPhoto,
		MIME:     "image/jpeg",
		FileSize: 3,
		Data:     []byte("jpg"),
		Date:     time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	svc := openGUI(t)
	rec := &eventRecorder{}
	svc.SetImportEmitter(rec.emit)

	type result struct {
		out *gui.ImportOutcome
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := svc.Import.Run(context.Background(), gui.ImportOptions{Confirm: true})
		done <- result{out, err}
	}()

	var prompt gui.ImportPrompt
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if prompts := rec.importPrompts(); len(prompts) > 0 {
			prompt = prompts[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if prompt.ID == "" {
		t.Fatal("no import.prompt event for the photo choice")
	}
	if prompt.Kind != gui.PromptPhotos || prompt.Photos != 1 {
		t.Fatalf("import.prompt = %+v, want a photos prompt for 1 photo", prompt)
	}
	if err := svc.Import.AnswerPrompt(context.Background(), prompt.ID, "document"); err != nil {
		t.Fatal(err)
	}

	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.out.PhotosAs != "document" || res.out.Imported != 1 {
		t.Fatalf("Run = %+v, want the photo imported as a document", res.out)
	}
}

// Cancelling the photo prompt aborts the import with ERR_CANCELLED, the way
// cancelling an auth prompt aborts the login.
func TestImportPhotoPromptCancelAbortsTheImport(t *testing.T) {
	seedSaved(t, nil, telegram.Message{
		ID:       201,
		Kind:     telegram.KindPhoto,
		MIME:     "image/jpeg",
		FileSize: 3,
		Data:     []byte("jpg"),
		Date:     time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	svc := openGUI(t)
	rec := &eventRecorder{}
	svc.SetImportEmitter(rec.emit)

	done := make(chan error, 1)
	go func() {
		_, err := svc.Import.Run(context.Background(), gui.ImportOptions{Confirm: true})
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if prompts := rec.importPrompts(); len(prompts) > 0 {
			if err := svc.Import.CancelPrompt(context.Background(), prompts[0].ID); err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	err := <-done
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_CANCELLED" {
		t.Fatalf("Run error = %v, want ERR_CANCELLED", err)
	}
}

// Answering a prompt that does not exist is a usage error, not a panic.
func TestImportAnswerPromptRejectsUnknownIDs(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)

	err := svc.Import.AnswerPrompt(context.Background(), "prompt-999", "document")
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
		t.Fatalf("AnswerPrompt error = %v, want ERR_USAGE", err)
	}
}
