package app

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
)

// transferTime formats a moment in the fixed-width UTC layout the storage
// contract sets for transfers timestamps.
func transferTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

// seedTransfer writes a Transfer row straight into the index, so a test can
// stage history without running the Transfer.
func seedTransfer(t *testing.T, dbPath string, r sqlitestore.TransferRow) {
	t.Helper()
	db, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.InsertTransfer(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

// getTransfer reads one Transfer row straight from the index; "" when the
// row is gone.
func getTransfer(t *testing.T, dbPath, id string) string {
	t.Helper()
	db, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	r, err := db.GetTransfer(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		return ""
	}
	return r.ID
}

// TestE2ETransfersRetention: when a Transfer Manager starts (here the one
// behind td transfers list), terminal Transfers that ended more than 30
// days ago are pruned, while recently ended and still-active Transfers are
// kept — however old the active one's row is.
func TestE2ETransfersRetention(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	// A real, just-finished Transfer: kept.
	small := e2eLocalFile(t, dir, "small.txt", "small")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", small, "/small.txt")

	now := time.Now().UTC()
	row := func(stage string, finished time.Time) sqlitestore.TransferRow {
		r := sqlitestore.TransferRow{
			ID: uuid.NewString(), Kind: "upload", ChannelTGID: "1001",
			Source: "/seed.bin", Dest: "/seed.bin", Stage: stage, FrontEnd: "cli",
			// Even the active row is old: only ending old, not being old,
			// prunes.
			CreatedAt: transferTime(now.Add(-40 * 24 * time.Hour)),
			UpdatedAt: transferTime(now),
		}
		if !finished.IsZero() {
			r.FinishedAt = transferTime(finished)
		}
		return r
	}
	old := row("completed", now.Add(-31*24*time.Hour))
	recent := row("completed", now.Add(-29*24*time.Hour))
	active := row("uploading", time.Time{})
	seedTransfer(t, dbPath, old)
	seedTransfer(t, dbPath, recent)
	seedTransfer(t, dbPath, active)

	list := listTransfers(t, bin, cfgPath, dbPath, statePath, "--all")
	var ids []string
	for _, tr := range list {
		ids = append(ids, fmt.Sprint(tr["id"]))
	}
	if slices.Contains(ids, old.ID) {
		t.Fatalf("terminal Transfer ended 31 days ago survived the Manager start: %v", ids)
	}
	for _, want := range []string{recent.ID, active.ID} {
		if !slices.Contains(ids, want) {
			t.Fatalf("Transfer %s missing after the Manager start: %v", want, ids)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("transfers after pruning = %v, want the cp, the recent terminal, and the active one", ids)
	}

	// Pruned means deleted from the index, not filtered from the listing.
	if got := getTransfer(t, dbPath, old.ID); got != "" {
		t.Fatalf("old terminal Transfer still in the index: %v", got)
	}
	if got := getTransfer(t, dbPath, recent.ID); got != recent.ID {
		t.Fatalf("recent terminal Transfer gone from the index")
	}
}
