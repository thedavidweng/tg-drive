package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/core/telegram/fake"
)

// writeAudit is the Telegram client every service test runs against: the
// fake, plus a check that each remote write happens inside an operation
// whose operation locks are live in the database. A write outside one fails
// the test that made it, so a use case cannot drift out of the
// lock-per-remote-write rule unnoticed.
type writeAudit struct {
	*fake.Client
	db *sqlitestore.DB

	mu         sync.Mutex
	violations []string
}

func auditWrites(t *testing.T, app *App, tg *fake.Client) {
	t.Helper()
	w := &writeAudit{Client: tg, db: app.DB}
	app.TG = w
	t.Cleanup(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		for _, v := range w.violations {
			t.Errorf("remote write %s", v)
		}
	})
}

func (w *writeAudit) check(ctx context.Context, write string) {
	violation := ""
	if op := operationFrom(ctx); op == nil {
		violation = write + " outside an operation"
	} else {
		for key := range op.held {
			// A background context: an aborting operation's cancelled
			// context must not hide a lock it lost.
			if held, err := w.db.LockHeld(context.Background(), key); err != nil || !held {
				violation = fmt.Sprintf("%s without its operation lock %q", write, key)
				break
			}
		}
	}
	if violation == "" {
		return
	}
	w.mu.Lock()
	w.violations = append(w.violations, violation)
	w.mu.Unlock()
}

func (w *writeAudit) UploadMedia(ctx context.Context, req telegram.UploadRequest) (*telegram.UploadResult, error) {
	w.check(ctx, "UploadMedia")
	return w.Client.UploadMedia(ctx, req)
}

func (w *writeAudit) UploadMediaGroup(ctx context.Context, reqs []telegram.UploadRequest) ([]telegram.UploadResult, error) {
	w.check(ctx, "UploadMediaGroup")
	return w.Client.UploadMediaGroup(ctx, reqs)
}

func (w *writeAudit) SendTextReply(ctx context.Context, channelID int64, replyTo int, text string) (int, error) {
	w.check(ctx, "SendTextReply")
	return w.Client.SendTextReply(ctx, channelID, replyTo, text)
}

func (w *writeAudit) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	w.check(ctx, fmt.Sprintf("EditCaption(%d)", messageID))
	return w.Client.EditCaption(ctx, channelID, messageID, caption)
}

func (w *writeAudit) EditText(ctx context.Context, channelID int64, messageID int, text string) error {
	w.check(ctx, fmt.Sprintf("EditText(%d)", messageID))
	return w.Client.EditText(ctx, channelID, messageID, text)
}

func (w *writeAudit) DeleteMessage(ctx context.Context, channelID int64, messageID int) error {
	w.check(ctx, fmt.Sprintf("DeleteMessage(%d)", messageID))
	return w.Client.DeleteMessage(ctx, channelID, messageID)
}

func (w *writeAudit) SendThreadReply(ctx context.Context, channelID int64, postMsgID int, text string) (int, error) {
	w.check(ctx, "SendThreadReply")
	return w.Client.SendThreadReply(ctx, channelID, postMsgID, text)
}

func (w *writeAudit) EditThreadMessage(ctx context.Context, channelID int64, msgID int, text string) error {
	w.check(ctx, fmt.Sprintf("EditThreadMessage(%d)", msgID))
	return w.Client.EditThreadMessage(ctx, channelID, msgID, text)
}

func (w *writeAudit) DeleteThreadMessage(ctx context.Context, channelID int64, msgID int) error {
	w.check(ctx, fmt.Sprintf("DeleteThreadMessage(%d)", msgID))
	return w.Client.DeleteThreadMessage(ctx, channelID, msgID)
}

func (w *writeAudit) DeleteSavedMessage(ctx context.Context, messageID int) error {
	w.check(ctx, fmt.Sprintf("DeleteSavedMessage(%d)", messageID))
	return w.Client.DeleteSavedMessage(ctx, messageID)
}

// channelID and tgChannelID read the bound channel for test setup and
// assertions.
func (a *App) channelID(ctx context.Context) (int64, string, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return 0, "", err
	}
	return ch.rowID, ch.tgIDStr, nil
}

func (a *App) tgChannelID(ctx context.Context) (int64, error) {
	ch, err := a.channel(ctx)
	if err != nil {
		return 0, err
	}
	return ch.tgID, nil
}
