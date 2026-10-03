package telegramgotd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	tgtelegram "github.com/thedavidweng/tg-drive/core/telegram"
)

// The fake Telegram client used by the E2E suite replaces the whole adapter,
// so whether a real media download stops on cancellation (gotd's downloader
// plus the rate-limit middleware) is pinned here.

// chunkInvoker serves the first upload.getFile request with a full chunk.
// The next request stays in flight, as on a slow connection: it reports
// itself on inFlight, then behaves like gotd's RPC engine (rpc.Engine.Do),
// which returns the context's error once the caller cancels.
type chunkInvoker struct {
	served   int
	inFlight chan struct{}
}

func (c *chunkInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	req, ok := input.(*tg.UploadGetFileRequest)
	if !ok {
		return errors.New("unexpected request")
	}
	if c.served == 0 {
		c.served++
		var buf bin.Buffer
		if err := (&tg.UploadFile{Type: &tg.StorageFilePartial{}, Bytes: make([]byte, req.Limit)}).Encode(&buf); err != nil {
			return err
		}
		return output.Decode(&buf)
	}
	close(c.inFlight)
	<-ctx.Done()
	return ctx.Err()
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestMediaDownloadStopsOnCancel(t *testing.T) {
	inv := &chunkInvoker{inFlight: make(chan struct{})}
	api := tg.NewClient(testLimiter().invoke(inv))
	msg := &tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, Size: 64 * 1024 * 1024}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- streamMessageMedia(ctx, api, msg, discard{}) }()
	select {
	case <-inv.inFlight:
	case err := <-done:
		t.Fatalf("download ended before its second chunk request: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if !apperr.IsCancelled(tgtelegram.MapError(err)) {
			t.Fatalf("mapped err = %v, want ERR_CANCELLED", tgtelegram.MapError(err))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("download kept running after its context was cancelled")
	}
}
