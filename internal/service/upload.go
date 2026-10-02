package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/localfs"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/model"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
	"github.com/thedavidweng/tg-drive-cli/core/publisher"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"lukechampine.com/blake3"
)

// App is the main application service.
type App struct {
	Cfg config.Config
	// ConfigPath is the resolved config file, checked by td doctor.
	ConfigPath string
	DB         *sqlitestore.DB
	TG         telegram.Client
	// Channel optionally selects a configured channel by title or Telegram ID
	// (from --channel / TD_CHANNEL). Empty selects the first configured one.
	Channel string
	Render  func() bool // returns json mode
	// Progress optionally receives upload part confirmations.
	Progress telegram.UploadProgress
	// Index optionally overrides the file index used by the publisher.
	// Tests inject a failing index to cover the post-upload crash window.
	Index ports.FileIndex

	limitMu     sync.Mutex
	cachedLimit int64
}

func (a *App) files() ports.FileSystem {
	return localfs.FS{}
}

func (a *App) fileIndex() ports.FileIndex {
	if a.Index != nil {
		return a.Index
	}
	return a.DB
}

func (a *App) publisher() *publisher.Publisher {
	return publisher.New(a.TG, a.fileIndex(), publisher.Config{
		SafeMediaCaptionUTF16Units: a.Cfg.Caption.SafeMediaCaptionUTF16Units,
		MarginUTF16Units:           a.Cfg.Caption.MarginUTF16Units,
	})
}

func isMessageGone(err error) bool {
	var nf *telegram.MessageNotFoundError
	return errors.As(err, &nf)
}

// ConflictPolicy for uploads/downloads.
type ConflictPolicy string

const (
	ConflictFail    ConflictPolicy = "fail"
	ConflictReplace ConflictPolicy = "replace"
	ConflictSkip    ConflictPolicy = "skip"
	ConflictRename  ConflictPolicy = "rename"
)

func (a *App) channelID(ctx context.Context) (int64, string, error) {
	var id int64
	var tgID, title string
	var err error
	if a.Channel != "" {
		err = a.DB.Raw().QueryRowContext(ctx, `select id, tg_channel_id, title from channels where title=? or tg_channel_id=? limit 1`,
			a.Channel, a.Channel).Scan(&id, &tgID, &title)
		if err == sql.ErrNoRows {
			return 0, "", apperr.New(apperr.ErrChannelNotFound, "channel not found: "+a.Channel)
		}
	} else {
		err = a.DB.Raw().QueryRowContext(ctx, `select id, tg_channel_id, title from channels limit 1`).Scan(&id, &tgID, &title)
		if err == sql.ErrNoRows {
			return 0, "", apperr.New(apperr.ErrChannelNotFound,
				"no channel bound in this database; run: td init <local-root> --create-channel (new drive) or --bind-channel (existing drive, rebuilds the index)")
		}
	}
	return id, tgID, err
}

func (a *App) tgChannelID(ctx context.Context) (int64, error) {
	_, tgID, err := a.channelID(ctx)
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(tgID, 10, 64)
	if err != nil {
		return 0, apperr.New(apperr.ErrDB, "stored channel id is not numeric: "+tgID)
	}
	return id, nil
}

func (a *App) activePaths(ctx context.Context, channelID int64) ([]fsmodel.ActivePath, error) {
	rows, err := a.DB.ActivePaths(ctx, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list paths", err)
	}
	out := make([]fsmodel.ActivePath, len(rows))
	for i, r := range rows {
		out[i] = fsmodel.ActivePath{Canonical: r.Path, IsDir: r.IsDir}
	}
	return out, nil
}

func computeHash(r io.Reader, enabled bool) (string, error) {
	if !enabled {
		return "", nil
	}
	h := blake3.New(32, nil)
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return "blake3:" + hex.EncodeToString(h.Sum(nil)), nil
}

func detectMIME(path string) string {
	ext := filepath.Ext(path)
	mt := mime.TypeByExtension(ext)
	if mt == "" {
		return "application/octet-stream"
	}
	return mt
}

func (a *App) loadExistingSlugs(ctx context.Context, channelID int64) (map[string]string, error) {
	slugs, err := a.DB.LoadSlugMap(ctx, model.ChannelID(channelID))
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "load slugs", err)
	}
	if slugs == nil {
		return map[string]string{}, nil
	}
	return slugs, nil
}

// loadSlugMap is the best-effort variant of loadExistingSlugs for chain
// generation call sites that treat the persisted cache as advisory.
func (a *App) loadSlugMap(ctx context.Context, channelID int64) map[string]string {
	out, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return map[string]string{}
	}
	return out
}

func (a *App) uploadLimit(ctx context.Context) int64 {
	a.limitMu.Lock()
	defer a.limitMu.Unlock()
	if a.cachedLimit > 0 {
		return a.cachedLimit
	}
	limit := a.Cfg.Limits.FreeUploadBytes
	if tgChID, err := a.tgChannelID(ctx); err == nil {
		if caps, err := a.TG.Doctor(ctx, tgChID); err == nil && caps != nil && caps.MaxUploadBytes > 0 {
			limit = caps.MaxUploadBytes
		}
	}
	a.cachedLimit = limit
	return limit
}

// UploadResult reports one single-file upload. A skipped upload (the
// destination exists under --skip-existing) carries only Path and Skipped.
type UploadResult struct {
	ChannelID         string `json:"channel_id"`
	Hash              string `json:"hash"`
	InviteLink        string `json:"invite_link,omitempty"`
	ManifestMessageID *int   `json:"manifest_message_id"`
	MessageID         int    `json:"message_id"`
	Path              string `json:"path"`
	Resumed           bool   `json:"resumed,omitempty"`
	Size              int64  `json:"size"`
	Skipped           bool   `json:"skipped,omitempty"`
}

// MarshalJSON renders a skipped upload as just its path: nothing was sent, so
// the message fields would be meaningless zeros.
func (r UploadResult) MarshalJSON() ([]byte, error) {
	if r.Skipped {
		return marshalNoEscape(struct {
			Path    string `json:"path"`
			Skipped bool   `json:"skipped"`
		}{r.Path, true})
	}
	type uploaded UploadResult // drops the method, avoiding recursion
	return marshalNoEscape(uploaded(r))
}

// marshalNoEscape matches the output renderer, which disables HTML escaping;
// json.Marshal would escape '&', '<' and '>' in paths, and the outer encoder
// does not undo that for Marshaler output.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// UploadFile uploads a single local file as a plain document.
func (a *App) UploadFile(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool) (*UploadResult, error) {
	return a.uploadFile(ctx, localPath, remotePath, policy, noHash, Presentation{}, "")
}

// uploadFileWithCaption uploads one file keeping humanCaption above the
// rendered caption block. Imports (td import saved) use it to carry the
// source message's own text onto the republished message.
func (a *App) uploadFileWithCaption(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool, pres Presentation, humanCaption string) (*UploadResult, error) {
	return a.uploadFile(ctx, localPath, remotePath, policy, noHash, pres, humanCaption)
}

// UploadFileAs uploads a single local file with presentation metadata that
// selects how native Telegram clients render the message. The zero
// Presentation behaves exactly like UploadFile.
func (a *App) UploadFileAs(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool, pres Presentation) (*UploadResult, error) {
	return a.uploadFile(ctx, localPath, remotePath, policy, noHash, pres, "")
}

func (a *App) uploadFile(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool, pres Presentation, humanCaption string) (*UploadResult, error) {
	if err := pres.Validate(); err != nil {
		return nil, err
	}
	if _, err := a.checkUploadSource(ctx, localPath, "", false); err != nil {
		return nil, err
	}
	// cp convention: a destination that is "/" or ends with "/" is a
	// directory; keep the source file's basename.
	if remotePath == "/" || strings.HasSuffix(remotePath, "/") {
		remotePath = strings.TrimRight(remotePath, "/") + "/" + filepath.Base(localPath)
	}
	dest, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	if dest == "/" {
		return nil, apperr.New(apperr.ErrPathInvalid, "destination must include a file name")
	}
	channelID, tgIDStr, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return nil, err
	}
	// A destination naming an existing remote directory also keeps the
	// source basename.
	for _, ap := range active {
		if ap.Canonical == dest && ap.IsDir {
			dest, err = fsmodel.NormalizeCanonicalPath(dest + "/" + filepath.Base(localPath))
			if err != nil {
				return nil, err
			}
			break
		}
	}

	out, err := a.runUpload(ctx, uploadRun{
		members: []uploadMember{{localPath: localPath, dest: dest, pres: pres, humanCaption: humanCaption}},
		policy:  policy,
		noHash:  noHash,
	})
	if err != nil {
		return nil, err
	}
	if len(out.sent) == 0 {
		return &UploadResult{Path: dest, Skipped: true}, nil
	}
	sent := out.sent[0]
	data := &UploadResult{
		Path:      sent.dest,
		ChannelID: tgIDStr,
		MessageID: sent.messageID,
		Size:      sent.size,
		Hash:      sent.hash,
		Resumed:   sent.resumed,
	}
	if sent.manifestMsgID > 0 {
		data.ManifestMessageID = &sent.manifestMsgID
	}
	if link, err := a.TG.GetInviteLink(ctx, tgChID); err == nil {
		data.InviteLink = link
	}
	return data, nil
}

// uploadLockedArgs describes a file superseded by --replace, for
// retireReplacedFile.
type uploadLockedArgs struct {
	localPath       string
	dest            string
	policy          ConflictPolicy
	size            int64
	channelID       int64
	tgChID          int64
	tgIDStr         string
	replaceFileID   int64
	oldMsgID        sql.NullInt64
	oldManifestID   sql.NullInt64
	oldManifestChat string
	pres            Presentation
	// humanCaption is the source text kept above the rendered caption block
	// (imports only); empty renders the block alone.
	humanCaption string
}

// retireReplacedFile redacts the Telegram records of the file a --replace
// superseded. It is best effort: the replacement is already published and
// indexed, so failures leave stale records for scans rather than failing cp.
func (a *App) retireReplacedFile(ctx context.Context, channelID, tgChID int64, dest, displayName string, args uploadLockedArgs) {
	carrier := a.manifestCarrier(args.oldManifestChat)
	oldMsgID, oldManID := 0, 0
	if args.oldMsgID.Valid {
		oldMsgID = int(args.oldMsgID.Int64)
	}
	if args.oldManifestID.Valid {
		oldManID = int(args.oldManifestID.Int64)
	}
	album, isAlbum, loadErr := a.loadAlbumManifest(ctx, tgChID, carrier, oldManID)
	switch {
	case isAlbum:
		// Album members share one inventory; redacting it would drop every
		// sibling's machine record.
		_, _, _ = a.retireAlbumMember(ctx, channelID, tgChID, carrier, oldManID, album, oldMsgID)
		return
	case loadErr != nil && !isMessageGone(loadErr):
		// The record may be a shared album inventory; leave it untouched and
		// retire the media alone.
		oldManID = 0
	}
	tombstone := func() {
		if oldMsgID > 0 {
			_ = a.TG.EditCaption(ctx, tgChID, oldMsgID, manifest.RenderTombstoneCaption(displayName, dest))
		}
		if oldManID > 0 {
			_ = carrier.Edit(ctx, tgChID, oldManID, manifest.RenderTombstoneManifest(dest))
		}
	}
	if a.Cfg.Delete.Mode == "tombstone" {
		// The caption tombstone outranks a live comment during scans, so it
		// also covers a failed comment edit.
		tombstone()
		return
	}
	if oldMsgID > 0 {
		if err := a.TG.DeleteMessage(ctx, tgChID, oldMsgID); err != nil && !isMessageGone(err) {
			// Retirement must stay sticky even when the media cannot be
			// deleted: tombstone what remains instead.
			tombstone()
			return
		}
	}
	if oldManID > 0 {
		_ = carrier.Delete(ctx, tgChID, oldManID)
	}
}
