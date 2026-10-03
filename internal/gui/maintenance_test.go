//go:build gui

package gui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/core/telegram/fake"
	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// seedUnmanaged seeds a bound drive and adds a plain document message
// straight to its channel — content someone posted before td managed it.
func seedUnmanaged(t *testing.T, msg telegram.Message) {
	t.Helper()
	seedDriveEnv(t, map[string]string{"/notes.txt": "hello"}, func(statePath string) {
		tg := fake.NewPersistent(statePath)
		ch, err := tg.ResolveChannel(context.Background(), "Drive")
		if err != nil {
			t.Fatal(err)
		}
		tg.AddMessage(ch.ID, msg)
	})
}

func unmanagedDocument(id int, name string, data []byte) telegram.Message {
	return telegram.Message{
		ID:       id,
		Kind:     telegram.KindDocument,
		FileName: name,
		MIME:     "application/octet-stream",
		FileSize: int64(len(data)),
		Data:     data,
		Date:     time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC),
	}
}

func adoptItemByMsg(t *testing.T, out *gui.AdoptOutcome, id int) gui.AdoptItem {
	t.Helper()
	for _, it := range out.Items {
		if it.MessageID == id {
			return it
		}
	}
	t.Fatalf("no adopt item for message %d: %+v", id, out.Items)
	return gui.AdoptItem{}
}

func TestAdoptPreviewPlansUnmanagedMessages(t *testing.T) {
	seedUnmanaged(t, unmanagedDocument(501, "old-report.pdf", []byte("pdf-bytes")))
	svc := openGUI(t)

	out, err := svc.Maintenance.PreviewAdopt(context.Background(), gui.AdoptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.DryRun {
		t.Fatalf("PreviewAdopt = %+v, want a dry run", out)
	}
	it := adoptItemByMsg(t, out, 501)
	if it.Action != "adopt" || it.Path == "" || it.FileName != "old-report.pdf" {
		t.Fatalf("PreviewAdopt item = %+v, want an adopt action with a path", it)
	}
	// The td-managed message is recognized, not adopted again.
	var managed gui.AdoptItem
	for _, other := range out.Items {
		if other.MessageID == 1 {
			managed = other
		}
	}
	if managed.Action != "skip" {
		t.Fatalf("PreviewAdopt td-managed item = %+v, want a skip", managed)
	}
	// A preview changes nothing: the message's directory is not in the
	// index yet (unmanaged documents land in /files).
	entries, err := svc.Drive.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "files" {
			t.Fatalf("List(/) = %+v, want no /files from a dry run", entries)
		}
	}
}

func TestAdoptRunRequiresConfirmation(t *testing.T) {
	seedUnmanaged(t, unmanagedDocument(501, "old-report.pdf", []byte("pdf-bytes")))
	svc := openGUI(t)

	_, err := svc.Maintenance.Adopt(context.Background(), gui.AdoptOptions{})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Adopt error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CONFIRMATION_REQUIRED" || guiErr.Category != "safety" {
		t.Fatalf("Adopt error = %+v, want ERR_CONFIRMATION_REQUIRED / safety", guiErr)
	}
}

func TestAdoptRunAdoptsTheMessage(t *testing.T) {
	seedUnmanaged(t, unmanagedDocument(501, "old-report.pdf", []byte("pdf-bytes")))
	svc := openGUI(t)

	out, err := svc.Maintenance.Adopt(context.Background(), gui.AdoptOptions{Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.DryRun || out.Adopted == 0 || out.Failed != 0 {
		t.Fatalf("Adopt = %+v, want the message adopted, none failed", out)
	}
	it := adoptItemByMsg(t, out, 501)
	if it.Action != "adopt" || it.Path == "" {
		t.Fatalf("Adopt item = %+v, want an adopt action with a path", it)
	}
	// The adopted file is browsable (unmanaged documents land in /files).
	entries, err := svc.Drive.List(context.Background(), "/files")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "old-report.pdf" {
		t.Fatalf("List(/files) = %+v, want the adopted file", entries)
	}
}

func TestRepairEachModeReturnsItsOutcome(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)
	ctx := context.Background()

	// Pending: nothing stale, so all counters are zero but present.
	pending, err := svc.Maintenance.Repair(ctx, gui.RepairOptions{Mode: gui.RepairModePending})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Mode != gui.RepairModePending || pending.Pending == nil {
		t.Fatalf("Repair(pending) = %+v, want the pending outcome", pending)
	}

	// Path: re-renders the one file's records.
	byPath, err := svc.Maintenance.Repair(ctx, gui.RepairOptions{Mode: gui.RepairModePath, Path: "/notes.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if byPath.Path == nil || byPath.Path.Repaired != "/notes.txt" {
		t.Fatalf("Repair(path) = %+v, want /notes.txt repaired", byPath)
	}

	// Captions, dry run: every modern caption is examined, none cleaned yet.
	captions, err := svc.Maintenance.Repair(ctx, gui.RepairOptions{Mode: gui.RepairModeCaptions, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if captions.Captions == nil || !captions.Captions.DryRun || captions.Captions.Total != 1 {
		t.Fatalf("Repair(captions) = %+v, want a dry run over the one file", captions)
	}

	// Orphaned: nothing orphaned.
	orphaned, err := svc.Maintenance.Repair(ctx, gui.RepairOptions{Mode: gui.RepairModeOrphaned})
	if err != nil {
		t.Fatal(err)
	}
	if orphaned.Orphaned == nil {
		t.Fatalf("Repair(orphaned) = %+v, want the orphaned outcome", orphaned)
	}

	// Scan errors: a reprocess sweep reports resolved/pending counts.
	scanErrs, err := svc.Maintenance.Repair(ctx, gui.RepairOptions{Mode: gui.RepairModeScanErrors})
	if err != nil {
		t.Fatal(err)
	}
	if scanErrs.ScanErrors == nil {
		t.Fatalf("Repair(scan-errors) = %+v, want the scan-errors outcome", scanErrs)
	}
}

func TestRepairHashBackfillsAnAdoptedFile(t *testing.T) {
	// A CLI front end uploads a file without its content hash. This runs
	// before the GUI opens because the fake Telegram keeps its state in
	// memory per process: a client opened earlier never sees messages
	// another front end adds later.
	seedDriveEnv(t, map[string]string{"/notes.txt": "hello"}, func(string) {
		cli, closeCLI, err := service.Open(service.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer closeCLI()
		local := filepath.Join(t.TempDir(), "plain.bin")
		if err := os.WriteFile(local, []byte("plain-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.UploadFile(context.Background(), local, "/plain.bin",
			service.ConflictFail, true, service.UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	svc := openGUI(t)
	ctx := context.Background()

	out, err := svc.Maintenance.Repair(ctx, gui.RepairOptions{Mode: gui.RepairModeHash})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hash == nil || out.Hash.Backfilled != 1 || out.Hash.Total != 1 {
		t.Fatalf("Repair(hash) = %+v, want 1 backfilled of 1", out)
	}
	if len(out.Hash.Items) != 1 || out.Hash.Items[0].Path != "/plain.bin" || out.Hash.Items[0].Action != "backfilled" {
		t.Fatalf("Repair(hash) items = %+v, want /plain.bin backfilled", out.Hash.Items)
	}
}

func TestRepairDeleteOrphanedRequiresConfirmation(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)

	_, err := svc.Maintenance.Repair(context.Background(),
		gui.RepairOptions{Mode: gui.RepairModeOrphaned, DeleteOrphaned: true})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) {
		t.Fatalf("Repair error = %T %v, want *gui.Error", err, err)
	}
	if guiErr.Code != "ERR_CONFIRMATION_REQUIRED" || guiErr.Category != "safety" {
		t.Fatalf("Repair error = %+v, want ERR_CONFIRMATION_REQUIRED / safety", guiErr)
	}
}

func TestRepairRejectsUnknownModes(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)

	_, err := svc.Maintenance.Repair(context.Background(), gui.RepairOptions{Mode: "defrag"})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
		t.Fatalf("Repair(defrag) error = %v, want ERR_USAGE", err)
	}
}

func TestRepairPathModeRequiresAPath(t *testing.T) {
	seedDrive(t, nil)
	svc := openGUI(t)

	_, err := svc.Maintenance.Repair(context.Background(), gui.RepairOptions{Mode: gui.RepairModePath})
	var guiErr *gui.Error
	if !errors.As(err, &guiErr) || guiErr.Code != "ERR_USAGE" {
		t.Fatalf("Repair(path) error = %v, want ERR_USAGE", err)
	}
}

func TestDoctorReportsEachCheck(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)

	report, err := svc.Maintenance.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) == 0 {
		t.Fatal("Doctor reported no checks")
	}
	byName := map[string]gui.DoctorCheck{}
	for _, c := range report.Checks {
		byName[c.Name] = c
		switch c.Status {
		case "pass", "warn", "fail", "unknown":
		default:
			t.Fatalf("check %q has status %q, want pass/warn/fail/unknown", c.Name, c.Status)
		}
	}
	// The fake grants every capability the bound channel needs.
	for _, name := range []string{"auth", "channel", "upload", "delete", "invite_link", "edit_old_caption", "history_read"} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("no %q check in %+v", name, report.Checks)
		}
		if c.Status != "pass" {
			t.Fatalf("check %q = %+v, want pass against the fake", name, c)
		}
	}
	// Sorted, so the frontend renders a stable list.
	for i := 1; i < len(report.Checks); i++ {
		if report.Checks[i-1].Name > report.Checks[i].Name {
			t.Fatalf("checks out of order: %+v", report.Checks)
		}
	}
	if report.MaxUploadBytes == nil || *report.MaxUploadBytes <= 0 {
		t.Fatalf("Doctor max_upload_bytes = %v, want the fake's limit", report.MaxUploadBytes)
	}
}

func TestPathCodecDoctorReportsSelfTestAndDatabase(t *testing.T) {
	seedDrive(t, map[string]string{"/notes.txt": "hello"})
	svc := openGUI(t)

	report, err := svc.Maintenance.PathCodecDoctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.FixedVectors != "pass" || report.DBCheck != "pass" {
		t.Fatalf("PathCodecDoctor = %+v, want pass/pass against the fake", report)
	}
}
