package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// A preview is an ephemeral read of an active file's media for a front end
// to display. It is not a Transfer: it takes no operation lock, writes
// nothing to the index, and keeps no history. Its reads go through the
// Telegram client's exact-range seam (ReadMediaRange), so a range near the
// end of a large file fetches only that range.

// PreviewFile is an active file row resolved for preview. It is pinned to
// the row and its channel, so later reads never re-resolve the path against
// whichever channel is active by then.
type PreviewFile struct {
	// ID is the files row; Lookup by it only succeeds while the row is
	// still active.
	ID        int64
	Path      string
	Name      string
	MIME      string
	Size      int64
	UpdatedAt string

	channelTGID int64
	messageID   int
}

// ResolvePreview resolves the active file at remotePath in the channel ctx
// selects.
func (a *App) ResolvePreview(ctx context.Context, remotePath string) (PreviewFile, error) {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return PreviewFile{}, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return PreviewFile{}, err
	}
	row, found, err := a.DB.ActiveByPath(ctx, ch.rowID, p)
	if err != nil {
		return PreviewFile{}, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	if !found {
		return PreviewFile{}, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
	}
	return previewFile(row, ch.tgID)
}

// PreviewFileByID resolves an active files row by its id, in whichever
// channel it belongs to. found is false once the row is gone or no longer
// active (deleted, moved, replaced).
func (a *App) PreviewFileByID(ctx context.Context, id int64) (PreviewFile, bool, error) {
	row, tgChannel, found, err := a.DB.ActiveByID(ctx, id)
	if err != nil {
		return PreviewFile{}, false, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	if !found {
		return PreviewFile{}, false, nil
	}
	tgID, err := strconv.ParseInt(tgChannel, 10, 64)
	if err != nil {
		return PreviewFile{}, false, apperr.New(apperr.ErrDB, "stored channel id is not numeric")
	}
	f, err := previewFile(row, tgID)
	return f, err == nil, err
}

func previewFile(row sqlitestore.FileRow, channelTGID int64) (PreviewFile, error) {
	if !row.MessageID.Valid {
		return PreviewFile{}, apperr.New(apperr.ErrDB, fmt.Sprintf("lookup file: active row for %q has no message", row.CanonicalPath))
	}
	name := row.DisplayName
	if name == "" {
		name = fsmodel.BaseName(row.CanonicalPath)
	}
	mt := row.MIME
	if mt == "" || mt == "application/octet-stream" {
		if guess := detectMIME(name); guess != "application/octet-stream" || mt == "" {
			mt = guess
		}
	}
	return PreviewFile{
		ID:          row.ID,
		Path:        row.CanonicalPath,
		Name:        name,
		MIME:        mt,
		Size:        row.Size.Int64,
		UpdatedAt:   row.UpdatedAt,
		channelTGID: channelTGID,
		messageID:   int(row.MessageID.Int64),
	}, nil
}

func (a *App) previewClient() (telegram.Client, error) {
	if a.TG == nil {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram is not configured")
	}
	return a.TG, nil
}

// ReadPreviewRange writes exactly bytes [offset, offset+length) of f's
// media into w and describes the representation; a zero length only
// describes it. Errors are mapped like every other Telegram failure; an
// interval the representation cannot serve stays a *telegram.MediaRangeError
// so callers can answer it as unsatisfiable.
func (a *App) ReadPreviewRange(ctx context.Context, f PreviewFile, offset, length int64, w io.Writer) (telegram.MediaInfo, error) {
	tg, err := a.previewClient()
	if err != nil {
		return telegram.MediaInfo{Size: -1}, err
	}
	info, err := tg.ReadMediaRange(ctx, f.channelTGID, f.messageID, offset, length, w)
	if err != nil {
		var re *telegram.MediaRangeError
		if errors.As(err, &re) {
			return info, re
		}
		return info, telegram.MapError(err)
	}
	return info, nil
}

// StreamPreview writes f's whole downloadable body into w, the way a
// download would: the path for representations that are not seekable
// (native photos, text messages).
func (a *App) StreamPreview(ctx context.Context, f PreviewFile, w io.Writer) error {
	if _, err := a.previewClient(); err != nil {
		return err
	}
	_, err := a.downloadTo(ctx, f.channelTGID, f.messageID, w, &progressWriter{})
	return telegram.MapError(err)
}

// previewMIMEIsActive reports whether serving mt to a webview as is could
// run stored markup or script in the app's origin.
func previewMIMEIsActive(mt string) bool {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(mt, ";", 2)[0]))
	switch base {
	case "text/html", "application/xhtml+xml", "text/javascript", "application/javascript",
		"application/ecmascript", "text/ecmascript", "text/xml", "application/xml":
		return true
	}
	return false
}

// PreviewContentType is the Content-Type a preview of f is served with when
// Telegram serves the representation info describes. The representation's
// own type wins over the indexed one: a native photo is JPEG and a text
// message is plain text whatever the file is named. Types a webview would
// execute or render as an active document (HTML, XHTML, script, XML) are
// served as plain text: a preview shows a stored page's source, it never
// runs it.
func PreviewContentType(f PreviewFile, info telegram.MediaInfo) string {
	mt := info.MIME
	if mt == "" || mt == "application/octet-stream" {
		mt = f.MIME
	}
	if mt == "" {
		return "application/octet-stream"
	}
	if previewMIMEIsActive(mt) {
		return "text/plain; charset=utf-8"
	}
	return mt
}
