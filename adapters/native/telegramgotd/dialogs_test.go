package telegramgotd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

type dialogPageInvoker struct {
	pages []tg.MessagesDialogsClass
	calls int
	err   error
}

func (d *dialogPageInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	if _, ok := input.(*tg.MessagesGetDialogsRequest); !ok {
		return fmt.Errorf("unexpected request %T", input)
	}
	d.calls++
	if d.calls > len(d.pages) {
		return d.err
	}
	var buf bin.Buffer
	if err := d.pages[d.calls-1].Encode(&buf); err != nil {
		return err
	}
	return output.Decode(&buf)
}

func dialogPage(channels ...*tg.Channel) *tg.MessagesDialogsSlice {
	page := &tg.MessagesDialogsSlice{Count: 10}
	for _, ch := range channels {
		ch.SetAccessHash(ch.ID * 10)
		ch.Photo = &tg.ChatPhotoEmpty{}
		page.Chats = append(page.Chats, ch)
		page.Dialogs = append(page.Dialogs, &tg.Dialog{
			Peer: &tg.PeerChannel{ChannelID: ch.ID},
		})
	}
	return page
}

func TestResolveChannelPeerRequiresUniqueRemoteIdentity(t *testing.T) {
	for _, seedCache := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", seedCache), func(t *testing.T) {
			c := New(1, "hash", "", false, 0)
			if seedCache {
				c.RegisterChannelInfo(1, 10, "Same")
			}
			inv := &dialogPageInvoker{pages: []tg.MessagesDialogsClass{
				dialogPage(&tg.Channel{ID: 1, Title: "Same", Broadcast: true}),
				dialogPage(&tg.Channel{ID: 2, Title: "Same [TD]", Broadcast: true}),
				&tg.MessagesDialogs{},
			}}
			peer, err := c.resolveChannelPeer(context.Background(), tg.NewClient(inv), "Same")
			ae, ok := apperr.As(err)
			if peer != nil || !ok || ae.Code != apperr.ErrUsage {
				t.Fatalf("ambiguous title = %v, %v; want ERR_USAGE without a peer", peer, err)
			}
		})
	}
}

func TestResolveChannelPeerIDWinsOverNumericTitle(t *testing.T) {
	c := New(1, "hash", "", false, 0)
	inv := &dialogPageInvoker{pages: []tg.MessagesDialogsClass{
		dialogPage(&tg.Channel{ID: 1, Title: "2", Broadcast: true}),
		dialogPage(&tg.Channel{ID: 2, Title: "Other", Broadcast: true}),
		&tg.MessagesDialogs{},
	}}
	peer, err := c.resolveChannelPeer(context.Background(), tg.NewClient(inv), "2")
	if err != nil || peer.ChannelID != 2 {
		t.Fatalf("explicit ID = %v, %v; want channel 2", peer, err)
	}
}

func TestResolveChannelPeerRefusesIncompleteTitleLookup(t *testing.T) {
	c := New(1, "hash", "", false, 0)
	inv := &dialogPageInvoker{
		pages: []tg.MessagesDialogsClass{dialogPage(&tg.Channel{ID: 1, Title: "Same", Broadcast: true})},
		err:   errors.New("dialog connection lost"),
	}
	peer, err := c.resolveChannelPeer(context.Background(), tg.NewClient(inv), "Same")
	if peer != nil || err == nil {
		t.Fatalf("incomplete title lookup = %v, %v; want failure without a peer", peer, err)
	}
}

func TestResolveChannelPeerUniqueAndCachedIDs(t *testing.T) {
	for _, selector := range []string{"Same", "2", "-1002", "999"} {
		t.Run(selector, func(t *testing.T) {
			c := New(1, "hash", "", false, 0)
			inv := &dialogPageInvoker{pages: []tg.MessagesDialogsClass{
				dialogPage(&tg.Channel{ID: 2, Title: "Same [TD]", Broadcast: true},
					&tg.Channel{ID: 3, Title: "999", Broadcast: true}),
				&tg.MessagesDialogs{},
			}}
			peer, err := c.resolveChannelPeer(context.Background(), tg.NewClient(inv), selector)
			want := int64(2)
			if selector == "999" {
				want = 3
			}
			if err != nil || peer.ChannelID != want {
				t.Fatalf("selector %q = %v, %v; want %d", selector, peer, err, want)
			}
			cached := &dialogPageInvoker{err: errors.New("unexpected dialog walk")}
			peer, err = c.resolveChannelPeer(context.Background(), tg.NewClient(cached), fmt.Sprint(want))
			if err != nil || peer.ChannelID != want || cached.calls != 0 {
				t.Fatalf("cached ID = %v, %v; dialog calls=%d", peer, err, cached.calls)
			}
		})
	}
}

func TestResolveChannelPeerNotFound(t *testing.T) {
	c := New(1, "hash", "", false, 0)
	// A stale display title is not evidence that a remote channel still has it.
	c.RegisterChannelInfo(1, 10, "Old")
	inv := &dialogPageInvoker{pages: []tg.MessagesDialogsClass{
		dialogPage(&tg.Channel{ID: 1, Title: "Renamed", Broadcast: true}),
		&tg.MessagesDialogs{},
	}}
	peer, err := c.resolveChannelPeer(context.Background(), tg.NewClient(inv), "Old")
	ae, ok := apperr.As(err)
	if peer != nil || !ok || ae.Code != apperr.ErrChannelNotFound {
		t.Fatalf("absent channel = %v, %v; want ERR_CHANNEL_NOT_FOUND", peer, err)
	}
}
