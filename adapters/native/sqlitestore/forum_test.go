package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func forumV6Fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v6.db")
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	ctx := context.Background()
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, m := range migrations[:6] {
		for _, stmt := range m.stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
		if m.run != nil {
			if err := m.run(ctx, tx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_version values(?,'now')`, m.version); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
 insert into accounts(id,tg_user_id,created_at,updated_at) values(4,'account','now','now');
 insert into channels(id,account_id,tg_channel_id,title,root_local_path,discussion_tg_channel_id,created_at,updated_at) values(9,4,'100','Drive','/original','200','now','now');
 insert into nodes(id,channel_id,canonical_path,parent_path,display_name,type,created_at,updated_at) values(7,9,'/data','/','data','dir','now','now');
 insert into files(id,channel_id,node_id,message_id,canonical_path,display_name,status,updated_at) values(11,9,7,42,'/data/file','file','active','now');
 insert into path_tags(file_id,tag,depth) values(11,'#td_data',1);
 insert into upload_progress(key,file_id,part_size,total_parts,total_bytes,confirmed_parts,updated_at) values('file:11',11,1024,2,2048,'[0]','now');
 insert into scan_state(channel_id,last_scanned_message_id,checkpoint_message_id,full_scan_started_at,updated_at) values(9,42,30,'started','now');
 insert into directory_delete_operations(id,channel_id,path,mode,total,remaining,status,created_at,updated_at) values('delete',9,'/old','delete',3,2,'failed','now','now');
 insert into directory_move_operations(id,channel_id,source_path,requested_destination,destination_path,total,remaining,status,created_at,updated_at) values('move',9,'/data','/new','/new',3,2,'failed','now','now');
 insert into transfers(id,kind,channel_tg_id,source,dest,stage,front_end,created_at,updated_at) values('transfer','upload','100','/local','/data/file','interrupted','cli','now','now');`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func forumSnapshot(t *testing.T, raw *sql.DB) []string {
	t.Helper()
	var out []string
	for _, table := range []string{"files", "nodes", "path_tags", "upload_progress", "scan_state", "directory_delete_operations", "directory_move_operations", "transfers"} {
		rows, err := raw.Query(`select * from ` + table + ` order by rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			out = append(out, table+fmt.Sprint(values))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
	}
	return out
}

func TestForumMigrationPreservesV6AndReopens(t *testing.T) {
	path := forumV6Fixture(t)
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	before := forumSnapshot(t, raw)
	_ = raw.Close()
	for range 2 {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := forumSnapshot(t, db.Raw()); !reflect.DeepEqual(got, before) {
			t.Fatalf("migration changed child state: before=%v after=%v", before, got)
		}
		var topic int
		var root, discussion string
		if err := db.Raw().QueryRow(`select topic_id,root_local_path,discussion_tg_channel_id from channels where id=9`).Scan(&topic, &root, &discussion); err != nil || topic != 0 || root != "/original" || discussion != "200" {
			t.Fatalf("binding changed: %d %q %q %v", topic, root, discussion, err)
		}
		if _, err := db.Raw().Exec(`insert into channels(account_id,tg_channel_id,topic_id,title,root_local_path,created_at,updated_at) values(4,'100',10,'Topic','/topic','now','now') on conflict(account_id,tg_channel_id,topic_id) do nothing`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Raw().Exec(`insert into channels(account_id,tg_channel_id,topic_id,title,root_local_path,created_at,updated_at) values(4,'100',10,'Duplicate','/duplicate','now','now')`); err == nil {
			t.Fatal("duplicate peer/topic accepted")
		}
		if _, err := db.Raw().Exec(`insert into files(channel_id,canonical_path,display_name,status,updated_at) values(999,'/bad','bad','active','now')`); err == nil {
			t.Fatal("foreign keys not re-enabled")
		}
		_ = db.Close()
	}
}

func TestForumMigrationIntegrityFailureRollsBack(t *testing.T) {
	path := forumV6Fixture(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`pragma foreign_keys=off; update files set channel_id=999 where id=11`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if db, err := Open(path); err == nil {
		_ = db.Close()
		t.Fatal("corrupt foreign key accepted")
	}
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var version, topics int
	if err := raw.QueryRow(`select max(version) from schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`select count(*) from pragma_table_info('channels') where name='topic_id'`).Scan(&topics); err != nil {
		t.Fatal(err)
	}
	if version != 6 || topics != 0 {
		t.Fatalf("failed migration committed: version=%d topic columns=%d", version, topics)
	}
}
