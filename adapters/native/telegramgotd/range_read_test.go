package telegramgotd

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	tgtelegram "github.com/thedavidweng/tg-drive/core/telegram"
)

// The fake Telegram client replaces the whole adapter, so the translation of
// an exact byte interval into Telegram precise upload.getFile requests is
// pinned here against an invoker that serves a synthetic document.

const (
	kib = 1024
	mib = 1024 * 1024
)

type getFileCall struct {
	offset  int64
	limit   int
	precise bool
	// thumb is the photo size a photo location names; empty otherwise.
	thumb string
}

// fileInvoker serves upload.getFile from data the way Telegram does: the
// bytes at [offset, offset+limit), short at end of file. It records every
// request. A non-nil fail is returned for the request with index failAt.
type fileInvoker struct {
	mu     sync.Mutex
	data   []byte
	calls  []getFileCall
	fail   error
	failAt int
	// block, when set, makes the request with index blockAt report itself
	// on block and wait for the caller's cancellation.
	block   chan struct{}
	blockAt int
}

func (f *fileInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	req, ok := input.(*tg.UploadGetFileRequest)
	if !ok {
		return errors.New("unexpected request")
	}
	f.mu.Lock()
	idx := len(f.calls)
	call := getFileCall{offset: req.Offset, limit: req.Limit, precise: req.Precise}
	if loc, ok := req.Location.(*tg.InputPhotoFileLocation); ok {
		call.thumb = loc.ThumbSize
	}
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if f.fail != nil && idx == f.failAt {
		return f.fail
	}
	if f.block != nil && idx == f.blockAt {
		close(f.block)
		<-ctx.Done()
		return ctx.Err()
	}
	start := min(req.Offset, int64(len(f.data)))
	end := min(req.Offset+int64(req.Limit), int64(len(f.data)))
	var buf bin.Buffer
	if err := (&tg.UploadFile{Type: &tg.StorageFilePartial{}, Bytes: f.data[start:end]}).Encode(&buf); err != nil {
		return err
	}
	return output.Decode(&buf)
}

func (f *fileInvoker) recorded() []getFileCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]getFileCall(nil), f.calls...)
}

func patterned(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + i/251)
	}
	return b
}

func documentMessage(size int) *tg.Message {
	return &tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, Size: int64(size), MimeType: "video/mp4"}}}
}

// assertLegal checks every request against Telegram's precise rules.
func assertLegal(t *testing.T, calls []getFileCall) {
	t.Helper()
	for i, c := range calls {
		switch {
		case !c.precise:
			t.Errorf("request %d (%d+%d) is not precise", i, c.offset, c.limit)
		case c.offset%kib != 0 || c.limit%kib != 0:
			t.Errorf("request %d (%d+%d) is not 1 KiB aligned", i, c.offset, c.limit)
		case c.limit <= 0 || c.limit > mib:
			t.Errorf("request %d (%d+%d) has limit outside (0, 1 MiB]", i, c.offset, c.limit)
		case c.offset/mib != (c.offset+int64(c.limit)-1)/mib:
			t.Errorf("request %d (%d+%d) crosses a 1 MiB boundary", i, c.offset, c.limit)
		}
	}
}

func readRange(t *testing.T, inv *fileInvoker, msg *tg.Message, offset, length int64) ([]byte, error) {
	t.Helper()
	api := tg.NewClient(testLimiter().invoke(inv))
	var out bytes.Buffer
	_, err := readMessageRange(context.Background(), api, msg, offset, length, &out)
	return out.Bytes(), err
}

func TestRangeReadUnalignedIntervalIsTrimmed(t *testing.T) {
	data := patterned(3*mib + 500)
	inv := &fileInvoker{data: data}
	got, err := readRange(t, inv, documentMessage(len(data)), 1500, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data[1500:4500]) {
		t.Fatalf("got %d bytes, not bytes [1500, 4500)", len(got))
	}
	calls := inv.recorded()
	assertLegal(t, calls)
	want := []getFileCall{{offset: 1024, limit: 4096, precise: true}}
	if len(calls) != 1 || calls[0] != want[0] {
		t.Fatalf("requests = %+v, want %+v", calls, want)
	}
}

func TestRangeReadAcrossMiBBoundaryIsSplit(t *testing.T) {
	data := patterned(3 * mib)
	inv := &fileInvoker{data: data}
	got, err := readRange(t, inv, documentMessage(len(data)), mib-100, 300)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data[mib-100:mib+200]) {
		t.Fatalf("got %d bytes, not bytes [1 MiB-100, 1 MiB+200)", len(got))
	}
	calls := inv.recorded()
	assertLegal(t, calls)
	want := []getFileCall{
		{offset: mib - kib, limit: kib, precise: true},
		{offset: mib, limit: kib, precise: true},
	}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("requests = %+v, want %+v", calls, want)
	}
}

func TestRangeReadLongIntervalUsesLegalRequests(t *testing.T) {
	data := patterned(5*mib + 333)
	inv := &fileInvoker{data: data}
	offset, length := int64(10), int64(5*mib/2)
	got, err := readRange(t, inv, documentMessage(len(data)), offset, length)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data[offset:offset+length]) {
		t.Fatalf("got %d bytes, not the requested %d", len(got), length)
	}
	calls := inv.recorded()
	assertLegal(t, calls)
	// [0, 1 MiB), [1 MiB, 2 MiB), [2 MiB, 2.5 MiB + 1 KiB).
	if len(calls) != 3 || calls[0].offset != 0 || calls[2].offset != 2*mib || calls[2].limit != mib/2+kib {
		t.Fatalf("requests = %+v, want three requests covering [0, 2.5 MiB + 1 KiB)", calls)
	}
}

func TestRangeReadFinalShortChunk(t *testing.T) {
	data := patterned(2*mib + 700)
	inv := &fileInvoker{data: data}
	got, err := readRange(t, inv, documentMessage(len(data)), 2*mib+100, 600)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data[2*mib+100:]) {
		t.Fatalf("got %d bytes, not the last 600", len(got))
	}
	calls := inv.recorded()
	assertLegal(t, calls)
	if len(calls) != 1 || calls[0].offset != 2*mib || calls[0].limit != kib {
		t.Fatalf("requests = %+v, want one 1 KiB request at 2 MiB", calls)
	}
}

func TestRangeReadOnlyFetchesNeededChunks(t *testing.T) {
	// A read near the end of a large document must not start from byte zero.
	size := 64 * mib
	inv := &fileInvoker{data: patterned(size)}
	got, err := readRange(t, inv, documentMessage(size), int64(size-5000), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5000 {
		t.Fatalf("got %d bytes, want 5000", len(got))
	}
	calls := inv.recorded()
	assertLegal(t, calls)
	if len(calls) != 1 || calls[0].offset != int64(size-5*kib) {
		t.Fatalf("requests = %+v, want one request at the tail", calls)
	}
}

func TestRangeReadStopsOnCancel(t *testing.T) {
	size := 8 * mib
	inv := &fileInvoker{data: patterned(size), block: make(chan struct{}), blockAt: 1}
	api := tg.NewClient(testLimiter().invoke(inv))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := readMessageRange(ctx, api, documentMessage(size), 0, int64(size), discard{})
		done <- err
	}()
	select {
	case <-inv.block:
	case err := <-done:
		t.Fatalf("read ended before its second request: %v", err)
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
		t.Fatal("read kept running after its context was cancelled")
	}
	if n := len(inv.recorded()); n != 2 {
		t.Fatalf("requests = %d, want no request after cancellation", n)
	}
}

func TestRangeReadCancelledContextSendsNothing(t *testing.T) {
	inv := &fileInvoker{data: patterned(mib)}
	api := tg.NewClient(testLimiter().invoke(inv))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readMessageRange(ctx, api, documentMessage(mib), 0, 10, discard{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := len(inv.recorded()); n != 0 {
		t.Fatalf("requests = %d, want 0", n)
	}
}

func TestRangeReadMapsRPCErrors(t *testing.T) {
	cases := []struct {
		name  string
		rpc   error
		check func(error) bool
	}{
		{"permission", tgerr.New(400, "CHANNEL_PRIVATE"), func(err error) bool {
			var e *tgtelegram.PermissionDeniedError
			return errors.As(err, &e)
		}},
		{"auth", tgerr.New(401, "AUTH_KEY_UNREGISTERED"), func(err error) bool {
			var e *tgtelegram.AuthRequiredError
			return errors.As(err, &e)
		}},
		{"flood wait", tgerr.New(420, "FLOOD_WAIT_60"), func(err error) bool {
			var e *tgtelegram.FloodWaitError
			return errors.As(err, &e)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := &fileInvoker{data: patterned(2 * mib), fail: tc.rpc, failAt: 1}
			_, err := readRange(t, inv, documentMessage(2*mib), mib-10, 20)
			if !tc.check(err) {
				t.Fatalf("err = %v (%T), want the mapped error", err, err)
			}
			if n := len(inv.recorded()); n != 2 {
				t.Fatalf("requests = %d, want the read to stop at the failing request", n)
			}
		})
	}
}

func TestRangeReadDescribesRepresentation(t *testing.T) {
	photo := &tg.Message{Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}}
	text := &tg.Message{Message: "hello"}
	cases := []struct {
		name string
		msg  *tg.Message
		want tgtelegram.MediaInfo
	}{
		{"document", documentMessage(5000), tgtelegram.MediaInfo{Size: 5000, Seekable: true, MIME: "video/mp4"}},
		{"photo without sizes", photo, tgtelegram.MediaInfo{Size: -1, MIME: "image/jpeg"}},
		{"photo with a sized largest size", photoMessage(
			&tg.PhotoSize{Type: "s", W: 90, H: 90, Size: 2000},
			&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: 90000},
			&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 9000},
		), tgtelegram.MediaInfo{Size: 90000, Seekable: true, MIME: "image/jpeg"}},
		{"photo with a progressive largest size", photoMessage(
			&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 9000},
			&tg.PhotoSizeProgressive{Type: "y", W: 1280, H: 960, Sizes: []int{4000, 20000, 70000}},
		), tgtelegram.MediaInfo{Size: 70000, Seekable: true, MIME: "image/jpeg"}},
		{"photo whose largest size is unsized", photoMessage(
			&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 9000},
			&tg.PhotoSize{Type: "y", W: 1280, H: 960},
		), tgtelegram.MediaInfo{Size: -1, MIME: "image/jpeg"}},
		{"photo whose largest progressive size lists none", photoMessage(
			&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 9000},
			&tg.PhotoSizeProgressive{Type: "y", W: 1280, H: 960},
		), tgtelegram.MediaInfo{Size: -1, MIME: "image/jpeg"}},
		{"text", text, tgtelegram.MediaInfo{Size: -1, MIME: "text/plain; charset=utf-8"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := &fileInvoker{}
			api := tg.NewClient(testLimiter().invoke(inv))
			info, err := readMessageRange(context.Background(), api, tc.msg, 0, 0, discard{})
			if err != nil || info != tc.want {
				t.Fatalf("info = %+v err = %v, want %+v", info, err, tc.want)
			}
			if n := len(inv.recorded()); n != 0 {
				t.Fatalf("requests = %d, want none for a metadata-only read", n)
			}
		})
	}
}

func photoMessage(sizes ...tg.PhotoSizeClass) *tg.Message {
	return &tg.Message{Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1, Sizes: sizes}}}
}

func TestRangeReadServesTheLargestSizedPhoto(t *testing.T) {
	data := patterned(90000)
	inv := &fileInvoker{data: data}
	msg := photoMessage(
		&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 9000},
		&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: int(len(data))},
	)
	got, err := readRange(t, inv, msg, 85000, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data[85000:]) {
		t.Fatalf("got %d bytes, not the photo's last 5000", len(got))
	}
	calls := inv.recorded()
	assertLegal(t, calls)
	for i, c := range calls {
		if c.thumb != "y" {
			t.Fatalf("request %d read photo size %q, want the largest (y)", i, c.thumb)
		}
	}
}

func TestRangeReadRejectsUnservableIntervals(t *testing.T) {
	photo := &tg.Message{Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}}
	cases := []struct {
		name           string
		msg            *tg.Message
		offset, length int64
	}{
		{"past end", documentMessage(5000), 4000, 1001},
		{"offset past end", documentMessage(5000), 5001, 1},
		{"negative offset", documentMessage(5000), -1, 10},
		{"negative length", documentMessage(5000), 0, -1},
		{"not seekable", photo, 0, 10},
		{"past a sized photo's end", photoMessage(&tg.PhotoSize{Type: "y", W: 10, H: 10, Size: 5000}), 4990, 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := &fileInvoker{data: patterned(5000)}
			got, err := readRange(t, inv, tc.msg, tc.offset, tc.length)
			var re *tgtelegram.MediaRangeError
			if !errors.As(err, &re) {
				t.Fatalf("err = %v, want *MediaRangeError", err)
			}
			if len(got) != 0 || len(inv.recorded()) != 0 {
				t.Fatalf("wrote %d bytes over %d requests, want nothing", len(got), len(inv.recorded()))
			}
		})
	}
}
