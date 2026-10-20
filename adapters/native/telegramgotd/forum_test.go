package telegramgotd

import (
	"context"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	port "github.com/thedavidweng/tg-drive/core/telegram"
)

type forumInvoker struct {
	forum, closed, hidden, deleted bool
	topic                          int
	pages                          int
}

func (f *forumInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	var result bin.Encoder
	switch req := input.(type) {
	case *tg.ChannelsGetChannelsRequest:
		result = &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Channel{ID: 100, Title: "Forum", Forum: f.forum, Photo: &tg.ChatPhotoEmpty{}}}}
	case *tg.MessagesGetForumTopicsByIDRequest:
		if req.Peer.(*tg.InputPeerChannel).ChannelID != 100 || len(req.Topics) != 1 || req.Topics[0] != 10 {
			return fmt.Errorf("wrong topic request: %+v", req)
		}
		topics := &tg.MessagesForumTopics{}
		if f.deleted {
			topics.Topics = []tg.ForumTopicClass{&tg.ForumTopicDeleted{ID: 10}}
		} else {
			topics.Topics = []tg.ForumTopicClass{&tg.ForumTopic{ID: 10, Title: "Topic", Peer: &tg.PeerChannel{ChannelID: 100}, FromID: &tg.PeerUser{UserID: 1}, Closed: f.closed, Hidden: f.hidden, NotifySettings: tg.PeerNotifySettings{}}}
		}
		result = topics
	case *tg.ChannelsGetMessagesRequest:
		result = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{forumMessage(req.ID[0].(*tg.InputMessageID).ID, f.topic)}}
	case *tg.MessagesGetHistoryRequest:
		f.pages++
		if f.pages == 1 {
			result = &tg.MessagesChannelMessages{Count: 4, Messages: []tg.MessageClass{forumMessage(104, 20), forumMessage(103, 10), &tg.MessageService{ID: 102, PeerID: &tg.PeerChannel{ChannelID: 100}, Action: &tg.MessageActionEmpty{}}}}
		} else {
			result = &tg.MessagesChannelMessages{Count: 4, Messages: []tg.MessageClass{forumMessage(101, 10)}}
		}
	default:
		return fmt.Errorf("unexpected request %T", input)
	}
	var buf bin.Buffer
	if err := result.Encode(&buf); err != nil {
		return err
	}
	return output.Decode(&buf)
}

func forumMessage(id, topic int) *tg.Message {
	m := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: "body"}
	if topic > 1 {
		header := &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: id - 1}
		header.SetReplyToTopID(topic)
		m.SetReplyTo(header)
	}
	return m
}

func TestForumCapabilityAndTargetMembership(t *testing.T) {
	peer := &tg.InputPeerChannel{ChannelID: 100, AccessHash: 1}
	ctx := port.WithForumTopic(context.Background(), 100, 10)
	for _, tc := range []struct {
		name string
		inv  forumInvoker
		want bool
	}{
		{"open", forumInvoker{forum: true, topic: 10}, true},
		{"closed", forumInvoker{forum: true, closed: true, topic: 10}, true},
		{"hidden", forumInvoker{forum: true, hidden: true, topic: 10}, true},
		{"deleted", forumInvoker{forum: true, deleted: true}, false},
		{"not forum", forumInvoker{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := tg.NewClient(&tc.inv)
			topic, err := resolveForumTopic(ctx, api, peer, 10)
			if tc.want {
				if err != nil || topic.Closed != tc.inv.closed || topic.Hidden != tc.inv.hidden {
					t.Fatalf("capability=%+v %v", topic, err)
				}
			} else if err == nil {
				t.Fatal("invalid topic accepted")
			}
		})
	}
	for _, actual := range []int{0, 10, 20} {
		err := checkTopicTarget(ctx, tg.NewClient(&forumInvoker{topic: actual}), peer, 42)
		if (err == nil) != (actual == 10) {
			t.Fatalf("target topic %d: %v", actual, err)
		}
	}
	if err := checkTopicMessage(port.WithForumTopic(context.Background(), 100, 1), 100, forumMessage(42, 0)); err != nil {
		t.Fatalf("General rejected: %v", err)
	}
	if err := checkTopicMessage(ctx, 200, forumMessage(42, 10)); err == nil {
		t.Fatal("different peer accepted")
	}
}

func TestForumFilteringRetainsRawPaginationProof(t *testing.T) {
	inv := &forumInvoker{}
	c := New(1, "hash", "", false, 0)
	var ids []int
	meta, err := c.paginateHistory(context.Background(), tg.NewClient(inv), &tg.InputPeerChannel{ChannelID: 100}, 0, 0, func(m *tg.Message) error {
		converted := messageFromTG(m)
		if port.InForumTopic(converted, 10) {
			ids = append(ids, m.ID)
		}
		return nil
	})
	if err != nil || !meta.Complete || inv.pages != 2 || len(ids) != 2 || ids[0] != 103 || ids[1] != 101 {
		t.Fatalf("topic pagination=%+v ids=%v pages=%d err=%v", meta, ids, inv.pages, err)
	}
}

func TestForumReplyProtocol(t *testing.T) {
	for _, tc := range []struct{ topic, target, reply, top int }{{1, 0, 0, 0}, {10, 0, 10, 0}, {10, 10, 10, 0}, {10, 42, 42, 10}, {1, 42, 42, 0}, {0, 42, 42, 0}} {
		reply := topicReply(tc.topic, tc.target)
		if tc.reply == 0 {
			if reply != nil {
				t.Fatalf("General should not create a reply: %+v", reply)
			}
			continue
		}
		top, _ := reply.GetTopMsgID()
		if reply.ReplyToMsgID != tc.reply || top != tc.top {
			t.Fatalf("topic=%d target=%d request=%+v", tc.topic, tc.target, reply)
		}
	}
	_, err := resolveForumTopic(context.Background(), tg.NewClient(&forumInvoker{}), &tg.InputPeerChannel{}, 0)
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrUsage {
		t.Fatalf("invalid ID: %v", err)
	}
}
