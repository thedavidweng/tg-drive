package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/core/telegram/fake"
)

func TestE2EForumTopicOperationsAndReconstruction(t *testing.T) {
	dir := t.TempDir()
	bin, cfg, dbPath, state, _ := e2eSetup(t, dir)
	e2eLogin(t, bin, cfg, dbPath, state)
	tg := fake.NewPersistent(state)
	ch, err := tg.CreateChannel(context.Background(), "Forum")
	if err != nil {
		t.Fatal(err)
	}
	peer := fmt.Sprint(ch.ID)
	roots := []string{filepath.Join(dir, "first"), filepath.Join(dir, "second"), filepath.Join(dir, "general")}
	for i, topic := range []int{10, 20, 1} {
		tg.SetForumTopic(ch.ID, telegram.ForumTopic{ID: topic, Title: fmt.Sprint(topic)})
		bound := runE2EJSON(t, bin, cfg, dbPath, state, "init", roots[i], "--bind-channel="+peer, "--topic", fmt.Sprint(topic))
		if bound["topic_id"] != float64(topic) {
			t.Fatalf("topic binding=%v", bound)
		}
		src := e2eLocalFile(t, dir, fmt.Sprintf("source%d.txt", i), fmt.Sprintf("topic %d bytes", topic))
		runE2EJSON(t, bin, cfg, dbPath, state, "--root", roots[i], "cp", src, "/same.txt")
		sibling := e2eLocalFile(t, dir, fmt.Sprintf("album%d.txt", i), fmt.Sprintf("album topic %d bytes", topic))
		runE2EJSON(t, bin, cfg, dbPath, state, "--root", roots[i], "cp", src, sibling, "/album/")
		// Re-open the persistent fake before changing the next topic: do not
		// overwrite messages emitted by the actual CLI process.
		tg = fake.NewPersistent(state)
	}
	runE2EExpectError(t, bin, cfg, dbPath, state, 2, "ERR_USAGE", "--channel", peer, "ls", "/")
	runE2EJSON(t, bin, cfg, dbPath, state, "--root", roots[0], "mv", "--confirm", "/same.txt", "/moved.txt")
	db, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Raw().Exec(`delete from files; delete from nodes; delete from scan_state`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for i, topic := range []int{10, 20, 1} {
		runE2EJSON(t, bin, cfg, dbPath, state, "--root", roots[i], "scan", "--full")
		remote := "/same.txt"
		if i == 0 {
			remote = "/moved.txt"
		}
		dest := filepath.Join(dir, fmt.Sprintf("download%d.txt", i))
		runE2EJSON(t, bin, cfg, dbPath, state, "--root", roots[i], "get", remote, dest)
		data, err := os.ReadFile(dest)
		if err != nil || string(data) != fmt.Sprintf("topic %d bytes", topic) {
			t.Fatalf("topic %d body=%q err=%v", topic, data, err)
		}
		albumDest := filepath.Join(dir, fmt.Sprintf("album-download%d.txt", i))
		runE2EJSON(t, bin, cfg, dbPath, state, "--root", roots[i], "get", fmt.Sprintf("/album/album%d.txt", i), albumDest)
		data, err = os.ReadFile(albumDest)
		if err != nil || string(data) != fmt.Sprintf("album topic %d bytes", topic) {
			t.Fatalf("album topic %d bytes=%q err=%v", topic, data, err)
		}
	}
	runE2EJSON(t, bin, cfg, dbPath, state, "--root", roots[0], "rm", "--confirm", "/moved.txt")
	dest := filepath.Join(dir, "after-delete.txt")
	runE2EJSON(t, bin, cfg, dbPath, state, "--root", roots[1], "get", "/same.txt", dest)
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "topic 20 bytes" {
		t.Fatalf("other topic after delete: %q %v", data, err)
	}
	tg = fake.NewPersistent(state)
	tg.SetForumTopic(ch.ID, telegram.ForumTopic{ID: 10, Title: "Closed", Closed: true})
	src := e2eLocalFile(t, dir, "closed.txt", "must not be uploaded")
	runE2EExpectError(t, bin, cfg, dbPath, state, 4, "ERR_CHANNEL_PERMISSION", "--root", roots[0], "cp", src, "/closed.txt")
	runE2EExpectError(t, bin, cfg, dbPath, state, 2, "ERR_USAGE", "init", filepath.Join(dir, "invalid"), "--bind-channel="+peer, "--topic=0")
}
