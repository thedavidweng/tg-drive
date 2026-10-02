package service

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"lukechampine.com/blake3"
)

// LSEntry is one directory listing entry.
//
// The td ls --json wire shape is contract-tested: file entries always carry
// hash (see MarshalJSON); dir entries keep their historical key set.
type LSEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
	// Hash is the stored BLAKE3 content hash ("blake3:<hex>"); empty when
	// unknown, e.g. rows adopted without --hash. Emitted only for file
	// entries, where it is always present (possibly "") so no-download
	// integrity audits can rely on the key.
	Hash      string `json:"-"`
	Status    string `json:"status,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

// lsFileJSON is the wire shape of a file entry in ls --json output.
type lsFileJSON struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Size      int64  `json:"size,omitempty"`
	Hash      string `json:"hash"`
	Status    string `json:"status,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

// lsDirJSON is the wire shape of a directory entry; it predates the hash
// field and must not grow one.
type lsDirJSON LSEntry

// MarshalJSON pins the ls --json entry contract: file entries always carry
// hash — the stored blake3 value, "" when unknown — while dir entries keep
// their historical key set.
func (e LSEntry) MarshalJSON() ([]byte, error) {
	if e.Type == "file" {
		return json.Marshal(lsFileJSON(e))
	}
	return json.Marshal(lsDirJSON(e))
}

// ListDir lists children of a remote path.
func (a *App) ListDir(ctx context.Context, remotePath string) ([]LSEntry, error) {
	// Trailing slashes are accepted directory intent; canonical paths drop
	// them (fsmodel rejects them outright).
	p, err := fsmodel.NormalizeCanonicalPath(strings.TrimRight(remotePath, "/"))
	if err != nil {
		return nil, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	channelID := ch.rowID
	prefix := p
	if prefix != "/" {
		prefix += "/"
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select canonical_path, display_name, 'file' as type, coalesce(size,0), status, 0, coalesce(content_hash,'')
		from files where channel_id=? and status='active' and canonical_path like ? escape '\'
		union
		select canonical_path, display_name, 'dir', 0, '', ephemeral, ''
		from nodes where channel_id=? and type='dir' and parent_path=?
		order by type desc, display_name`, channelID, escapeLike(prefix)+"%", channelID, p)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "ls", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	var out []LSEntry
	for rows.Next() {
		var fullPath, name, typ, status, contentHash string
		var size int64
		var ephemeral int
		if err := rows.Scan(&fullPath, &name, &typ, &size, &status, &ephemeral, &contentHash); err != nil {
			return nil, err
		}
		childName := name
		if typ == "file" {
			rel := strings.TrimPrefix(fullPath, prefix)
			if strings.Contains(rel, "/") {
				childName = strings.Split(rel, "/")[0]
				typ = "dir"
				fullPath = prefix + childName
			} else {
				childName = name
			}
		}
		if seen[fullPath] {
			continue
		}
		seen[fullPath] = true
		entry := LSEntry{Name: childName, Path: fullPath, Type: typ, Size: size, Status: status, Ephemeral: ephemeral == 1}
		if typ == "file" {
			entry.Hash = contentHash
		}
		out = append(out, entry)
	}
	if len(out) == 0 && p != "/" {
		// Nothing under p: it is either a file (list it, like Unix ls), an
		// empty directory (empty listing), or absent (error).
		row, found, err := a.DB.ActiveByPath(ctx, channelID, p)
		switch {
		case err != nil:
			return nil, apperr.Wrap(apperr.ErrDB, "ls", err)
		case found:
			return []LSEntry{{Name: row.DisplayName, Path: p, Type: "file", Size: row.Size.Int64, Hash: row.ContentHash.String, Status: row.Status}}, nil
		default:
			var one int
			dirErr := a.DB.Raw().QueryRowContext(ctx, `select 1 from nodes where channel_id=? and canonical_path=? and type='dir'`, channelID, p).Scan(&one)
			if dirErr == sql.ErrNoRows {
				return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
			}
			if dirErr != nil {
				return nil, apperr.Wrap(apperr.ErrDB, "ls", dirErr)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// escapeLike escapes SQLite LIKE wildcards so path segments containing
// '_' or '%' match literally (queries use ESCAPE '\').
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// TreeNode is a tree entry.
type TreeNode struct {
	Path     string     `json:"path"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Children []TreeNode `json:"children,omitempty"`
}

// Tree builds a directory tree up to maxDepth.
func (a *App) Tree(ctx context.Context, remotePath string, maxDepth int) ([]TreeNode, error) {
	entries, err := a.ListDir(ctx, remotePath)
	if err != nil {
		return nil, err
	}
	if maxDepth == 0 {
		maxDepth = 32
	}
	var nodes []TreeNode
	for _, e := range entries {
		node := TreeNode{Path: e.Path, Name: e.Name, Type: e.Type}
		if e.Type == "dir" && maxDepth > 1 {
			children, err := a.Tree(ctx, e.Path, maxDepth-1)
			if err != nil {
				return nil, err
			}
			node.Children = children
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// StatusResult reports auth, binding, and index health. Authenticated is nil
// when Telegram was unreachable. The index fields are nil until a channel is
// bound (Initialized false).
type StatusResult struct {
	Authenticated        *bool          `json:"authenticated,omitempty"`
	Channel              *BoundChannel  `json:"channel,omitempty"`
	ChannelID            string         `json:"channel_id,omitempty"`
	Channels             []BoundChannel `json:"channels"`
	DBPath               string         `json:"db_path"`
	DisplayName          *string        `json:"display_name,omitempty"`
	Files                map[string]int `json:"files,omitzero"`
	Initialized          bool           `json:"initialized"`
	LastFullScanAt       *string        `json:"last_full_scan_at,omitempty"`
	LastScanAt           *string        `json:"last_scan_at,omitempty"`
	LastScannedMessageID *int           `json:"last_scanned_message_id,omitempty"`
	Orphaned             *int           `json:"orphaned,omitempty"`
	ScanErrorsPending    *int           `json:"scan_errors_pending,omitempty"`
	StaleLocks           *int           `json:"stale_locks,omitempty"`
	StalePending         *int           `json:"stale_pending,omitempty"`
	UploadLimitBytes     *int64         `json:"upload_limit_bytes,omitempty"`
	UploadStates         *int           `json:"upload_states,omitempty"`
	UserID               int64          `json:"user_id,omitempty"`
}

// Status returns index statistics.
func (a *App) Status(ctx context.Context) (*StatusResult, error) {
	channels, err := a.boundChannels(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		// Before init there is nothing to count, but auth and paths are
		// still worth reporting; an explicit --channel that matches nothing
		// stays an error.
		if ae, ok := apperr.As(err); ok && ae.Code == apperr.ErrChannelNotFound && a.channelSelector(ctx) == "" {
			out := &StatusResult{
				Initialized: false,
				Channels:    channels,
				DBPath:      a.Cfg.Storage.DBPath,
			}
			a.statusAuth(ctx, out)
			return out, nil
		}
		return nil, err
	}
	channelID, tgID := ch.rowID, ch.tgIDStr
	counts := map[string]int{}
	rows, err := a.DB.Raw().QueryContext(ctx, `select status, count(*) from files where channel_id=? group by status`, channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status string
		var n int
		_ = rows.Scan(&status, &n)
		counts[status] = n
	}
	out := &StatusResult{
		Initialized: true,
		ChannelID:   tgID,
		Channels:    channels,
		Files:       counts,
		DBPath:      a.Cfg.Storage.DBPath,
	}
	for _, ch := range channels {
		if ch.ChannelID == tgID {
			out.Channel = &ch
		}
	}
	a.statusAuth(ctx, out)
	var lastScan, lastFull string
	var lastMsgID int
	_ = a.DB.Raw().QueryRowContext(ctx, `
		select coalesce(last_scanned_message_id,0), coalesce(last_full_scan_at,''), coalesce(updated_at,'')
		from scan_state where channel_id=?`, channelID).Scan(&lastMsgID, &lastFull, &lastScan)
	out.LastScannedMessageID = &lastMsgID
	out.LastFullScanAt = &lastFull
	out.LastScanAt = &lastScan
	var scanErrors int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from scan_errors where channel_id=? and status='pending'`, channelID).Scan(&scanErrors)
	out.ScanErrorsPending = &scanErrors
	nowT := time.Now().UTC()
	staleCutoff := nowT.Add(-time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second).Format(time.RFC3339)
	var stalePending int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from files where channel_id=? and status='pending' and updated_at < ?`, channelID, staleCutoff).Scan(&stalePending)
	out.StalePending = &stalePending
	// Persisted resumable-upload states: abandoned attempts are collectable
	// via td repair --pending.
	if uploadStates, err := a.DB.CountUploadStates(ctx); err == nil {
		out.UploadStates = &uploadStates
	}
	var staleLocks int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from operation_locks where expires_at < ?`, nowT.Format(time.RFC3339)).Scan(&staleLocks)
	orphaned := counts["orphaned"]
	uploadLimit := a.uploadLimit(ctx)
	out.StaleLocks = &staleLocks
	out.Orphaned = &orphaned
	out.UploadLimitBytes = &uploadLimit
	return out, nil
}

// BoundChannel is one channel bound to a local root by td init.
type BoundChannel struct {
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	LocalRoot string `json:"local_root"`
}

func (a *App) boundChannels(ctx context.Context) ([]BoundChannel, error) {
	rows, err := a.DB.Raw().QueryContext(ctx, `select tg_channel_id, title, coalesce(root_local_path,'') from channels order by id`)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list channels", err)
	}
	defer func() { _ = rows.Close() }()
	out := []BoundChannel{}
	for rows.Next() {
		var ch BoundChannel
		if err := rows.Scan(&ch.ChannelID, &ch.Title, &ch.LocalRoot); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "list channels", err)
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

func (a *App) statusAuth(ctx context.Context, out *StatusResult) {
	user, ok, err := a.TG.Status(ctx)
	if err != nil {
		return
	}
	out.Authenticated = &ok
	if ok && user != nil {
		out.UserID = user.ID
		out.DisplayName = &user.DisplayName
	}
}

// DownloadResult reports what DownloadFile actually did.
type DownloadResult struct {
	Path    string `json:"path"`    // remote canonical path
	Dest    string `json:"local"`   // local destination actually written
	Size    int64  `json:"size"`    // remote size in bytes
	Skipped bool   `json:"skipped"` // destination existed and --skip-existing was set
}

// DownloadOptions are one download call's own settings. The zero value
// reports nothing.
type DownloadOptions struct {
	// Observer receives this call's stages, byte progress, and per-file
	// results.
	Observer Observer
}

// DownloadFile downloads a remote file to local path, streaming through a
// temp file and verifying size/hash before the atomic rename.
func (a *App) DownloadFile(ctx context.Context, remotePath, localDest string, policy ConflictPolicy, opts DownloadOptions) (*DownloadResult, error) {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	row, found, err := a.DB.ActiveByPath(ctx, ch.rowID, p)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	if !found {
		return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
	}
	obs := opts.Observer
	it := Item{Source: localDest, Path: p}
	res, err := a.downloadRow(ctx, ch, row, &it, policy, obs)
	switch {
	case err != nil:
		obs.done(it, err)
	case res.Skipped:
		obs.item(ItemResult{Item: it, Status: ItemSkipped})
	default:
		obs.done(it, nil)
	}
	return res, err
}

// downloadRow downloads one resolved active row to it.Source, which it
// rewrites to the local file the download actually targets.
func (a *App) downloadRow(ctx context.Context, ch *channelContext, row sqlitestore.FileRow, it *Item, policy ConflictPolicy, obs Observer) (*DownloadResult, error) {
	p := it.Path
	if !row.MessageID.Valid {
		return nil, apperr.New(apperr.ErrDB, fmt.Sprintf("lookup file: active row for %q has no message", p))
	}
	messageID, size, hash := int(row.MessageID.Int64), row.Size.Int64, row.ContentHash.String
	localDest := it.Source
	// cp convention: a destination that ends with a separator or names an
	// existing directory keeps the remote file's basename.
	if strings.HasSuffix(localDest, "/") || strings.HasSuffix(localDest, string(os.PathSeparator)) {
		localDest = filepath.Join(localDest, fsmodel.BaseName(p))
	} else if info, err := a.files().Stat(ctx, localDest); err == nil && info.IsDir {
		localDest = filepath.Join(localDest, fsmodel.BaseName(p))
	}
	it.Source = localDest
	if _, err := a.files().Stat(ctx, localDest); err == nil {
		switch policy {
		case ConflictSkip:
			return &DownloadResult{Path: p, Dest: localDest, Size: size, Skipped: true}, nil
		case ConflictReplace:
		case ConflictRename:
			localDest = autoRenameLocal(ctx, a.files(), localDest)
			it.Source = localDest
		default:
			return nil, apperr.New(apperr.ErrLocalPathExists,
				fmt.Sprintf("local file %q already exists (use --replace, --skip-existing, or --auto-rename)", localDest))
		}
	}
	if err := a.files().MkdirAll(ctx, filepath.Dir(localDest), 0o755); err != nil {
		return nil, err
	}
	obs.stage(*it, StageDownloading)
	tmp, f, err := a.files().CreateTemp(ctx, localDest)
	if err != nil {
		return nil, err
	}
	hashEnabled := hash != "" && a.Cfg.Hash.Enabled && strings.HasPrefix(hash, "blake3:")
	progress := &progressWriter{obs: obs, item: *it, total: size}
	writers := []io.Writer{f}
	var h *blake3.Hasher
	if hashEnabled {
		h = blake3.New(32, nil)
		writers = append(writers, h)
	}
	nativePhoto, err := a.downloadTo(ctx, ch.tgID, messageID, io.MultiWriter(append(writers, progress)...), progress)
	if err != nil {
		_ = f.Close()
		_ = a.files().Remove(ctx, tmp)
		return nil, telegram.MapError(err)
	}
	if err := f.Close(); err != nil {
		_ = a.files().Remove(ctx, tmp)
		return nil, err
	}
	// Native photos are Telegram's own recompressed representations: the
	// stored size/hash describe the original upload bytes, which the platform
	// never serves back (docs/integration-notes.md). Documents and attributed
	// videos keep strict verification because their bytes are untouched.
	if !nativePhoto {
		if size > 0 {
			info, err := a.files().Stat(ctx, tmp)
			if err != nil {
				_ = a.files().Remove(ctx, tmp)
				return nil, err
			}
			if info.Size != size {
				_ = a.files().Remove(ctx, tmp)
				return nil, apperr.New(apperr.ErrTelegramRPC, "size mismatch")
			}
		}
		if hashEnabled {
			got := "blake3:" + hex.EncodeToString(h.Sum(nil))
			if got != hash {
				_ = a.files().Remove(ctx, tmp)
				return nil, apperr.New(apperr.ErrTelegramRPC, "content hash mismatch")
			}
		}
	}
	if err := a.files().Rename(ctx, tmp, localDest); err != nil {
		_ = a.files().Remove(ctx, tmp)
		return nil, err
	}
	return &DownloadResult{Path: p, Dest: localDest, Size: size}, nil
}

// downloadTo streams the message's downloadable body. It reports whether the
// message is a native photo: Telegram serves its own recompressed
// representation for photos, so the stored size/hash of the original bytes
// cannot hold for what comes back, and progress has no known total.
func (a *App) downloadTo(ctx context.Context, tgChID int64, messageID int, w io.Writer, progress *progressWriter) (bool, error) {
	nativePhoto := false
	if msg, err := a.TG.GetMessage(ctx, tgChID, messageID); err == nil {
		if msg.Kind == telegram.KindText || (msg.MIME == "text/plain" && len(msg.Data) == 0 && msg.FileName == "") {
			body := msg.Text
			if body == "" {
				body = msg.Caption
			}
			_, err := w.Write([]byte(manifest.SplitHumanAndMachine(body)))
			return false, err
		}
		nativePhoto = msg.Kind == telegram.KindPhoto
	}
	if nativePhoto {
		progress.total = 0
	}
	err := a.TG.DownloadMedia(ctx, tgChID, messageID, w)
	return nativePhoto, err
}

func autoRenameLocal(ctx context.Context, files ports.FileSystem, path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	for i := 1; i < 1000; i++ {
		candidate := filepath.Join(dir, fsmodel.ConflictRenameCandidate(base, i))
		if _, err := files.Stat(ctx, candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate
		}
	}
	return path
}

// MoveOptions controls td mv behavior.
type MoveOptions struct {
	// Confirm is the ADR 0003 confirmation a move requires.
	Confirm bool
}

// Validate rejects an unconfirmed move. MoveFile applies it first; front ends
// may call it before opening anything so the gate fails fast.
func (o MoveOptions) Validate() error {
	if !o.Confirm {
		return apperr.New(apperr.ErrConfirmationRequired, "moving a remote file requires --confirm")
	}
	return nil
}

// MoveFile moves or renames a remote file within the configured channel.
func (a *App) MoveFile(ctx context.Context, from, to string, opts MoveOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	src, err := fsmodel.NormalizeCanonicalPath(from)
	if err != nil {
		return err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return err
	}
	channelID := ch.rowID
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return err
	}
	if fsmodel.IsDirectorySource(src, active) {
		return apperr.New(apperr.ErrDirectoryMoveUnsupported,
			fmt.Sprintf("%s is a directory; td mv moves single files (move each file, e.g. td mv %s/<file> <dest>/)", src, src)).
			WithDetails(map[string]any{"path": src})
	}
	dst, err := fsmodel.MoveDestination(src, to, active)
	if err != nil {
		return err
	}
	if dst == src {
		return nil
	}
	// Validate the destination before any Telegram mutation.
	remaining := make([]fsmodel.ActivePath, 0, len(active))
	for _, ap := range active {
		if !ap.IsDir && ap.Canonical == src {
			continue
		}
		remaining = append(remaining, ap)
	}
	if err := fsmodel.CheckUploadConflict(dst, remaining); err != nil {
		return err
	}
	srcRow, found, err := a.DB.ActiveByPath(ctx, channelID, src)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "lookup source", err)
	}
	if !found {
		var otherChannel int64
		if scanErr := a.DB.Raw().QueryRowContext(ctx, `select channel_id from files where canonical_path=? and status='active' and channel_id != ? limit 1`,
			src, channelID).Scan(&otherChannel); scanErr == nil {
			return apperr.New(apperr.ErrCrossChannelMove, "source file belongs to a different channel; V1 supports same-channel moves only")
		}
		return apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", src))
	}
	// Path locks (sorted) with heartbeat renewal: a move touching two paths
	// holds both for the whole operation regardless of duration.
	return a.operate(ctx, ch, []string{src, dst}, func(ctx context.Context) error {
		return a.fileRecord(channelID, ch.tgID, srcRow).Rename(ctx, dst)
	})
}

// DeleteOptions controls td rm behavior.
type DeleteOptions struct {
	// Tombstone forces tombstone mode regardless of configured delete.mode.
	Tombstone bool
	// AllowStaleManifest downgrades a failed manifest redaction/removal to a
	// warning instead of an error.
	AllowStaleManifest bool
	// Confirm is the ADR 0003 confirmation a delete requires.
	Confirm bool
}

// Validate rejects an unconfirmed delete. DeleteFile applies it first; front
// ends may call it before opening anything so the gate fails fast.
func (o DeleteOptions) Validate() error {
	if !o.Confirm {
		return apperr.New(apperr.ErrConfirmationRequired, "deleting a remote file requires --confirm")
	}
	return nil
}

// DeleteResult reports how a file was removed. StaleManifest marks a
// manifest reply that could not be redacted.
type DeleteResult struct {
	Mode          string `json:"mode"`
	Path          string `json:"path"`
	StaleManifest bool   `json:"stale_manifest,omitempty"`
}

// DeleteFile removes a remote file according to the delete policy.
func (a *App) DeleteFile(ctx context.Context, remotePath string, opts DeleteOptions) (*DeleteResult, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	active, err := a.activePaths(ctx, ch.rowID)
	if err != nil {
		return nil, err
	}
	for _, ap := range active {
		if ap.IsDir && ap.Canonical == p {
			return nil, apperr.New(apperr.ErrDirectoryDeleteUnsupported,
				fmt.Sprintf("%s is a directory; td rm deletes single files (list them with td ls %s)", p, p)).
				WithDetails(map[string]any{"path": p})
		}
	}
	row, found, err := a.DB.ActiveByPath(ctx, ch.rowID, p)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	if !found {
		return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
	}
	// Hold the path lock (with heartbeat renewal) for the whole delete: the
	// Telegram mutations and the index commit must be exclusive.
	var out *DeleteResult
	lockErr := a.operate(ctx, ch, []string{p}, func(ctx context.Context) error {
		res, err := a.deleteFileLocked(ctx, ch.rowID, ch.tgID, row, opts)
		if err != nil {
			return err
		}
		out = res
		return nil
	})
	if lockErr != nil {
		return nil, lockErr
	}
	return out, nil
}

func (a *App) deleteFileLocked(ctx context.Context, channelID, tgChID int64, row sqlitestore.FileRow, opts DeleteOptions) (*DeleteResult, error) {
	mode := a.Cfg.Delete.Mode
	if opts.Tombstone {
		mode = "tombstone"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	retired, err := a.fileRecord(channelID, tgChID, row).Retire(ctx, mode)
	if err != nil {
		return nil, err
	}
	// The media mutation already succeeded (or the message was already
	// gone), so the row is marked deleted even when the record could not be
	// redacted or recorded; those failures surface afterwards.
	if err := a.DB.MarkDeleted(ctx, row.ID, now); err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "mark deleted", err)
	}
	if err := a.DB.RunDirectoryGC(ctx, channelID); err != nil {
		return nil, err
	}
	if retired.recordErr != nil {
		return nil, retired.recordErr
	}
	out := &DeleteResult{Path: row.CanonicalPath, Mode: retired.mode, StaleManifest: retired.stale != nil}
	if err := retired.staleErr(opts.AllowStaleManifest); err != nil {
		return out, err
	}
	return out, nil
}

// RecursiveUploadResult reports a directory upload. ChannelID accompanies
// InviteLink and is omitted with it.
type RecursiveUploadResult struct {
	Albums     []AlbumGroup `json:"albums"`
	ChannelID  string       `json:"channel_id,omitempty"`
	Errors     []string     `json:"errors"`
	Failed     int          `json:"failed"`
	InviteLink string       `json:"invite_link,omitempty"`
	Skipped    int          `json:"skipped"`
	Uploaded   int          `json:"uploaded"`
}

// UploadRecursive uploads a directory recursively. Files are published as
// native media groups: each source directory's direct children form one
// album, split into consecutive groups of MaxMediaGroupMembers (issue #26).
func (a *App) UploadRecursive(ctx context.Context, localDir, remoteDir string, policy ConflictPolicy, continueOnError, noHash, includeEmptyDirs bool, opts UploadOptions) (*RecursiveUploadResult, error) {
	if err := opts.Validate(policy); err != nil {
		return nil, err
	}
	if includeEmptyDirs {
		return nil, apperr.New(apperr.ErrEmptyDirsUnsupported, "empty directories cannot be persisted to Telegram in V1")
	}
	// Trailing slashes are directory intent; the canonical form drops them.
	remoteDir, err := fsmodel.NormalizeCanonicalPath(strings.TrimRight(remoteDir, "/"))
	if err != nil {
		return nil, err
	}
	info, err := a.files().Stat(ctx, localDir)
	if err != nil {
		return nil, apperr.New(apperr.ErrLocalNotFound, fmt.Sprintf("local directory %q not found", localDir))
	}
	if !info.IsDir {
		return nil, apperr.New(apperr.ErrUsage, "recursive upload requires a directory source")
	}
	var files []string
	err = a.files().Walk(ctx, localDir, func(path string, info ports.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrLocalNotFound, "walk local directory", err)
	}
	sort.Strings(files)

	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}

	uploaded, skipped, failed := 0, 0, 0
	var errs []string
	albums := []AlbumGroup{}
	// Each source directory's children become one album batch.
	groups := map[string][]uploadMember{}
	order := []string{}
	for _, f := range files {
		dir := filepath.Dir(f)
		if _, ok := groups[dir]; !ok {
			order = append(order, dir)
		}
		rel, _ := filepath.Rel(localDir, f)
		dest := remoteDir
		if dest != "/" {
			dest += "/"
		}
		dest += filepath.ToSlash(rel)
		groups[dir] = append(groups[dir], uploadMember{localPath: f, dest: dest})
	}
	sort.Strings(order)
	for _, dir := range order {
		if err := cancelled(ctx); err != nil {
			return nil, err
		}
		out, err := a.runUpload(ctx, uploadRun{members: groups[dir], policy: policy, noHash: noHash, album: true, lenient: continueOnError, opts: opts})
		if out != nil {
			skipped += len(out.skipped)
			failed += len(out.failures)
			for _, f := range out.failures {
				errs = append(errs, f.String())
			}
		}
		if err != nil {
			if stopsRun(ctx, err) {
				return nil, apperr.Cancelled()
			}
			failed++
			errs = append(errs, err.Error())
			if !continueOnError {
				return nil, err
			}
			continue
		}
		uploaded += len(out.sent)
		albums = append(albums, out.albums...)
	}

	data := &RecursiveUploadResult{Uploaded: uploaded, Skipped: skipped, Failed: failed, Errors: errs, Albums: albums}
	if link, err := a.TG.GetInviteLink(ctx, ch.tgID); err == nil && link != "" {
		data.InviteLink = link
		data.ChannelID = fmt.Sprintf("%d", ch.tgID)
	}
	return data, nil
}

// RecursiveDownloadResult reports a directory download.
type RecursiveDownloadResult struct {
	Downloaded int      `json:"downloaded"`
	Errors     []string `json:"errors"`
	Failed     int      `json:"failed"`
	Local      string   `json:"local"`
	Path       string   `json:"path"`
	Skipped    int      `json:"skipped"`
}

// DownloadRecursive downloads a directory tree and reports per-file results.
func (a *App) DownloadRecursive(ctx context.Context, remotePath, localDir string, policy ConflictPolicy, continueOnError bool, opts DownloadOptions) (*RecursiveDownloadResult, error) {
	st := &downloadStats{}
	if err := a.downloadRecursive(ctx, remotePath, localDir, policy, continueOnError, opts, st); err != nil {
		return nil, err
	}
	return &RecursiveDownloadResult{
		Downloaded: st.downloaded,
		Errors:     st.errors,
		Failed:     st.failed,
		Local:      localDir,
		Path:       remotePath,
		Skipped:    st.skipped,
	}, nil
}

type downloadStats struct {
	downloaded int
	skipped    int
	failed     int
	errors     []string
}

func (a *App) downloadRecursive(ctx context.Context, remotePath, localDir string, policy ConflictPolicy, continueOnError bool, opts DownloadOptions, st *downloadStats) error {
	entries, err := a.ListDir(ctx, remotePath)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := cancelled(ctx); err != nil {
			return err
		}
		localPath := filepath.Join(localDir, e.Name)
		if e.Type == "dir" {
			if err := a.files().MkdirAll(ctx, localPath, 0o755); err != nil {
				st.failed++
				st.errors = append(st.errors, err.Error())
				if !continueOnError {
					return err
				}
				continue
			}
			if err := a.downloadRecursive(ctx, e.Path, localPath, policy, continueOnError, opts, st); err != nil && (!continueOnError || stopsRun(ctx, err)) {
				return err
			}
			continue
		}
		res, err := a.DownloadFile(ctx, e.Path, localPath, policy, opts)
		if err != nil {
			if stopsRun(ctx, err) {
				return apperr.Cancelled()
			}
			st.failed++
			st.errors = append(st.errors, err.Error())
			if !continueOnError {
				return err
			}
			continue
		}
		if res.Skipped {
			st.skipped++
			continue
		}
		st.downloaded++
	}
	return nil
}
