package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/manifest"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

type forumCapabilityClient struct {
	telegram.Client
	missing string
}

func (c forumCapabilityClient) Doctor(ctx context.Context, peer int64) (*telegram.Capabilities, error) {
	caps, err := c.Client.Doctor(ctx, peer)
	if err != nil {
		return nil, err
	}
	switch c.missing {
	case "upload":
		caps.UploadOK = false
	case "edit":
		caps.EditOldCaptionOK = false
	case "delete":
		caps.DeleteOK = false
	}
	return caps, nil
}

func (c forumCapabilityClient) History(ctx context.Context, peer int64, after, limit int) ([]telegram.Message, error) {
	if c.missing == "history" {
		return nil, &telegram.PermissionDeniedError{}
	}
	return c.Client.History(ctx, peer, after, limit)
}

func TestForumBindingRefusesMissingCapabilitiesWithoutPersistence(t *testing.T) {
	for _, missing := range []string{"upload", "edit", "delete", "history", "hidden", "closed", "missing"} {
		t.Run(missing, func(t *testing.T) {
			a, tg := testApp(t)
			loginAndInit(t, a, tg)
			ctx := context.Background()
			peer, err := tg.CreateChannel(ctx, "Forum")
			if err != nil {
				t.Fatal(err)
			}
			if missing != "missing" {
				tg.SetForumTopic(peer.ID, telegram.ForumTopic{ID: 10, Title: "Topic", Hidden: missing == "hidden", Closed: missing == "closed"})
			}
			a.TG = forumCapabilityClient{Client: tg, missing: missing}
			if _, err := a.InitForumRoot(ctx, t.TempDir(), fmt.Sprint(peer.ID), 10); err == nil {
				t.Fatal("unsupported forum accepted")
			}
			var rows int
			if err := a.DB.Raw().QueryRow(`select count(*) from channels where tg_channel_id=?`, fmt.Sprint(peer.ID)).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("failed binding persisted: rows=%d err=%v", rows, err)
			}
			if len(tg.Messages(peer.ID)) != 0 {
				t.Fatal("capability refusal wrote Telegram messages")
			}
		})
	}
}

func TestForumRootsConsumeOperationsAndReconstructInIsolation(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	forum, err := tg.CreateChannel(ctx, "Forum")
	if err != nil {
		t.Fatal(err)
	}
	tg.SetForumTopic(forum.ID, telegram.ForumTopic{ID: 10, Title: "First"})
	tg.SetForumTopic(forum.ID, telegram.ForumTopic{ID: 20, Title: "Second"})
	first, err := a.InitForumRoot(ctx, t.TempDir(), fmt.Sprint(forum.ID), 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.InitForumRoot(ctx, t.TempDir(), fmt.Sprint(forum.ID), 20)
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(local, []byte("topic bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, root := range []string{first.LocalRoot, second.LocalRoot} {
		if err := os.WriteFile(local, []byte(fmt.Sprintf("topic %d bytes", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.UploadFile(WithLocalRoot(ctx, root), local, "/nested/data.txt", ConflictFail, false, UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	selected := WithLocalRoot(ctx, first.LocalRoot)
	if err := a.MoveFile(selected, "/nested", "/moved", MoveOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{first.LocalRoot, second.LocalRoot} {
		if _, err := a.Scan(WithLocalRoot(ctx, root), ScanOptions{Full: true}); err != nil {
			t.Fatal(err)
		}
	}
	for i, tc := range []struct{ root, path string }{{first.LocalRoot, "/moved/data.txt"}, {second.LocalRoot, "/nested/data.txt"}} {
		f, err := a.ResolvePreview(WithLocalRoot(ctx, tc.root), tc.path)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := a.StreamPreview(ctx, f, &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != fmt.Sprintf("topic %d bytes", i) {
			t.Fatalf("wrong topic bytes: %q", out.String())
		}
	}
	if _, err := a.channel(WithChannel(ctx, fmt.Sprint(forum.ID))); err == nil {
		t.Fatal("peer ID silently selected one of two topics")
	}
	before := len(tg.Messages(forum.ID))
	tg.SetForumTopic(forum.ID, telegram.ForumTopic{ID: 10, Title: "First", Closed: true})
	_, err = a.UploadFile(selected, local, "/closed.txt", ConflictFail, false, UploadOptions{})
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrChannelPermission {
		t.Fatalf("closed topic: %v", err)
	}
	if len(tg.Messages(forum.ID)) != before {
		t.Fatal("closed topic wrote remote messages")
	}
}

func TestForumScanRejectsCrossTopicManifestReferences(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	forum, err := tg.CreateChannel(ctx, "Forum")
	if err != nil {
		t.Fatal(err)
	}
	tg.SetForumTopic(forum.ID, telegram.ForumTopic{ID: 10, Title: "First"})
	tg.SetForumTopic(forum.ID, telegram.ForumTopic{ID: 20, Title: "Second"})
	other := tg.AddMessage(forum.ID, telegram.Message{TopicID: 20, Kind: telegram.KindDocument, Data: []byte("private")})
	target := other.ID
	tg.AddMessage(forum.ID, telegram.Message{TopicID: 10, ReplyTo: &target, Text: manifest.RenderManifestReply(manifest.FileMeta{CanonicalPath: "/stolen.txt", DisplayName: "stolen.txt", Size: 7})})
	r, err := a.InitForumRoot(ctx, t.TempDir(), fmt.Sprint(forum.ID), 10)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := a.ListDir(WithLocalRoot(ctx, r.LocalRoot), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cross-topic file indexed: %+v", entries)
	}
}

func TestForumDirectoryPermissionLossRetainsHistoryAndOtherTopic(t *testing.T) {
	for _, operation := range []string{"move", "delete"} {
		t.Run(operation, func(t *testing.T) {
			a, tg := testApp(t)
			loginAndInit(t, a, tg)
			ctx := context.Background()
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
			selected, other := WithLocalRoot(ctx, roots[0]), WithLocalRoot(ctx, roots[1])
			var second int
			for i, path := range []string{"/batch/a", "/batch/b", "/batch/c"} {
				r, err := a.UploadFile(selected, writeLocal(t, path), path, ConflictFail, false, UploadOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if i == 1 {
					second = r.MessageID
				}
				if _, err := a.UploadFile(other, writeLocal(t, "other "+path), path, ConflictFail, false, UploadOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			a.TG = &revokeDirectoryPermission{Client: tg, messageID: second, revoke: func() { tg.SetDenyPermissions(true) }}
			run := func() error {
				if operation == "move" {
					return a.MoveFile(selected, "/batch", "/moved", MoveOptions{Confirm: true})
				}
				_, err := a.DeleteFile(selected, "/batch", DeleteOptions{Confirm: true})
				return err
			}
			err = run()
			ae, ok := apperr.As(err)
			if !ok || ae.Code != apperr.ErrChannelPermission || ae.Details["remaining"] != 2 {
				t.Fatalf("permission loss: %v", err)
			}
			operationID := ae.Details["operation_id"].(string)
			a.TG = tg
			tg.SetDenyPermissions(false)
			if err := run(); err != nil {
				t.Fatal(err)
			}
			for i, root := range roots {
				scan, err := a.Scan(WithLocalRoot(ctx, root), ScanOptions{Full: true})
				want := 3
				if i == 0 && operation == "delete" {
					want = 0
				}
				if err != nil || scan.Active != want {
					t.Fatalf("reconstruct root %d: %+v %v", i, scan, err)
				}
			}
			table := "directory_delete_operations"
			if operation == "move" {
				table = "directory_move_operations"
			}
			var status string
			if err := a.DB.Raw().QueryRow(`select status from `+table+` where id=?`, operationID).Scan(&status); err != nil || status != "failed" {
				t.Fatalf("original history changed: %q %v", status, err)
			}
			var body bytes.Buffer
			preview, err := a.ResolvePreview(other, "/batch/b")
			if err != nil {
				t.Fatal(err)
			}
			if err := a.StreamPreview(ctx, preview, &body); err != nil || body.String() != "other /batch/b" {
				t.Fatalf("other topic body=%q %v", body.String(), err)
			}
		})
	}
}

func TestForumDedupeMutationCannotRetireAnotherTopic(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
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
		for _, path := range []string{"/a", "/b"} {
			if _, err := a.UploadFile(WithLocalRoot(ctx, roots[i]), writeLocal(t, "same bytes"), path, ConflictFail, false, UploadOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := a.DeleteDuplicates(WithLocalRoot(ctx, roots[0]), "/", DedupeDeleteOptions{Confirm: true})
	if err != nil || r.Deleted != 1 {
		t.Fatalf("cleanup=%+v %v", r, err)
	}
	for i, root := range roots {
		scan, err := a.Scan(WithLocalRoot(ctx, root), ScanOptions{Full: true})
		if err != nil || scan.Active != i+1 {
			t.Fatalf("root %d: %+v %v", i, scan, err)
		}
	}
}
