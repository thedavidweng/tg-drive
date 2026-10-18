package sqlitestore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrationSixPreservesDeleteHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Raw().Exec(`
		insert into accounts(id,tg_user_id,created_at,updated_at) values(1,'u','now','now');
		insert into channels(id,account_id,tg_channel_id,title,root_local_path,created_at,updated_at)
		values(1,1,'c','Drive','/root','now','now');
		drop table directory_move_operations;
		delete from schema_version where version=6;
	`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.StartDirectoryDelete(ctx, "previous-delete", 1, "/deleted", "delete", 3, "now"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	previous, err := db.DirectoryDeleteProgress(ctx, "previous-delete")
	if err != nil || previous.Remaining != 3 || previous.ChannelID != 1 {
		t.Fatalf("previous delete = %+v, %v", previous, err)
	}
	if err := db.StartDirectoryMove(ctx, "new-move", 1, "/source", "/target", "/target", 2, "now"); err != nil {
		t.Fatal(err)
	}
	if destination, ok, err := db.DirectoryMoveRetryDestination(ctx, 2, "/source", "/target"); err != nil || ok || destination != "" {
		t.Fatalf("cross-channel retry = %q, %v, %v", destination, ok, err)
	}
	if err := db.UpdateDirectoryMove(ctx, "new-move", "completed", "/source/b", "", 2, 0, 0, "later"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.DirectoryMoveRetryDestination(ctx, 1, "/source", "/target"); err != nil || ok {
		t.Fatalf("completed operation used for retry: %v, %v", ok, err)
	}
}
