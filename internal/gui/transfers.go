//go:build gui

package gui

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/transfer"
)

// Transfers is the facade service over the Transfer Manager (ADR 0033):
// every GUI upload and download runs as a Transfer, and the Transfers tab
// watches them all, including the ones other front ends run. The facade
// holds no Transfer state of its own beyond the last reported snapshot the
// event stream dedupes against; the shared index is the record.
type Transfers struct {
	state *appState
	pick  FilePicker

	mu sync.Mutex
	// emit is the typed-event sink SetTransferEmitter connects.
	emit Emitter
	// seen is the last reported state per Transfer ID. The Manager's
	// Observer (this process's Transfers) and the index sync (everyone's)
	// both feed note, and seen dedupes the overlap.
	seen map[string]Transfer
}

// FilePicker opens the native file dialogs. cmd/td-gui connects the Wails
// dialog manager (a scripted picker in server mode); tests connect a fake.
// An empty answer is the dialog cancelled.
type FilePicker interface {
	// PickFiles asks for one or more local files.
	PickFiles(ctx context.Context) ([]string, error)
	// PickDirectory asks for one local directory.
	PickDirectory(ctx context.Context) (string, error)
}

// Transfer is one Transfer as the frontend sees it: the index record with
// RFC3339 timestamps.
type Transfer struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Stage string `json:"stage"`
	// Channel is the Telegram ID of the drive channel the Transfer works
	// on.
	Channel string `json:"channel"`
	Source  string `json:"source"`
	Dest    string `json:"dest"`
	// BytesDone and BytesTotal track single-file Transfers; multi-item
	// kinds count items instead.
	BytesDone  int64 `json:"bytes_done"`
	BytesTotal int64 `json:"bytes_total"`
	ItemsDone  int   `json:"items_done"`
	ItemsTotal int   `json:"items_total"`
	// ItemsFailed is omitted when nothing failed.
	ItemsFailed int `json:"items_failed,omitempty"`
	// ErrorCode and ErrorMessage are the failure a failed Transfer ended
	// with: the envelope's code and its plain-language message.
	ErrorCode       string `json:"error_code,omitempty"`
	ErrorMessage    string `json:"error_message,omitempty"`
	FrontEnd        string `json:"front_end"`
	CancelRequested bool   `json:"cancel_requested"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	// FinishedAt is empty while the Transfer runs.
	FinishedAt string `json:"finished_at,omitempty"`
}

func transferDTO(t transfer.Transfer) Transfer {
	dto := Transfer{
		ID: t.ID, Kind: string(t.Kind), Stage: string(t.Stage),
		Channel: t.Channel, Source: t.Source, Dest: t.Dest,
		BytesDone: t.BytesDone, BytesTotal: t.BytesTotal,
		ItemsDone: t.ItemsDone, ItemsTotal: t.ItemsTotal, ItemsFailed: t.ItemsFailed,
		ErrorCode: t.ErrorCode, ErrorMessage: t.ErrorMessage,
		FrontEnd: string(t.FrontEnd), CancelRequested: t.CancelRequested,
		CreatedAt: t.CreatedAt.Format(time.RFC3339), UpdatedAt: t.UpdatedAt.Format(time.RFC3339),
	}
	if t.FinishedAt != nil {
		dto.FinishedAt = t.FinishedAt.Format(time.RFC3339)
	}
	return dto
}

// TransferList is the Transfers tab's data: the active Transfers above the
// history of the last 30 days (the index prunes older terminal Transfers),
// each newest first.
type TransferList struct {
	Active  []Transfer `json:"active"`
	History []Transfer `json:"history"`
}

// List returns every Transfer in the index, split into active and history.
// Reading marks Transfers whose owner vanished interrupted, as every
// Manager reader does.
func (t *Transfers) List(ctx context.Context) (*TransferList, error) {
	list, err := t.manager().List(ctx, transfer.Filter{All: true})
	if err != nil {
		return nil, toError(err)
	}
	out := &TransferList{Active: []Transfer{}, History: []Transfer{}}
	t.mu.Lock()
	for _, tr := range list {
		dto := transferDTO(tr)
		t.seen[dto.ID] = dto
		if tr.Stage.Terminal() {
			out.History = append(out.History, dto)
		} else {
			out.Active = append(out.Active, dto)
		}
	}
	t.mu.Unlock()
	return out, nil
}

// UploadOptions are the upload sheet's choices. They map one-to-one onto
// the service's upload options: the conflict policy and the replace
// confirmation (service.UploadOptions.Validate), the presentation kind and
// caption (service.Presentation, UploadOptions.Caption), and the recursive
// switches of td cp --recursive.
type UploadOptions struct {
	// Policy is the remote conflict policy: "skip", "replace", or "fail"
	// (the zero value, matching cp without flags). The CLI-only rename
	// policy is not offered.
	Policy string `json:"policy"`
	// ConfirmReplace is the ADR 0003 confirmation Policy "replace"
	// requires.
	ConfirmReplace bool `json:"confirm_replace,omitempty"`
	// Kind is the Telegram presentation: "document", "photo", or "video"
	// ("" is the document default). Folder uploads take no presentation.
	Kind string `json:"kind,omitempty"`
	// Caption is human text rendered above the caption block: on the file's
	// own message for one file, on the first member of an album. Folder
	// uploads take no caption.
	Caption string `json:"caption,omitempty"`
	// NoHash skips content hashing (cp --no-hash).
	NoHash bool `json:"no_hash,omitempty"`
	// ContinueOnError lets a folder upload continue past failed files
	// (cp --continue-on-error).
	ContinueOnError bool `json:"continue_on_error,omitempty"`
	// IncludeEmptyDirs asks a folder upload to create empty directories (cp
	// --include-empty-dirs); the service answers with its typed V1 error.
	IncludeEmptyDirs bool `json:"include_empty_dirs,omitempty"`
}

// DownloadOptions are the download sheet's choices.
type DownloadOptions struct {
	// Policy is the local conflict policy: "skip", "replace", or "fail"
	// (the zero value, matching get without flags).
	Policy string `json:"policy"`
}

// conflictPolicy parses a sheet's policy choice; the zero value is fail,
// the CLI default without flags.
func conflictPolicy(s string) (service.ConflictPolicy, error) {
	switch s {
	case "", string(service.ConflictFail):
		return service.ConflictFail, nil
	case string(service.ConflictSkip):
		return service.ConflictSkip, nil
	case string(service.ConflictReplace):
		return service.ConflictReplace, nil
	default:
		return "", apperr.New(apperr.ErrUsage,
			"unknown conflict policy "+strconv.Quote(s)+" (want skip, replace, or fail)")
	}
}

// Upload uploads local files and directories into the remote directory
// dest (the one the Drive tab shows), and returns the new Transfers' IDs.
// One file alone is a single-file Transfer; several files together are one
// album Transfer; each directory is its own recursive Transfer, landing
// under dest by its name. The mistakes the upload use cases would reject
// mid-run — an unconfirmed replace, an album replace, an unknown policy or
// presentation, presentation on a folder — fail fast here, before any
// Transfer exists.
//
// The Transfer outlives the call: the bound method's ctx ends when Upload
// returns, so the Transfer runs under a ctx bounded only by the process.
func (t *Transfers) Upload(ctx context.Context, paths []string, dest string, opts UploadOptions) ([]string, error) {
	if len(paths) == 0 {
		return nil, toError(apperr.New(apperr.ErrUsage, "nothing to upload"))
	}
	policy, err := conflictPolicy(opts.Policy)
	if err != nil {
		return nil, toError(err)
	}
	svcOpts := service.UploadOptions{ConfirmReplace: opts.ConfirmReplace, Caption: opts.Caption}
	if err := svcOpts.Validate(policy); err != nil {
		return nil, toError(err)
	}
	pres := service.Presentation{Kind: opts.Kind}
	if err := pres.Validate(); err != nil {
		return nil, toError(err)
	}
	var files, dirs []string
	for _, p := range paths {
		// A path that does not stat counts as a file, so the upload use
		// case reports it with its own error precedence.
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			dirs = append(dirs, p)
		} else {
			files = append(files, p)
		}
	}
	// As with cp, presentation belongs to file uploads; a folder upload
	// has none.
	if len(dirs) > 0 && (opts.Kind != "" || opts.Caption != "") {
		return nil, toError(apperr.New(apperr.ErrUsage,
			"presentation and caption apply to file uploads, not folder uploads"))
	}
	// The album use case rejects replace (UploadFilesAs); fail fast so the
	// mistake never becomes a Transfer.
	if len(files) > 1 && policy == service.ConflictReplace {
		return nil, toError(apperr.New(apperr.ErrUsage,
			"album uploads cannot replace existing files; upload them one at a time or remove the existing file first"))
	}
	m := t.manager()
	run := t.runContext(ctx)
	var ids []string
	switch {
	case len(files) == 1:
		h, err := m.SubmitUpload(run, transfer.Upload{
			Source: files[0], Dest: joinRemote(dest, filepath.Base(files[0])),
			Policy: policy, NoHash: opts.NoHash, Presentation: pres, Options: svcOpts,
		})
		if err != nil {
			return nil, toError(err)
		}
		ids = append(ids, h.ID())
	case len(files) > 1:
		h, err := m.SubmitAlbumUpload(run, transfer.AlbumUpload{
			// Directory intent: an album's destination is always the
			// directory the Drive tab shows, never a file path.
			Sources: files, Dest: remoteDir(dest),
			Policy: policy, NoHash: opts.NoHash, Presentation: pres, Options: svcOpts,
		})
		if err != nil {
			return nil, toError(err)
		}
		ids = append(ids, h.ID())
	}
	for _, dir := range dirs {
		h, err := m.SubmitRecursiveUpload(run, transfer.RecursiveUpload{
			Source: dir, Dest: joinRemote(dest, filepath.Base(dir)),
			Policy:           policy,
			NoHash:           opts.NoHash,
			ContinueOnError:  opts.ContinueOnError,
			IncludeEmptyDirs: opts.IncludeEmptyDirs,
			// A folder upload carries no presentation or caption (above);
			// the replace confirmation still applies.
			Options: service.UploadOptions{ConfirmReplace: opts.ConfirmReplace},
		})
		if err != nil {
			return nil, toError(err)
		}
		ids = append(ids, h.ID())
	}
	return ids, nil
}

// UploadPlanFile is one local path in the upload plan preview.
type UploadPlanFile struct {
	Local string `json:"local"`
	// Size is the file's size; 0 for folders and paths that do not stat.
	Size int64 `json:"size"`
	// Dir marks a folder (a recursive upload).
	Dir bool `json:"dir,omitempty"`
	// OverLimit marks a file larger than the account's upload limit; the
	// upload itself would fail with ERR_FILE_TOO_LARGE.
	OverLimit bool `json:"over_limit,omitempty"`
}

// UploadPlan is the dry-run preview the upload sheet shows before the
// upload starts. Local, Remote, Policy, and WouldReplace are the service's
// dry-run plan for the same input, exactly as td cp --dry-run reports it;
// the rest lets the sheet show what the bare plan cannot.
type UploadPlan struct {
	Local        []string `json:"local"`
	Remote       string   `json:"remote"`
	Policy       string   `json:"policy"`
	WouldReplace string   `json:"would_replace,omitempty"`
	// UploadLimitBytes is the account's per-file upload limit (2 GB, or
	// 4 GB with Premium), from the same capability surface as td status.
	UploadLimitBytes int64 `json:"upload_limit_bytes"`
	// Files carries one entry per path, in the order given.
	Files []UploadPlanFile `json:"files"`
}

// PlanUpload previews uploading paths into the remote directory dest under
// policy, without starting anything: the service's dry-run plan plus each
// file's size against the account's upload limit. It needs a logged-in
// App for the limit, like the channel status does.
func (t *Transfers) PlanUpload(ctx context.Context, paths []string, dest, policy string) (*UploadPlan, error) {
	if len(paths) == 0 {
		return nil, toError(apperr.New(apperr.ErrUsage, "nothing to upload"))
	}
	p, err := conflictPolicy(policy)
	if err != nil {
		return nil, toError(err)
	}
	app := t.state.current()
	if app.TG == nil {
		return nil, toError(apperr.New(apperr.ErrAuthRequired, "not logged in"))
	}
	st, err := app.Status(ctx)
	if err != nil {
		return nil, toError(err)
	}
	plan := service.PlanUpload(paths, dest, p)
	out := &UploadPlan{
		Local: plan.Local, Remote: plan.Remote, Policy: string(plan.Policy),
		WouldReplace: plan.WouldReplace, Files: make([]UploadPlanFile, 0, len(paths)),
	}
	if st.UploadLimitBytes != nil {
		out.UploadLimitBytes = *st.UploadLimitBytes
	}
	for _, local := range paths {
		f := UploadPlanFile{Local: local}
		if info, err := os.Stat(local); err == nil {
			f.Dir = info.IsDir()
			if info.Mode().IsRegular() {
				f.Size = info.Size()
				f.OverLimit = out.UploadLimitBytes > 0 && info.Size() > out.UploadLimitBytes
			}
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

// Download downloads a remote file or directory into the local directory
// destDir, under the remote name, and returns the new Transfer's ID.
func (t *Transfers) Download(ctx context.Context, remotePath, destDir string, opts DownloadOptions) (string, error) {
	policy, err := conflictPolicy(opts.Policy)
	if err != nil {
		return "", toError(err)
	}
	entries, err := t.state.current().ListDir(ctx, remotePath)
	if err != nil {
		return "", toError(err)
	}
	// Listing a file path returns the file itself (Unix-ls style); anything
	// else is a directory.
	isFile := len(entries) == 1 && entries[0].Type == "file" &&
		entries[0].Path == strings.TrimRight(remotePath, "/")
	m := t.manager()
	run := t.runContext(ctx)
	local := filepath.Join(destDir, path.Base(strings.TrimRight(remotePath, "/")))
	if isFile {
		h, err := m.SubmitDownload(run, transfer.Download{
			Source: remotePath, Dest: local, Policy: policy,
		})
		if err != nil {
			return "", toError(err)
		}
		return h.ID(), nil
	}
	h, err := m.SubmitRecursiveDownload(run, transfer.RecursiveDownload{
		Source: remotePath, Dest: local, Policy: policy,
	})
	if err != nil {
		return "", toError(err)
	}
	return h.ID(), nil
}

// Cancel requests a Transfer's cancellation through the index. It works on
// any process's Transfer: the owner notices the flag on its lease
// heartbeat and ends the Transfer cancelled.
func (t *Transfers) Cancel(ctx context.Context, id string) (*Transfer, error) {
	tr, err := t.manager().Cancel(ctx, id)
	if err != nil {
		return nil, toError(err)
	}
	dto := transferDTO(*tr)
	t.note(dto)
	return &dto, nil
}

// Retry re-runs a failed, cancelled, or interrupted Transfer from its
// recorded request; this GUI becomes its owner and an interrupted upload
// resumes from its saved parts. The Manager pre-flights the stage from the
// index and rejects active and completed Transfers with ERR_USAGE, as
// td transfers retry does.
func (t *Transfers) Retry(ctx context.Context, id string) (*Transfer, error) {
	if _, err := t.manager().Retry(context.WithoutCancel(ctx), id, service.Observer{}); err != nil {
		return nil, toError(err)
	}
	tr, err := t.manager().Get(ctx, id)
	if err != nil {
		return nil, toError(err)
	}
	dto := transferDTO(*tr)
	t.note(dto)
	return &dto, nil
}

// ClearFinished removes every terminal Transfer from the index and reports
// each removal as a transfer-removed event. It returns how many cleared.
func (t *Transfers) ClearFinished(ctx context.Context) (int, error) {
	ids, err := t.manager().ClearFinished(ctx)
	if err != nil {
		return 0, toError(err)
	}
	for _, id := range ids {
		t.remove(id)
	}
	return len(ids), nil
}

// PickFiles opens the native multi-file dialog. Without a connected picker
// (a server-mode build without scripted answers) it is ERR_USAGE.
func (t *Transfers) PickFiles(ctx context.Context) ([]string, error) {
	if t.pick == nil {
		return nil, toError(apperr.New(apperr.ErrUsage, "no file dialog is connected"))
	}
	files, err := t.pick.PickFiles(ctx)
	if err != nil {
		return nil, toError(err)
	}
	return files, nil
}

// PickDirectory opens the native directory dialog, for the folder to
// upload or the folder a download lands in. Without a connected picker it
// is ERR_USAGE.
func (t *Transfers) PickDirectory(ctx context.Context) (string, error) {
	if t.pick == nil {
		return "", toError(apperr.New(apperr.ErrUsage, "no file dialog is connected"))
	}
	dir, err := t.pick.PickDirectory(ctx)
	if err != nil {
		return "", toError(err)
	}
	return dir, nil
}

func (t *Transfers) manager() *transfer.Manager { return t.state.currentManager() }

// runContext is the context a submitted Transfer runs under: it outlives
// the bound call, and pins the active channel so a later switch does not
// move the Transfer.
func (t *Transfers) runContext(ctx context.Context) context.Context {
	return service.WithChannel(context.WithoutCancel(ctx), t.state.selected())
}

// note records the newest known state of a Transfer and emits the typed
// event for what changed: a stage event for a new Transfer or a stage
// change, a progress event for byte, item, or cancel-flag movement within
// a stage.
func (t *Transfers) note(dto Transfer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, known := t.seen[dto.ID]
	t.seen[dto.ID] = dto
	if t.emit == nil {
		return
	}
	switch {
	case !known || prev.Stage != dto.Stage:
		t.emit(EventTransferStage, dto)
	case prev.BytesDone != dto.BytesDone || prev.BytesTotal != dto.BytesTotal ||
		prev.ItemsDone != dto.ItemsDone || prev.ItemsTotal != dto.ItemsTotal ||
		prev.ItemsFailed != dto.ItemsFailed || prev.CancelRequested != dto.CancelRequested:
		t.emit(EventTransferProgress, dto)
	}
}

// remove drops a Transfer from the snapshot and reports its removal once.
func (t *Transfers) remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, known := t.seen[id]; !known {
		return
	}
	delete(t.seen, id)
	if t.emit != nil {
		t.emit(EventTransferRemoved, TransferRemoved{ID: id})
	}
}

// observeStage and observeProgress are the Manager Observer of this
// process's Transfers; the tracker's own throttle bounds the progress
// rate.
func (t *Transfers) observeStage(tr transfer.Transfer)    { t.note(transferDTO(tr)) }
func (t *Transfers) observeProgress(tr transfer.Transfer) { t.note(transferDTO(tr)) }

// syncFromIndex reports changes other processes made to the index: new and
// changed Transfers as stage and progress events, vanished ones as
// removals. Services.StartSync runs it on every index commit.
func (t *Transfers) syncFromIndex(ctx context.Context) {
	list, err := t.manager().List(ctx, transfer.Filter{All: true})
	if err != nil {
		return
	}
	present := make(map[string]bool, len(list))
	for _, tr := range list {
		dto := transferDTO(tr)
		present[dto.ID] = true
		t.note(dto)
	}
	t.mu.Lock()
	var gone []string
	for id := range t.seen {
		if !present[id] {
			gone = append(gone, id)
		}
	}
	t.mu.Unlock()
	for _, id := range gone {
		t.remove(id)
	}
}

// joinRemote joins a remote directory and a file or directory name.
func joinRemote(dir, name string) string {
	if dir == "" || dir == "/" {
		return "/" + name
	}
	return strings.TrimRight(dir, "/") + "/" + name
}

// remoteDir is dir with directory intent: exactly one trailing slash,
// except the root.
func remoteDir(dir string) string {
	if dir == "" || dir == "/" {
		return "/"
	}
	return strings.TrimRight(dir, "/") + "/"
}
