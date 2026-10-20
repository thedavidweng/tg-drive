package telegramgotd

import (
	"context"
	"strconv"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	tgtelegram "github.com/thedavidweng/tg-drive/core/telegram"
)

func (c *Client) ResolveForumTopic(ctx context.Context, channelID int64, topicID int) (*tgtelegram.ForumTopic, error) {
	var out *tgtelegram.ForumTopic
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		out, err = resolveForumTopic(ctx, api, peer, topicID)
		return err
	})
	return out, err
}

func resolveForumTopic(ctx context.Context, api *tg.Client, peer *tg.InputPeerChannel, topicID int) (*tgtelegram.ForumTopic, error) {
	if topicID <= 0 {
		return nil, apperr.New(apperr.ErrUsage, "topic ID must be positive")
	}
	chats, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash}})
	if err != nil {
		return nil, mapRPCError(err)
	}
	ch, err := firstChannel(chats)
	if err != nil {
		return nil, err
	}
	if !ch.Forum {
		return nil, apperr.New(apperr.ErrUsage, "Telegram peer is not a forum")
	}
	topics, err := api.MessagesGetForumTopicsByID(ctx, &tg.MessagesGetForumTopicsByIDRequest{Peer: peer, Topics: []int{topicID}})
	if err != nil {
		return nil, mapRPCError(err)
	}
	for _, item := range topics.Topics {
		if topic, ok := item.(*tg.ForumTopic); ok && topic.ID == topicID {
			return &tgtelegram.ForumTopic{ID: topic.ID, Title: topic.Title, Closed: topic.Closed, Hidden: topic.Hidden}, nil
		}
	}
	return nil, apperr.New(apperr.ErrChannelNotFound, "forum topic not found or deleted")
}

func checkTopicMessage(ctx context.Context, peerID int64, msg *tg.Message) error {
	topic, err := tgtelegram.ForumTopicID(ctx, peerID)
	if err != nil {
		return err
	}
	if !tgtelegram.InForumTopic(messageFromTG(msg), topic) {
		return &tgtelegram.MessageNotFoundError{}
	}
	return nil
}

func checkTopicTarget(ctx context.Context, api *tg.Client, peer *tg.InputPeerChannel, messageID int) error {
	topic, err := tgtelegram.ForumTopicID(ctx, peer.ChannelID)
	if err != nil || topic == 0 {
		return err
	}
	msgs, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return mapRPCError(err)
	}
	msg, err := firstMessage(msgs)
	if err != nil {
		return &tgtelegram.MessageNotFoundError{}
	}
	return checkTopicMessage(ctx, peer.ChannelID, msg)
}

func topicReply(topicID, replyTo int) *tg.InputReplyToMessage {
	if replyTo == 0 {
		if topicID <= 1 {
			return nil
		}
		return &tg.InputReplyToMessage{ReplyToMsgID: topicID}
	}
	reply := &tg.InputReplyToMessage{ReplyToMsgID: replyTo}
	if topicID > 1 && topicID != replyTo {
		reply.SetTopMsgID(topicID)
	}
	return reply
}
