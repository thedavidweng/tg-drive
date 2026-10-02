package telegramgotd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// The middleware sits below every RPC and the fake Telegram client used by
// the E2E suite never reaches it, so its retry policy is pinned here.

type scriptedInvoker struct {
	errs  []error // returned in order; nil once exhausted
	calls int
}

func (s *scriptedInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	s.calls++
	if len(s.errs) == 0 {
		return nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	return err
}

func testLimiter() *RateLimiter {
	rl := NewRateLimiter(false, time.Second)
	rl.transientBase = time.Millisecond
	return rl
}

func invokeVia(rl *RateLimiter, ctx context.Context, inv *scriptedInvoker, input bin.Encoder) error {
	return rl.invoke(inv)(ctx, input, nil)
}

func TestTransientErrorRetriedForIdempotentRead(t *testing.T) {
	inv := &scriptedInvoker{errs: []error{tgerr.New(500, "INTERNAL_SERVER_ERROR"), tgerr.New(-503, "Timeout")}}
	if err := invokeVia(testLimiter(), context.Background(), inv, &tg.MessagesGetHistoryRequest{}); err != nil {
		t.Fatalf("err = %v, want success after retries", err)
	}
	if inv.calls != 3 {
		t.Fatalf("calls = %d, want 3", inv.calls)
	}
}

func TestTransientErrorNotRetriedForMutation(t *testing.T) {
	// A send that failed with a server error may still have been delivered;
	// retrying would risk a duplicate post in the drive channel.
	inv := &scriptedInvoker{errs: []error{tgerr.New(500, "INTERNAL_SERVER_ERROR")}}
	err := invokeVia(testLimiter(), context.Background(), inv, &tg.MessagesSendMediaRequest{})
	if err == nil || inv.calls != 1 {
		t.Fatalf("err = %v calls = %d, want the error after one call", err, inv.calls)
	}
}

func TestPermanentErrorNotRetried(t *testing.T) {
	inv := &scriptedInvoker{errs: []error{tgerr.New(400, "CHANNEL_INVALID")}}
	err := invokeVia(testLimiter(), context.Background(), inv, &tg.MessagesGetHistoryRequest{})
	if !tgerr.Is(err, "CHANNEL_INVALID") || inv.calls != 1 {
		t.Fatalf("err = %v calls = %d, want CHANNEL_INVALID after one call", err, inv.calls)
	}
}

func TestTransientRetriesAreBounded(t *testing.T) {
	errs := make([]error, 50)
	for i := range errs {
		errs[i] = tgerr.New(500, "INTERNAL_SERVER_ERROR")
	}
	inv := &scriptedInvoker{errs: errs}
	err := invokeVia(testLimiter(), context.Background(), inv, &tg.UploadSaveBigFilePartRequest{})
	if err == nil {
		t.Fatal("want the transient error once retries run out")
	}
	if inv.calls != 1+maxTransientRetries {
		t.Fatalf("calls = %d, want %d", inv.calls, 1+maxTransientRetries)
	}
}

func TestTransientBackoffStopsOnCancel(t *testing.T) {
	rl := testLimiter()
	rl.transientBase = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	inv := &scriptedInvoker{errs: []error{tgerr.New(500, "INTERNAL_SERVER_ERROR")}}
	done := make(chan error, 1)
	go func() { done <- invokeVia(rl, ctx, inv, &tg.MessagesGetHistoryRequest{}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backoff ignored cancellation")
	}
}
