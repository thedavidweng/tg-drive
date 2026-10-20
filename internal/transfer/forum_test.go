package transfer_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/internal/service"
	"github.com/thedavidweng/tg-drive/internal/transfer"
)

func TestForumTransferRetryKeepsTopicAcrossSelectionSwitch(t *testing.T) {
	a, tg := newApp(t)
	ctx := context.Background()
	forum, err := tg.CreateChannel(ctx, "Forum")
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{t.TempDir(), t.TempDir()}
	for i, id := range []int{10, 20} {
		tg.SetForumTopic(forum.ID, telegram.ForumTopic{ID: id, Title: fmt.Sprint(id)})
		if _, err := a.InitForumRoot(ctx, roots[i], fmt.Sprint(forum.ID), id); err != nil {
			t.Fatal(err)
		}
	}
	m := transfer.New(a, transfer.Options{FrontEnd: transfer.FrontEndCLI})
	tg.SetFailUpload(true)
	h, err := m.SubmitUpload(service.WithLocalRoot(ctx, roots[0]), transfer.Upload{Source: localFile(t, "retry.bin", 100), Dest: "/retry.bin", Policy: service.ConflictFail})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Wait(); err == nil {
		t.Fatal("injected upload failure did not fire")
	}
	tg.SetFailUpload(false)
	r, err := m.Retry(service.WithLocalRoot(ctx, roots[1]), h.ID(), service.Observer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Wait(); err != nil {
		t.Fatal(err)
	}
	for i, root := range roots {
		files, err := a.ListDir(service.WithLocalRoot(ctx, root), "/")
		if err != nil || len(files) != 1-i {
			t.Fatalf("root %d files=%v err=%v", i, files, err)
		}
	}

	// Durable work cannot be redirected by reusing its old root for another topic.
	tg.SetFailUpload(true)
	h, err = m.SubmitUpload(service.WithLocalRoot(ctx, roots[0]), transfer.Upload{Source: localFile(t, "refuse.bin", 100), Dest: "/refuse.bin", Policy: service.ConflictFail})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Wait(); err == nil {
		t.Fatal("injected failure did not fire")
	}
	tg.SetFailUpload(false)
	if _, err := a.DB.Raw().Exec(`update channels set root_local_path=? where topic_id=10`, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Raw().Exec(`update channels set root_local_path=? where topic_id=20`, roots[0]); err != nil {
		t.Fatal(err)
	}
	before := len(tg.Messages(forum.ID))
	if _, err := m.Retry(ctx, h.ID(), service.Observer{}); err == nil {
		t.Fatal("rebound root accepted")
	}
	if len(tg.Messages(forum.ID)) != before {
		t.Fatal("rejected retry wrote Telegram messages")
	}
	record, err := m.Get(ctx, h.ID())
	if err != nil || record.Stage != transfer.StageFailed {
		t.Fatalf("rejected retry claimed transfer: %+v %v", record, err)
	}
}
