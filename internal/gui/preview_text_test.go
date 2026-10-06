//go:build gui

package gui_test

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The text preview asks for its first 2 MiB chunk by Range; the handler
// answers exactly that chunk and reads nothing past it from Telegram.
func TestPreviewTextServesOnlyTheInitialChunk(t *testing.T) {
	const chunk = 2 << 20
	line := "2026-01-01T00:00:00Z INFO preview log line with ünïcödé text\n"
	body := strings.Repeat(line, (5<<20)/len(line))
	seedDrive(t, map[string]string{"/logs/big.log": body})
	logPath := filepath.Join(t.TempDir(), "ranges.jsonl")
	t.Setenv("TD_FAKE_RANGE_LOG", logPath)
	svc := openGUI(t)
	srv := mediaServer(t, svc)

	d := preview(t, svc, "/logs/big.log")
	if !d.Capabilities.Ranges || d.Size != int64(len(body)) {
		t.Fatalf("descriptor = %+v, want a seekable %d-byte file", d, len(body))
	}
	resp := mediaRequest(t, http.MethodGet, srv.URL+d.URL, map[string]string{"Range": "bytes=0-" + strconv.Itoa(chunk-1)})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Content-Range"), "bytes 0-"+strconv.Itoa(chunk-1)+"/"+strconv.Itoa(len(body)); got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	if got := readBody(t, resp); !bytes.Equal(got, []byte(body[:chunk])) {
		t.Fatalf("initial chunk differs: %d bytes, want the first %d", len(got), chunk)
	}

	var read int64
	for _, e := range readRangeLog(t, logPath) {
		if e.Event == "done" {
			if e.Offset+e.Length > chunk {
				t.Fatalf("Telegram read %d+%d, past the requested chunk", e.Offset, e.Length)
			}
			read += e.Length
		}
	}
	if read != chunk {
		t.Fatalf("Telegram read %d bytes, want %d", read, chunk)
	}
}

// An HTML file previews as source: the media route never hands the webview
// an HTML document to render.
func TestPreviewHTMLIsServedAsPlainText(t *testing.T) {
	page := `<!doctype html><script>alert(1)</script><img src=x onerror=alert(2)>`
	seedDrive(t, map[string]string{"/site/index.html": page})
	svc := openGUI(t)
	srv := mediaServer(t, svc)

	resp := mediaRequest(t, http.MethodGet, srv.URL+preview(t, svc, "/site/index.html").URL, map[string]string{"Range": "bytes=0-" + strconv.Itoa(2<<20-1)})
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
	if got := readBody(t, resp); string(got) != page {
		t.Fatalf("body = %q", got)
	}
}
