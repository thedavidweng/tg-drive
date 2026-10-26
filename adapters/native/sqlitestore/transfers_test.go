package sqlitestore

import (
	"context"
	"path/filepath"
	"testing"
)

// seedTransfer inserts one Transfer row with the given stage and finish
// time, the columns DeleteFinishedTransfers selects on.
func seedTransfer(t *testing.T, d *DB, id, stage, finishedAt string) {
	t.Helper()
	if _, err := d.Raw().Exec(`insert into transfers(id,kind,channel_tg_id,source,dest,stage,front_end,finished_at,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?)`,
		id, "upload", "100", "/local", "/data/"+id, stage, "cli", finishedAt, "2026-01-01T00:00:00.000000000Z", "2026-01-01T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
}

// TestDeleteFinishedTransfersNeverTakesAClaimedTransfer: a Transfer a retry
// has just claimed has no finish time, so the conditional delete cannot
// select it, even when the same statement runs after the retry's claim. A
// select-then-delete would have taken the id the retry cleared the finish
// time on.
func TestDeleteFinishedTransfersNeverTakesAClaimedTransfer(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()

	seedTransfer(t, d, "done", "completed", "2026-01-01T00:00:00.000000000Z")
	seedTransfer(t, d, "claimed", "interrupted", "2026-01-01T00:00:00.000000000Z")
	seedTransfer(t, d, "running", "uploading", "")

	// A retry claims the interrupted Transfer: same row, no finish time.
	if _, err := d.Raw().Exec(`update transfers set stage='queued', finished_at='' where id='claimed'`); err != nil {
		t.Fatal(err)
	}

	ids, err := d.DeleteFinishedTransfers(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "done" {
		t.Fatalf("deleted %v, want only the ended transfer", ids)
	}
	for _, id := range []string{"claimed", "running"} {
		row, err := d.GetTransfer(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if row == nil {
			t.Fatalf("transfer %s was removed while it had no finish time", id)
		}
	}
}

// TestDeleteFinishedTransfersRespectsCutoff: the retention prune removes
// only the Transfers that ended before its cutoff.
func TestDeleteFinishedTransfersRespectsCutoff(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()

	seedTransfer(t, d, "old", "failed", "2026-01-01T00:00:00.000000000Z")
	seedTransfer(t, d, "new", "failed", "2026-03-01T00:00:00.000000000Z")

	ids, err := d.DeleteFinishedTransfers(ctx, "2026-02-01T00:00:00.000000000Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "old" {
		t.Fatalf("deleted %v, want only the transfer older than the cutoff", ids)
	}
	if row, err := d.GetTransfer(ctx, "new"); err != nil || row == nil {
		t.Fatalf("newer transfer: row=%v err=%v, want it kept", row, err)
	}
}
