package fake

import (
	"context"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// SetForumTopic seeds an existing forum topic or changes its capabilities.
func (c *Client) SetForumTopic(peerID int64, topic telegram.ForumTopic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channels[peerID].Forum = true
	if c.forumTopics[peerID] == nil {
		c.forumTopics[peerID] = map[int]telegram.ForumTopic{}
	}
	c.forumTopics[peerID][topic.ID] = topic
	c.save()
}

func (c *Client) ResolveForumTopic(ctx context.Context, peerID int64, topicID int) (*telegram.ForumTopic, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	topic, ok := c.forumTopics[peerID][topicID]
	if !ok {
		return nil, apperr.New(apperr.ErrChannelNotFound, "forum topic not found or deleted")
	}
	return &topic, nil
}

// Callers hold mu; reads and mutations share the same membership rule.
func (c *Client) topicTarget(ctx context.Context, peerID int64, messageID int) error {
	topic, err := telegram.ForumTopicID(ctx, peerID)
	if err != nil || topic == 0 {
		return err
	}
	for _, msg := range c.messages[peerID] {
		if msg.ID == messageID && telegram.InForumTopic(msg, topic) {
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) writableTopic(ctx context.Context, peerID int64) (int, error) {
	topicID, err := telegram.ForumTopicID(ctx, peerID)
	if err != nil || topicID == 0 {
		return topicID, err
	}
	topic, ok := c.forumTopics[peerID][topicID]
	if !ok {
		return 0, apperr.New(apperr.ErrChannelNotFound, "forum topic not found or deleted")
	}
	if topic.Closed || topic.Hidden {
		return 0, &telegram.PermissionDeniedError{}
	}
	return topicID, nil
}
