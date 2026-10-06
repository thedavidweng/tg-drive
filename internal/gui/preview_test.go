//go:build gui

package gui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/core/manifest"
	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/core/telegram/fake"
	"github.com/thedavidweng/tg-drive/internal/gui"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// fixtureBytes is a deterministic, non-repeating-looking body of n bytes.
func fixtureBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + i/251)
	}
	return b
}

// mediaServer serves the GUI's media route the way cmd/td-gui mounts it
// into the Wails asset handler.
func mediaServer(t *testing.T, svc *gui.Services) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(svc.MediaHandler())
	t.Cleanup(srv.Close)
	return srv
}

func preview(t *testing.T, svc *gui.Services, path string) *gui.PreviewDescriptor {
	t.Helper()
	d, err := svc.Drive.Preview(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mediaRequest(t *testing.T, method, url string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPreviewHeadDescribesSeekableMedia(t *testing.T) {
	body := fixtureBytes(5000)
	seedDrive(t, map[string]string{"/photos/cat.png": string(body)})
	svc := openGUI(t)
	srv := mediaServer(t, svc)

	d := preview(t, svc, "/photos/cat.png")
	if d.Name != "cat.png" || d.Path != "/photos/cat.png" || d.MIME != "image/png" || d.Size != 5000 {
		t.Fatalf("descriptor = %+v", d)
	}
	if !d.Capabilities.Ranges || d.Capabilities.MediaSize != 5000 {
		t.Fatalf("capabilities = %+v, want seekable 5000 bytes", d.Capabilities)
	}

	resp := mediaRequest(t, http.MethodHead, srv.URL+d.URL, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":   "image/png",
		"Content-Length": strconv.Itoa(len(body)),
		"Accept-Ranges":  "bytes",
		"Cache-Control":  "private, no-store",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if got := resp.Header.Get("Content-Disposition"); got != `inline; filename*=UTF-8''cat.png` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if b := readBody(t, resp); len(b) != 0 {
		t.Fatalf("HEAD wrote %d body bytes", len(b))
	}
}

func TestPreviewGetServesExactBytes(t *testing.T) {
	body := fixtureBytes(70_000)
	seedDrive(t, map[string]string{"/video/clip.mp4": string(body)})
	svc := openGUI(t)
	srv := mediaServer(t, svc)
	url := srv.URL + preview(t, svc, "/video/clip.mp4").URL

	full := mediaRequest(t, http.MethodGet, url, nil)
	if full.StatusCode != http.StatusOK || full.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("GET = %d %q", full.StatusCode, full.Header.Get("Content-Type"))
	}
	if got := readBody(t, full); !bytes.Equal(got, body) {
		t.Fatalf("GET body differs: %d bytes, want %d", len(got), len(body))
	}

	for _, tc := range []struct {
		spec       string
		start, end int
	}{
		{"bytes=0-99", 0, 99},
		{"bytes=33333-34567", 33333, 34567},
		{"bytes=69900-", 69900, 69999},
		{"bytes=-10", 69990, 69999},
		{"bytes=69990-80000", 69990, 69999},
	} {
		resp := mediaRequest(t, http.MethodGet, url, map[string]string{"Range": tc.spec})
		if resp.StatusCode != http.StatusPartialContent {
			t.Fatalf("%s: status = %d, want 206", tc.spec, resp.StatusCode)
		}
		wantRange := "bytes " + strconv.Itoa(tc.start) + "-" + strconv.Itoa(tc.end) + "/70000"
		if got := resp.Header.Get("Content-Range"); got != wantRange {
			t.Fatalf("%s: Content-Range = %q, want %q", tc.spec, got, wantRange)
		}
		if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(tc.end-tc.start+1) {
			t.Fatalf("%s: Content-Length = %q", tc.spec, got)
		}
		if got := readBody(t, resp); !bytes.Equal(got, body[tc.start:tc.end+1]) {
			t.Fatalf("%s: body differs (%d bytes)", tc.spec, len(got))
		}
	}
}

func TestPreviewUnsatisfiableRangeIs416(t *testing.T) {
	seedDrive(t, map[string]string{"/a.bin": string(fixtureBytes(1000))})
	svc := openGUI(t)
	srv := mediaServer(t, svc)
	url := srv.URL + preview(t, svc, "/a.bin").URL

	for _, spec := range []string{"bytes=1000-", "bytes=5000-6000"} {
		resp := mediaRequest(t, http.MethodGet, url, map[string]string{"Range": spec})
		if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("%s: status = %d, want 416", spec, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Range"); got != "bytes */1000" {
			t.Fatalf("%s: Content-Range = %q, want bytes */1000", spec, got)
		}
	}
}

func TestPreviewMediaRequiresTheProcessCapability(t *testing.T) {
	seedDrive(t, map[string]string{"/a.bin": string(fixtureBytes(1000))})
	svc := openGUI(t)
	srv := mediaServer(t, svc)
	u, err := neturl.Parse(preview(t, svc, "/a.bin").URL)
	if err != nil {
		t.Fatal(err)
	}
	capability := u.Query().Get("c")
	if len(capability) < 32 {
		t.Fatalf("capability %q is too short to be unguessable", capability)
	}
	for name, query := range map[string]string{
		"missing": "",
		"wrong":   "?c=" + strings.Repeat("0", len(capability)),
		"prefix":  "?c=" + capability[:len(capability)-1],
	} {
		resp := mediaRequest(t, http.MethodGet, srv.URL+u.Path+query, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s capability: status = %d, want 403", name, resp.StatusCode)
		}
		if b := readBody(t, resp); bytes.Contains(b, []byte(capability)) || len(b) > 64 {
			t.Fatalf("%s capability: body %q", name, b)
		}
	}
}

// seedSecondDrive binds a second drive channel titled title, the way td
// init --create-channel would, and uploads files into it.
func seedSecondDrive(t *testing.T, title string, files map[string]string) {
	t.Helper()
	app, closeApp, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	ctx := context.Background()
	if _, err := app.InitRoot(ctx, t.TempDir(), "", title, ""); err != nil {
		t.Fatal(err)
	}
	ctx = service.WithChannel(ctx, title)
	src := t.TempDir()
	for remote, body := range files {
		local := filepath.Join(src, filepath.Base(remote))
		if err := os.WriteFile(local, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := app.UploadFile(ctx, local, remote, service.ConflictFail, false, service.UploadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreviewURLStaysPinnedAcrossAChannelSwitch(t *testing.T) {
	seedDrive(t, map[string]string{"/same.txt": "drive body"})
	seedSecondDrive(t, "Archive", map[string]string{"/same.txt": "archive body, longer"})
	svc := openGUI(t)
	srv := mediaServer(t, svc)
	ctx := context.Background()

	pinned := preview(t, svc, "/same.txt").URL
	channels, err := svc.Channels.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Channels.Select(ctx, boundChannel(t, channels, "Archive").ChannelID); err != nil {
		t.Fatal(err)
	}

	if got := string(readBody(t, mediaRequest(t, http.MethodGet, srv.URL+pinned, nil))); got != "drive body" {
		t.Fatalf("pinned URL after the switch served %q, want the Drive file", got)
	}
	fresh := preview(t, svc, "/same.txt")
	if got := string(readBody(t, mediaRequest(t, http.MethodGet, srv.URL+fresh.URL, nil))); got != "archive body, longer" {
		t.Fatalf("a preview prepared after the switch served %q, want the Archive file", got)
	}
}

func TestPreviewURLStopsServingADeletedFile(t *testing.T) {
	seedDrive(t, map[string]string{"/gone.txt": "bye"})
	svc := openGUI(t)
	srv := mediaServer(t, svc)
	url := srv.URL + preview(t, svc, "/gone.txt").URL
	if _, err := svc.Drive.Delete(context.Background(), "/gone.txt", gui.DeleteOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if resp := mediaRequest(t, http.MethodGet, url, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", resp.StatusCode)
	}
}

func TestPreviewURLStopsServingARowReboundByScan(t *testing.T) {
	const replacement = "replacement body, longer than the original"
	seedDriveEnv(t, map[string]string{"/same.txt": "original"}, func(statePath string) {
		tg := fake.NewPersistent(statePath)
		ch, err := tg.ResolveChannel(context.Background(), "Drive")
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range tg.Messages(ch.ID) {
			if msg.Kind == telegram.KindDocument {
				msg.ID += 1000
				msg.Data = []byte(replacement)
				msg.FileSize = int64(len(replacement))
				msg.Caption = manifest.RenderCompact(manifest.FileMeta{
					CanonicalPath: "/same.txt", DisplayName: "same.txt",
					Size: int64(len(replacement)), MIME: "text/plain",
				})
				tg.AddMessage(ch.ID, msg)
				return
			}
		}
		t.Fatal("seeded document not found")
	})
	svc := openGUI(t)
	srv := mediaServer(t, svc)
	stale := preview(t, svc, "/same.txt")
	if _, err := svc.Drive.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		resp := mediaRequest(t, method, srv.URL+stale.URL, map[string]string{"Range": "bytes=0-3"})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s stale preview = %d, want 404", method, resp.StatusCode)
		}
	}
	fresh := preview(t, svc, "/same.txt")
	resp := mediaRequest(t, http.MethodGet, srv.URL+fresh.URL, nil)
	if resp.StatusCode != http.StatusOK || string(readBody(t, resp)) != replacement {
		t.Fatalf("fresh preview = %d, want the complete replacement", resp.StatusCode)
	}
}

// rangeLogEntry is one line of the fake Telegram's TD_FAKE_RANGE_LOG.
type rangeLogEntry struct {
	Event  string `json:"event"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	Error  string `json:"error"`
}

func readRangeLog(t *testing.T, path string) []rangeLogEntry {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []rangeLogEntry
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var e rangeLogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("range log line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestPreviewRangeNearTheEndReadsOnlyThatRange(t *testing.T) {
	const size = 8 << 20
	body := fixtureBytes(size)
	seedDrive(t, map[string]string{"/big.mkv": string(body)})
	logPath := filepath.Join(t.TempDir(), "ranges.jsonl")
	t.Setenv("TD_FAKE_RANGE_LOG", logPath)
	svc := openGUI(t)
	srv := mediaServer(t, svc)

	url := srv.URL + preview(t, svc, "/big.mkv").URL
	resp := mediaRequest(t, http.MethodGet, url, map[string]string{"Range": "bytes=8388000-"})
	if got := readBody(t, resp); resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, body[8388000:]) {
		t.Fatalf("tail range = %d with %d bytes", resp.StatusCode, len(got))
	}

	var reads []rangeLogEntry
	for _, e := range readRangeLog(t, logPath) {
		if e.Event == "done" && e.Length > 0 {
			reads = append(reads, e)
		}
	}
	if len(reads) != 1 || reads[0].Offset != 8388000 || reads[0].Length != size-8388000 {
		t.Fatalf("body reads = %+v, want only 8388000+%d", reads, size-8388000)
	}
}

func TestPreviewClientDisconnectCancelsTheTelegramRead(t *testing.T) {
	seedDrive(t, map[string]string{"/slow.mp4": string(fixtureBytes(4096))})
	logPath := filepath.Join(t.TempDir(), "ranges.jsonl")
	t.Setenv("TD_FAKE_RANGE_LOG", logPath)
	t.Setenv("TD_FAKE_TRANSFER_DELAY", "1m")
	svc := openGUI(t)
	srv := mediaServer(t, svc)
	url := srv.URL + preview(t, svc, "/slow.mp4").URL

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()

	waitFor := func(what string, ok func([]rangeLogEntry) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok(readRangeLog(t, logPath)) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; log = %+v", what, readRangeLog(t, logPath))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor("the body read to start", func(es []rangeLogEntry) bool {
		return slices.ContainsFunc(es, func(e rangeLogEntry) bool { return e.Event == "start" && e.Length > 0 })
	})
	cancel()
	<-done
	waitFor("the body read to end cancelled", func(es []rangeLogEntry) bool {
		return slices.ContainsFunc(es, func(e rangeLogEntry) bool {
			return e.Event == "done" && e.Length > 0 && strings.Contains(e.Error, "context canceled")
		})
	})
}

// uploadNativePhoto sends local bytes as a native Telegram photo message at
// remote, the way td cp --as photo does.
func uploadNativePhoto(t *testing.T, remote string, body []byte) {
	t.Helper()
	app, closeApp, err := service.Open(service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	local := filepath.Join(t.TempDir(), filepath.Base(remote))
	if err := os.WriteFile(local, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFileAs(context.Background(), local, remote, service.ConflictFail, false, service.Presentation{Kind: "photo"}, service.UploadOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewServesANativePhotoWholeWithoutRanges(t *testing.T) {
	seedDrive(t, nil)
	// The name says PNG, but Telegram stores native photos as JPEG.
	uploadNativePhoto(t, "/shot.png", []byte("jpeg bytes"))
	svc := openGUI(t)
	srv := mediaServer(t, svc)

	d := preview(t, svc, "/shot.png")
	if d.Capabilities.Ranges || d.Capabilities.MediaSize != -1 {
		t.Fatalf("native photo capabilities = %+v, want no ranges and an unknown size", d.Capabilities)
	}

	head := mediaRequest(t, http.MethodHead, srv.URL+d.URL, nil)
	if head.StatusCode != http.StatusOK || head.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("HEAD = %d Content-Type %q, want 200 image/jpeg", head.StatusCode, head.Header.Get("Content-Type"))
	}
	if head.Header.Get("Accept-Ranges") != "none" || head.Header.Get("Content-Length") != "" {
		t.Fatalf("HEAD Accept-Ranges %q Content-Length %q, want none and no length",
			head.Header.Get("Accept-Ranges"), head.Header.Get("Content-Length"))
	}

	resp := mediaRequest(t, http.MethodGet, srv.URL+d.URL, map[string]string{"Range": "bytes=0-3"})
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Accept-Ranges") != "none" || resp.Header.Get("Content-Range") != "" {
		t.Fatalf("GET = %d Accept-Ranges %q Content-Range %q, want a whole 200 without ranges",
			resp.StatusCode, resp.Header.Get("Accept-Ranges"), resp.Header.Get("Content-Range"))
	}
	if resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("GET Content-Type = %q, want image/jpeg", resp.Header.Get("Content-Type"))
	}
	if got := string(readBody(t, resp)); got != "jpeg bytes" {
		t.Fatalf("body = %q", got)
	}
}

func TestPreviewServesATextMessageAsWholeText(t *testing.T) {
	seedUnmanaged(t, telegram.Message{
		ID: 601, Kind: telegram.KindText, MIME: "text/plain", Text: "shopping list\nmilk",
		Date: time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC),
	})
	svc := openGUI(t)
	if _, err := svc.Maintenance.Adopt(context.Background(), gui.AdoptOptions{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	srv := mediaServer(t, svc)

	d := preview(t, svc, "/notes/shopping list.txt")
	if d.Capabilities.Ranges || d.Capabilities.MediaSize != -1 {
		t.Fatalf("text message capabilities = %+v, want no ranges and an unknown size", d.Capabilities)
	}
	resp := mediaRequest(t, http.MethodGet, srv.URL+d.URL, map[string]string{"Range": "bytes=2-5"})
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Accept-Ranges") != "none" {
		t.Fatalf("GET = %d Accept-Ranges %q, want a whole 200 without ranges", resp.StatusCode, resp.Header.Get("Accept-Ranges"))
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want plain text", got)
	}
	if got := string(readBody(t, resp)); got != "shopping list\nmilk" {
		t.Fatalf("body = %q, want only the human text", got)
	}
}

func TestPreviewFloodWaitIsARedactedErrorThatLeavesTheDriveIntact(t *testing.T) {
	seedDrive(t, map[string]string{"/secret-plans/report.pdf": "pdf bytes"})
	uploadNativePhoto(t, "/secret-plans/shot.jpg", []byte("jpeg bytes"))
	t.Setenv("TD_FAKE_MEDIA_FLOOD_WAIT", "30")
	svc := openGUI(t)
	srv := mediaServer(t, svc)

	for _, path := range []string{"/secret-plans/report.pdf", "/secret-plans/shot.jpg"} {
		d := preview(t, svc, path)
		u, err := neturl.Parse(d.URL)
		if err != nil {
			t.Fatal(err)
		}
		capability := u.Query().Get("c")
		resp := mediaRequest(t, http.MethodGet, srv.URL+d.URL, nil)
		if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "30" {
			t.Fatalf("%s: GET = %d Retry-After %q, want 429 after 30s", path, resp.StatusCode, resp.Header.Get("Retry-After"))
		}
		body := string(readBody(t, resp))
		if body != http.StatusText(http.StatusTooManyRequests) {
			t.Fatalf("%s: error body = %q, want only the status text", path, body)
		}
		for k, vs := range resp.Header {
			for _, v := range vs {
				if strings.Contains(v, "secret-plans") || strings.Contains(v, capability) ||
					strings.Contains(v, "report") || strings.Contains(v, "shot") {
					t.Fatalf("%s: error header %s = %q leaks the file or capability", path, k, v)
				}
			}
		}
	}

	entries, err := svc.Drive.List(context.Background(), "/secret-plans")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("List after a rate-limited preview = %+v, want both files", entries)
	}
}

// The frontend shows no view for a ZIP; the facade still describes it, and
// the fallback's Download, run while the preview is open, gets the file.
func TestPreviewOfAnUnsupportedFileLeavesDownloadWorking(t *testing.T) {
	body := fixtureBytes(4096)
	seedDrive(t, map[string]string{"/archive.zip": string(body)})
	svc := openGUI(t)
	srv := mediaServer(t, svc)
	ctx := context.Background()

	d := preview(t, svc, "/archive.zip")
	if d.Size != int64(len(body)) || d.URL == "" {
		t.Fatalf("descriptor = %+v", d)
	}
	resp := mediaRequest(t, http.MethodGet, srv.URL+d.URL, map[string]string{"Range": "bytes=0-15"})
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(readBody(t, resp), body[:16]) {
		t.Fatalf("media GET = %d", resp.StatusCode)
	}

	dest := t.TempDir()
	id, err := svc.Transfers.Download(ctx, "/archive.zip", dest, gui.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitForStage(t, svc, id, "completed")
	got, err := os.ReadFile(filepath.Join(dest, "archive.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("downloaded archive.zip differs: %d bytes, want %d", len(got), len(body))
	}
}
