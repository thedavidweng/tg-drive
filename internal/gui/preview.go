//go:build gui

package gui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// MediaRoute is the reserved path prefix of the preview media route that
// cmd/td-gui mounts into the Wails asset handler (ADR 0046). It carries
// preview bytes only; every command and its metadata stays on bindings.
const MediaRoute = "/td-media/"

// mediaCapabilityParam is the query parameter carrying the process
// capability. It is a query value rather than a path segment because the
// Wails asset server's request log prints the path, never the query.
const mediaCapabilityParam = "c"

// maxPreparedPreviews bounds how many prepared preview URLs stay servable;
// preparing one more forgets the oldest, whose URL then answers 404 until
// the frontend prepares it again.
const maxPreparedPreviews = 64

// PreviewDescriptor is what the frontend needs to show a file: safe display
// metadata, what the media route can do for it, and the media URL. It never
// carries Telegram channel or message IDs, access hashes, session data, or
// local paths.
type PreviewDescriptor struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// MIME is the indexed media type ("" when unknown); preview providers
	// match it first and the file extension second.
	MIME string `json:"mime"`
	// Size is the indexed size in bytes.
	Size int64 `json:"size"`
	// Date is the RFC3339 time the file last changed.
	Date         string              `json:"date"`
	Capabilities PreviewCapabilities `json:"capabilities"`
	// URL is the same-origin media URL, pinned to this file: a later
	// channel switch, move, or replace never makes it serve another file.
	// It stops working when the file stops being active, when the process
	// exits, or after enough newer previews were prepared.
	URL string `json:"url"`
}

// PreviewCapabilities is what the media URL serves for a file.
type PreviewCapabilities struct {
	// Ranges reports that the URL answers single byte-range requests (206):
	// the Telegram representation is seekable with a known size.
	Ranges bool `json:"ranges"`
	// MediaSize is the exact byte length the URL serves, or -1 when it is
	// not known in advance (a native photo's recompressed representation,
	// a text message). It can differ from Size for native photos.
	MediaSize int64 `json:"media_size"`
}

// preparedPreview is one prepared media URL: the pinned file row and the
// representation the media route serves for it, described once at prepare
// time so range requests do not each pay for a metadata round trip.
type preparedPreview struct {
	fileID      int64
	info        telegram.MediaInfo
	contentType string
	name        string
}

// media owns the process capability and the prepared preview URLs. The
// capability is random per process, never persisted, and never logged.
type media struct {
	state      *appState
	capability string

	mu       sync.Mutex
	prepared map[string]*preparedPreview
	order    []string
}

func newMedia(state *appState) *media {
	return &media{state: state, capability: randomHex(32), prepared: map[string]*preparedPreview{}}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func (m *media) prepare(p *preparedPreview) string {
	token := randomHex(16)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prepared[token] = p
	m.order = append(m.order, token)
	if len(m.order) > maxPreparedPreviews {
		delete(m.prepared, m.order[0])
		m.order = m.order[1:]
	}
	return MediaRoute + token + "?" + mediaCapabilityParam + "=" + m.capability
}

func (m *media) lookup(token string) (*preparedPreview, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prepared[token]
	return p, ok
}

// Preview resolves the active file at path in the active channel into a
// preview descriptor. It describes the file's Telegram representation once
// (no body bytes) and prepares a media URL pinned to the file row. Preview
// is an ephemeral read: no Transfer, operation lock, or history.
func (d *Drive) Preview(ctx context.Context, path string) (*PreviewDescriptor, error) {
	app := d.state.current()
	f, err := app.ResolvePreview(d.state.scoped(ctx), path)
	if err != nil {
		return nil, toError(err)
	}
	info, err := app.ReadPreviewRange(ctx, f, 0, 0, io.Discard)
	if err != nil {
		return nil, toError(err)
	}
	url := d.media.prepare(&preparedPreview{
		fileID:      f.ID,
		info:        info,
		contentType: service.PreviewContentType(f),
		name:        f.Name,
	})
	return &PreviewDescriptor{
		Name: f.Name,
		Path: f.Path,
		MIME: f.MIME,
		Size: f.Size,
		Date: f.UpdatedAt,
		Capabilities: PreviewCapabilities{
			Ranges:    info.Seekable,
			MediaSize: mediaSize(info),
		},
		URL: url,
	}, nil
}

func mediaSize(info telegram.MediaInfo) int64 {
	if info.Seekable && info.Size >= 0 {
		return info.Size
	}
	return -1
}

// MediaHandler is the framework-neutral handler of MediaRoute that
// cmd/td-gui mounts into the Wails asset handler. Services is not a bound
// Wails service, so this method is not in the frontend bindings.
func (s *Services) MediaHandler() http.Handler {
	return s.Drive.media
}

// ServeHTTP answers HEAD and GET for a prepared preview URL with one
// optional byte range. The request's context bounds the Telegram read, so
// a closed preview or a disconnected client cancels it. Responses never
// carry error details: the status says what failed.
func (m *media) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// A media URL opened as a document (rather than consumed by an img,
	// video, or fetch) must not run stored markup in the app's origin.
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:")

	given := r.URL.Query().Get(mediaCapabilityParam)
	if subtle.ConstantTimeCompare([]byte(given), []byte(m.capability)) != 1 {
		httpStatus(w, http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		httpStatus(w, http.StatusMethodNotAllowed)
		return
	}
	token, ok := strings.CutPrefix(r.URL.Path, MediaRoute)
	if !ok || token == "" || strings.Contains(token, "/") {
		httpStatus(w, http.StatusNotFound)
		return
	}
	p, ok := m.lookup(token)
	if !ok {
		httpStatus(w, http.StatusNotFound)
		return
	}
	app := m.state.current()
	f, found, err := app.PreviewFileByID(r.Context(), p.fileID)
	if err != nil {
		httpStatus(w, http.StatusInternalServerError)
		return
	}
	if !found {
		httpStatus(w, http.StatusNotFound)
		return
	}

	h.Set("Content-Type", p.contentType)
	h.Set("Content-Disposition", "inline; filename*=UTF-8''"+encodeRFC5987(p.name))
	if !p.info.Seekable {
		// Without a provable size and seekable representation the body is
		// served whole and any Range header is ignored, which HTTP allows.
		h.Set("Accept-Ranges", "none")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		lw := &lazyWriter{w: w, status: http.StatusOK}
		if err := app.StreamPreview(r.Context(), f, lw); err != nil && !lw.wrote {
			mediaError(w, err)
		}
		return
	}

	size := p.info.Size
	h.Set("Accept-Ranges", "bytes")
	start, length, status := int64(0), size, http.StatusOK
	if spec := r.Header.Get("Range"); spec != "" {
		s, n, ok, satisfiable := parseRange(spec, size)
		switch {
		case !satisfiable:
			h.Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
			httpStatus(w, http.StatusRequestedRangeNotSatisfiable)
			return
		case ok:
			start, length, status = s, n, http.StatusPartialContent
			h.Set("Content-Range", "bytes "+strconv.FormatInt(s, 10)+"-"+strconv.FormatInt(s+n-1, 10)+"/"+strconv.FormatInt(size, 10))
		}
	}
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	if r.Method == http.MethodHead || length == 0 {
		w.WriteHeader(status)
		return
	}
	lw := &lazyWriter{w: w, status: status}
	_, err = app.ReadPreviewRange(r.Context(), f, start, length, lw)
	if err != nil && !lw.wrote {
		var re *telegram.MediaRangeError
		if errors.As(err, &re) {
			// The representation changed since the preview was prepared.
			h.Del("Content-Length")
			h.Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
			httpStatus(w, http.StatusRequestedRangeNotSatisfiable)
			return
		}
		mediaError(w, err)
	}
}

// parseRange reads a single "bytes=" range against a body of size bytes.
// ok is false when the header should be ignored (another unit, several
// ranges, bad syntax), which serves the whole body; satisfiable is false
// when the one range lies wholly outside the body.
func parseRange(spec string, size int64) (start, length int64, ok, satisfiable bool) {
	v, found := strings.CutPrefix(spec, "bytes=")
	if !found || strings.Contains(v, ",") {
		return 0, 0, false, true
	}
	first, last, found := strings.Cut(strings.TrimSpace(v), "-")
	if !found {
		return 0, 0, false, true
	}
	if first == "" {
		// A suffix range: the final n bytes.
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, false, true
		}
		if n == 0 || size == 0 {
			return 0, 0, false, false
		}
		n = min(n, size)
		return size - n, n, true, true
	}
	s, err := strconv.ParseInt(first, 10, 64)
	if err != nil || s < 0 {
		return 0, 0, false, true
	}
	end := size - 1
	if last != "" {
		e, err := strconv.ParseInt(last, 10, 64)
		if err != nil || e < s {
			return 0, 0, false, true
		}
		end = min(e, size-1)
	}
	if s >= size {
		return 0, 0, false, false
	}
	return s, end - s + 1, true, true
}

// lazyWriter commits the response status on the first body byte, so a read
// that fails before producing any can still answer with an error status.
type lazyWriter struct {
	w      http.ResponseWriter
	status int
	wrote  bool
}

func (l *lazyWriter) Write(p []byte) (int, error) {
	if !l.wrote {
		l.wrote = true
		l.w.WriteHeader(l.status)
	}
	return l.w.Write(p)
}

// mediaError answers a failed read before any body byte went out. The
// response names the status only: messages can carry paths or Telegram
// detail that the media route must not echo.
func mediaError(w http.ResponseWriter, err error) {
	h := w.Header()
	h.Del("Content-Length")
	h.Del("Content-Range")
	h.Del("Content-Disposition")
	ae, ok := apperr.As(err)
	switch {
	case ok && ae.Code == apperr.ErrCancelled:
		// The client went away; nobody reads this answer.
		httpStatus(w, http.StatusServiceUnavailable)
	case ok && ae.Code == apperr.ErrTelegramRateLimited:
		if s, ok := ae.Details["retry_after_seconds"].(int); ok {
			h.Set("Retry-After", strconv.Itoa(s))
		}
		httpStatus(w, http.StatusTooManyRequests)
	case ok && ae.Code == apperr.ErrRemoteNotFound:
		httpStatus(w, http.StatusNotFound)
	default:
		httpStatus(w, http.StatusBadGateway)
	}
}

func httpStatus(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(http.StatusText(code)))
}

// encodeRFC5987 percent-encodes s as an RFC 5987 ext-value, so any file
// name is a safe filename* parameter.
func encodeRFC5987(s string) string {
	var b strings.Builder
	const hexDigits = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') ||
			strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0xF])
	}
	return b.String()
}
