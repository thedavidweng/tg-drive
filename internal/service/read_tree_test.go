package service

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"testing"

	"github.com/thedavidweng/tg-drive/core/telegram"
)

func TestReadTreePinsForumAndBoundsSubtree(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	peer, err := tg.CreateChannel(ctx, "Forum")
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{t.TempDir(), t.TempDir()}
	for i, root := range roots {
		topic := 10 + i
		tg.SetForumTopic(peer.ID, telegram.ForumTopic{ID: topic, Title: fmt.Sprint(topic)})
		if _, err := a.InitForumRoot(ctx, root, fmt.Sprint(peer.ID), topic); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"/safe/nested/file.txt", "/outside.txt"} {
			if _, err := a.UploadFile(WithLocalRoot(ctx, root), writeLocal(t, fmt.Sprint(topic)), name, ConflictFail, false, UploadOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	tree, err := a.OpenReadTree(WithLocalRoot(ctx, roots[0]), "/safe")
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated active selector cannot retarget the pinned tree.
	a.Channel = "missing"
	data, err := fs.ReadFile(tree, "nested/file.txt")
	if err != nil || string(data) != "10" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	for _, name := range []string{"../outside.txt", "/outside.txt", "nested/../../outside.txt", "outside.txt"} {
		if _, err := tree.Open(name); err == nil {
			t.Fatalf("escaped subtree: %q", name)
		}
	}
	entries, err := fs.ReadDir(tree, ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "nested" {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	file, err := tree.Open("nested/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.(io.Seeker).Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(file)
	if err != nil || string(data) != "0" {
		t.Fatalf("range=%q err=%v", data, err)
	}
	if _, err := a.DB.Raw().Exec(`update channels set root_local_path='/rebound' where root_local_path=?`, roots[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Open("nested/file.txt"); err == nil {
		t.Fatal("rebound root accepted")
	}
}

func TestReadTreeCancellationAndMissingRoot(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	if _, err := a.OpenReadTree(ctx, "/missing"); err == nil {
		t.Fatal("missing subtree accepted")
	}
	tree, err := a.OpenReadTree(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tree.WithContext(cancelled).Open("."); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestReadTreePreservesLifetimeCancellationAcrossRequestContexts(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.UploadFile(lifetime, writeLocal(t, "contents"), "/file.txt", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	tree, err := a.OpenReadTree(lifetime, "/")
	if err != nil {
		t.Fatal(err)
	}
	requestTree := tree.WithContext(context.Background())
	file, err := requestTree.Open("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	cancel()
	if _, err := requestTree.Open("file.txt"); err == nil {
		t.Fatal("new request escaped cancelled mount lifetime")
	}
	if _, err := file.Read(make([]byte, 1)); err == nil {
		t.Fatal("open file escaped cancelled mount lifetime")
	}
}
