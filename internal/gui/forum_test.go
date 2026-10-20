//go:build gui

package gui_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/core/telegram/fake"
	"github.com/thedavidweng/tg-drive/internal/service"
)

func TestForumGUISelectsUniqueRootNotSharedPeer(t *testing.T) {
	seedLogin(t)
	ctx := context.Background()
	a, closeApp, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	tg := fake.NewPersistent(os.Getenv("TD_FAKE_TELEGRAM_STATE"))
	a.TG = tg
	peer, err := tg.CreateChannel(ctx, "Forum")
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{t.TempDir(), t.TempDir()}
	for i, id := range []int{10, 20} {
		tg.SetForumTopic(peer.ID, telegram.ForumTopic{ID: id, Title: fmt.Sprint(id)})
		if _, err := a.InitForumRoot(ctx, roots[i], fmt.Sprint(peer.ID), id); err != nil {
			t.Fatal(err)
		}
	}
	closeApp()
	svc := openGUI(t)
	list, err := svc.Channels.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if !list[0].Active || list[1].Active || list[0].Selector == list[1].Selector {
		t.Fatalf("ambiguous GUI selection: %+v", list)
	}
	st, err := svc.Channels.Select(ctx, list[1].Selector)
	if err != nil || st.LocalRoot != roots[1] {
		t.Fatalf("selected=%+v err=%v", st, err)
	}
	list, err = svc.Channels.List(ctx)
	if err != nil || list[0].Active || !list[1].Active {
		t.Fatalf("selection not retained: %+v %v", list, err)
	}
	if _, err := svc.Channels.Select(ctx, fmt.Sprint(peer.ID)); err == nil {
		t.Fatal("ambiguous peer accepted")
	}
	st, err = svc.Channels.Status(ctx)
	if err != nil || st.LocalRoot != roots[1] {
		t.Fatalf("rejected selection changed root: %+v %v", st, err)
	}
	if _, err := svc.Channels.LinkDiscussion(ctx); err == nil {
		t.Fatal("forum linked a broadcast discussion")
	}
}
