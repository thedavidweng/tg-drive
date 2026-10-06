package fake

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/core/telegram"
)

func rangeFixture(t *testing.T) (*Client, int64, telegram.Message, []byte) {
	t.Helper()
	c := New()
	loginFake(t, c)
	const ch = int64(1)
	data := make([]byte, 10_000)
	for i := range data {
		data[i] = byte(i*7 + i/256)
	}
	msg := c.AddMessage(ch, telegram.Message{Kind: telegram.KindVideo, FileName: "clip.mp4", MIME: "video/mp4", Data: data})
	return c, ch, msg, data
}

func TestReadMediaRangeReturnsExactBytesAndRecordsRanges(t *testing.T) {
	c, ch, msg, data := rangeFixture(t)
	ctx := context.Background()
	for _, r := range []struct{ off, n int64 }{{0, 100}, {4321, 1234}, {9990, 10}} {
		var out bytes.Buffer
		info, err := c.ReadMediaRange(ctx, ch, msg.ID, r.off, r.n, &out)
		if err != nil {
			t.Fatal(err)
		}
		if want := (telegram.MediaInfo{Size: 10_000, Seekable: true, MIME: "video/mp4"}); info != want {
			t.Fatalf("info = %+v, want %+v", info, want)
		}
		if !bytes.Equal(out.Bytes(), data[r.off:r.off+r.n]) {
			t.Fatalf("range %d+%d returned different bytes", r.off, r.n)
		}
	}
	want := []MediaRangeRead{
		{ChannelID: ch, MessageID: msg.ID, Offset: 0, Length: 100},
		{ChannelID: ch, MessageID: msg.ID, Offset: 4321, Length: 1234},
		{ChannelID: ch, MessageID: msg.ID, Offset: 9990, Length: 10},
	}
	got := c.MediaRangeReads()
	if len(got) != len(want) {
		t.Fatalf("reads = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reads = %+v, want %+v", got, want)
		}
	}
}

func TestReadMediaRangeRejectsUnservableIntervals(t *testing.T) {
	c, ch, msg, _ := rangeFixture(t)
	photo := c.AddMessage(ch, telegram.Message{Kind: telegram.KindPhoto, MIME: "image/jpeg", Data: []byte("jpeg")})
	ctx := context.Background()
	for _, r := range []struct {
		id       int
		off, n   int64
		seekable bool
	}{{msg.ID, 9000, 1001, true}, {msg.ID, -1, 1, true}, {photo.ID, 0, 2, false}} {
		var out bytes.Buffer
		info, err := c.ReadMediaRange(ctx, ch, r.id, r.off, r.n, &out)
		var re *telegram.MediaRangeError
		if !errors.As(err, &re) {
			t.Fatalf("range %d+%d: err = %v, want *MediaRangeError", r.off, r.n, err)
		}
		if info.Seekable != r.seekable || out.Len() != 0 {
			t.Fatalf("range %d+%d: info = %+v wrote %d bytes", r.off, r.n, info, out.Len())
		}
	}
	if _, err := c.ReadMediaRange(ctx, ch, 999, 0, 0, &bytes.Buffer{}); !errors.As(err, new(*telegram.MessageNotFoundError)) {
		t.Fatalf("missing message err = %v, want MessageNotFoundError", err)
	}
}

func TestReadMediaRangeDelayDoesNotSerializeReads(t *testing.T) {
	c, ch, msg, _ := rangeFixture(t)
	const delay = 200 * time.Millisecond
	c.SetTransferDelay(delay)
	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.ReadMediaRange(context.Background(), ch, msg.ID, int64(i*100), 100, &bytes.Buffer{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed >= 3*delay {
		t.Fatalf("4 concurrent reads took %s, want them to overlap (delay %s each)", elapsed, delay)
	}
}

func TestReadMediaRangeHonoursCancellation(t *testing.T) {
	c, ch, msg, _ := rangeFixture(t)
	c.SetTransferDelay(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var out bytes.Buffer
	go func() {
		_, err := c.ReadMediaRange(ctx, ch, msg.ID, 0, 100, &out)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if out.Len() != 0 {
			t.Fatalf("wrote %d bytes after cancellation", out.Len())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read kept waiting after its context was cancelled")
	}
}
