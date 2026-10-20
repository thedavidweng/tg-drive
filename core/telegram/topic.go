package telegram

import (
	"context"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

type topicRoute struct {
	peerID  int64
	topicID int
}

// WithForumTopic pins Telegram calls to an existing peer/topic pair. A route
// belongs to one operation; clients never retain it as shared mutable state.
func WithForumTopic(ctx context.Context, peerID int64, topicID int) context.Context {
	return context.WithValue(ctx, topicRoute{}, topicRoute{peerID, topicID})
}

// ForumTopicID rejects accidental reuse of a topic route on another peer.
func ForumTopicID(ctx context.Context, peerID int64) (int, error) {
	route, ok := ctx.Value(topicRoute{}).(topicRoute)
	if !ok || route.topicID == 0 {
		return 0, nil
	}
	if route.peerID != peerID || route.topicID < 0 {
		return 0, apperr.New(apperr.ErrUsage, "forum route does not match the Telegram peer")
	}
	return route.topicID, nil
}

// InForumTopic normalizes Telegram's General topic: its messages do not carry
// the forum_topic reply flag, unlike messages in every other topic.
func InForumTopic(msg Message, topicID int) bool {
	return topicID == 0 || msg.TopicID == topicID || (topicID == 1 && msg.TopicID == 0)
}
