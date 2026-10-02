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
	"strings"
	"sync"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/localfs"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
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
	// It is the default for every call; WithChannel overrides it per call.
	Channel string
	Render  func() bool // returns json mode
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
	if ch, err := a.channel(ctx); err == nil {
		if caps, err := a.TG.Doctor(ctx, ch.tgID); err == nil && caps != nil && caps.MaxUploadBytes > 0 {
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

// UploadOptions are one upload call's own settings. The zero value uses the
// configured transfer settings and reports nothing.
type UploadOptions struct {
	// Threads overrides upload.threads for this call when positive.
	Threads int
	// PartSizeKB overrides upload.part_size_kb for this call when positive.
	PartSizeKB int
	// Observer receives this call's stages, byte progress, and per-file
	// results.
	Observer Observer
	// ConfirmReplace is the ADR 0003 confirmation that ConflictReplace
	// requires.
	ConfirmReplace bool
}

// Validate rejects an unconfirmed replace. The upload use cases apply it
// first; front ends may call it before opening anything so the gate fails
// fast.
func (o UploadOptions) Validate(policy ConflictPolicy) error {
	if policy == ConflictReplace && !o.ConfirmReplace {
		return apperr.New(apperr.ErrConfirmationRequired, "replacing an existing remote file requires --confirm")
	}
	return nil
}

func (o UploadOptions) threads(cfg config.Config) int {
	if o.Threads > 0 {
		return o.Threads
	}
	if cfg.Upload.Threads > 0 {
		return cfg.Upload.Threads
	}
	return 4
}

func (o UploadOptions) partSizeBytes(cfg config.Config) int {
	if o.PartSizeKB > 0 {
		return o.PartSizeKB * 1024
	}
	return cfg.Upload.PartSizeKB * 1024
}

// UploadFile uploads a single local file as a plain document.
func (a *App) UploadFile(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool, opts UploadOptions) (*UploadResult, error) {
	if err := opts.Validate(policy); err != nil {
		return nil, err
	}
	return a.uploadFile(ctx, localPath, remotePath, policy, noHash, Presentation{}, "", opts)
}

// uploadFileWithCaption uploads one file keeping humanCaption above the
// rendered caption block. Imports (td import saved) use it to carry the
// source message's own text onto the republished message.
func (a *App) uploadFileWithCaption(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool, pres Presentation, humanCaption string, opts UploadOptions) (*UploadResult, error) {
	return a.uploadFile(ctx, localPath, remotePath, policy, noHash, pres, humanCaption, opts)
}

// UploadFileAs uploads a single local file with presentation metadata that
// selects how native Telegram clients render the message. The zero
// Presentation and UploadOptions behave exactly like UploadFile.
func (a *App) UploadFileAs(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool, pres Presentation, opts UploadOptions) (*UploadResult, error) {
	if err := opts.Validate(policy); err != nil {
		return nil, err
	}
	return a.uploadFile(ctx, localPath, remotePath, policy, noHash, pres, "", opts)
}

func (a *App) uploadFile(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool, pres Presentation, humanCaption string, opts UploadOptions) (*UploadResult, error) {
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
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	active, err := a.activePaths(ctx, ch.rowID)
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
		opts:    opts,
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
		ChannelID: ch.tgIDStr,
		MessageID: sent.messageID,
		Size:      sent.size,
		Hash:      sent.hash,
		Resumed:   sent.resumed,
	}
	if sent.manifestMsgID > 0 {
		data.ManifestMessageID = &sent.manifestMsgID
	}
	if link, err := a.TG.GetInviteLink(ctx, ch.tgID); err == nil {
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
	a.fileRecord(channelID, tgChID, sqlitestore.FileRow{
		ID:            args.replaceFileID,
		CanonicalPath: dest,
		DisplayName:   displayName,
		MessageID:     args.oldMsgID,
		ManifestMsgID: args.oldManifestID,
		ManifestChat:  args.oldManifestChat,
	}).RetireSuperseded(ctx, a.Cfg.Delete.Mode)
}
