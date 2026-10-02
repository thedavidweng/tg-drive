package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// defaultConcurrency applies when transfers.concurrency is unset.
const defaultConcurrency = 2

// progressInterval is the least time between two byte-progress writes of one
// Transfer. Stage changes are written at once.
const progressInterval = 250 * time.Millisecond

// Observer watches the Transfers of one Manager. Callbacks run on the
// goroutine that changed the Transfer, one at a time per Transfer and in the
// order its changes happen; they must return promptly. Every callback is
// optional.
type Observer struct {
	// OnStage reports a Transfer entering a stage, queued included.
	OnStage func(Transfer)
	// OnProgress reports a Transfer's byte or item progress, at most as
	// often as it is written to the index.
	OnProgress func(Transfer)
}

// Options configures a Manager.
type Options struct {
	// FrontEnd is recorded as the creator of every Transfer submitted.
	FrontEnd FrontEnd
	// Observer receives every change to the Transfers this Manager runs.
	Observer Observer
}

// Manager submits Transfers and runs them on one App, at most
// transfers.concurrency at a time; the rest wait queued. A Manager owns the
// Transfers it submits. Reading Transfers (List, Get) needs no Telegram
// client, so an offline App serves them.
type Manager struct {
	app   *service.App
	opts  Options
	owner string
	slots chan struct{}
}

// New returns a Manager running Transfers on app.
func New(app *service.App, opts Options) *Manager {
	n := app.Cfg.Transfers.Concurrency
	if n < 1 {
		n = defaultConcurrency
	}
	return &Manager{app: app, opts: opts, owner: newOwnerToken(), slots: make(chan struct{}, n)}
}

// Upload asks for one local file to be uploaded, as service.App.UploadFileAs
// does.
type Upload struct {
	Source       string
	Dest         string
	Policy       service.ConflictPolicy
	NoHash       bool
	Presentation service.Presentation
	// Options are the upload call's own settings. Its Observer still sees
	// every report of the call.
	Options service.UploadOptions
}

// uploadOptions is how an upload's settings are stored for retrying it.
type uploadOptions struct {
	Policy            service.ConflictPolicy `json:"policy"`
	NoHash            bool                   `json:"no_hash"`
	Kind              string                 `json:"kind,omitempty"`
	DurationSeconds   float64                `json:"duration_seconds,omitempty"`
	Width             int                    `json:"width,omitempty"`
	Height            int                    `json:"height,omitempty"`
	SupportsStreaming bool                   `json:"supports_streaming,omitempty"`
	ThumbPath         string                 `json:"thumb_path,omitempty"`
	Threads           int                    `json:"threads,omitempty"`
	PartSizeKB        int                    `json:"part_size_kb,omitempty"`
	ConfirmReplace    bool                   `json:"confirm_replace,omitempty"`
	// Sources are an album upload's local files; a single-file upload keeps
	// its one source in the Transfer's Source.
	Sources []string `json:"sources,omitempty"`
	// ContinueOnError and IncludeEmptyDirs belong to recursive uploads.
	ContinueOnError  bool `json:"continue_on_error,omitempty"`
	IncludeEmptyDirs bool `json:"include_empty_dirs,omitempty"`
}

// marshalUploadOptions is the uploadOptions of one upload request.
func marshalUploadOptions(policy service.ConflictPolicy, noHash bool, pres service.Presentation, opts service.UploadOptions) uploadOptions {
	return uploadOptions{
		Policy: policy, NoHash: noHash,
		Kind: pres.Kind, DurationSeconds: pres.DurationSeconds, Width: pres.Width, Height: pres.Height,
		SupportsStreaming: pres.SupportsStreaming, ThumbPath: pres.ThumbPath,
		Threads: opts.Threads, PartSizeKB: opts.PartSizeKB, ConfirmReplace: opts.ConfirmReplace,
	}
}

// pinChannel resolves the bound drive channel's Telegram ID for the
// Transfer record and pins it as the call's channel, so record and call
// agree. A channel that does not resolve is recorded empty and left to the
// use case to report, so its errors keep their precedence (a missing source
// over an unbound drive) and the command's output stays the same.
func (m *Manager) pinChannel(ctx context.Context) (context.Context, string) {
	channel, _ := m.app.ChannelTelegramID(ctx)
	return service.WithChannel(ctx, channel), channel
}

// absPath makes p absolute, keeping p when it cannot be resolved.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// SubmitUpload records the upload as a queued Transfer and starts it. ctx
// bounds the Transfer's whole life: cancelling it cancels the Transfer,
// queued or running, which then ends failed with ERR_CANCELLED. Every
// failure of the upload itself ends the Transfer failed with the error the
// upload use case reports.
func (m *Manager) SubmitUpload(ctx context.Context, req Upload) (*Handle[*service.UploadResult], error) {
	ctx, channel := m.pinChannel(ctx)
	source := absPath(req.Source)
	var size int64
	if st, err := os.Stat(source); err == nil && st.Mode().IsRegular() {
		size = st.Size()
	}
	options, _ := json.Marshal(marshalUploadOptions(req.Policy, req.NoHash, req.Presentation, req.Options))
	t := Transfer{
		Kind: KindUpload, Channel: channel, Source: source, Dest: req.Dest,
		BytesTotal: size, ItemsTotal: 1,
	}
	return submit(ctx, m, t, string(options), func(ctx context.Context, tr *tracker) (*service.UploadResult, error) {
		opts := req.Options
		opts.Observer = tr.observe(opts.Observer)
		res, err := m.app.UploadFileAs(ctx, req.Source, req.Dest, req.Policy, req.NoHash, req.Presentation, opts)
		if err == nil {
			tr.landed(res.Path, res.Size, !res.Skipped)
		}
		return res, err
	})
}

// AlbumUpload asks for several local files to be uploaded as Telegram
// albums sharing one remote directory, as service.App.UploadFilesAs does.
// The whole call is one Transfer whose item counts track the member files.
type AlbumUpload struct {
	Sources []string
	// Dest is the remote directory as the command was given it.
	Dest         string
	Policy       service.ConflictPolicy
	NoHash       bool
	Presentation service.Presentation
	// Options are the upload call's own settings. Its Observer still sees
	// every report of the call.
	Options service.UploadOptions
}

// SubmitAlbumUpload records the album upload as a queued Transfer and
// starts it, under the same ctx contract as SubmitUpload. The Transfer's
// Source is empty: the sources are many, and the stored options list them.
func (m *Manager) SubmitAlbumUpload(ctx context.Context, req AlbumUpload) (*Handle[*service.AlbumUploadResult], error) {
	ctx, channel := m.pinChannel(ctx)
	stored := marshalUploadOptions(req.Policy, req.NoHash, req.Presentation, req.Options)
	stored.Sources = req.Sources
	options, _ := json.Marshal(stored)
	t := Transfer{
		Kind: KindAlbumUpload, Channel: channel, Dest: req.Dest,
		ItemsTotal: len(req.Sources),
	}
	return submit(ctx, m, t, string(options), func(ctx context.Context, tr *tracker) (*service.AlbumUploadResult, error) {
		opts := req.Options
		opts.Observer = tr.observe(opts.Observer)
		return m.app.UploadFilesAs(ctx, req.Sources, req.Dest, req.Policy, req.NoHash, req.Presentation, opts)
	})
}

// RecursiveUpload asks for one local directory tree to be uploaded, as
// service.App.UploadRecursive does. The whole tree is one Transfer whose
// item counts track the files.
type RecursiveUpload struct {
	// Source is the local directory.
	Source string
	// Dest is the remote directory as the command was given it.
	Dest             string
	Policy           service.ConflictPolicy
	ContinueOnError  bool
	NoHash           bool
	IncludeEmptyDirs bool
	// Options are the upload call's own settings. Its Observer still sees
	// every report of the call.
	Options service.UploadOptions
}

// SubmitRecursiveUpload records the recursive upload as a queued Transfer
// and starts it, under the same ctx contract as SubmitUpload. The walk
// discovers the files as it runs, so the Transfer's item total grows with
// each item reported.
func (m *Manager) SubmitRecursiveUpload(ctx context.Context, req RecursiveUpload) (*Handle[*service.RecursiveUploadResult], error) {
	ctx, channel := m.pinChannel(ctx)
	stored := marshalUploadOptions(req.Policy, req.NoHash, service.Presentation{}, req.Options)
	stored.ContinueOnError = req.ContinueOnError
	stored.IncludeEmptyDirs = req.IncludeEmptyDirs
	options, _ := json.Marshal(stored)
	t := Transfer{
		Kind: KindRecursiveUpload, Channel: channel, Source: absPath(req.Source), Dest: req.Dest,
	}
	return submit(ctx, m, t, string(options), func(ctx context.Context, tr *tracker) (*service.RecursiveUploadResult, error) {
		opts := req.Options
		opts.Observer = tr.observe(opts.Observer)
		return m.app.UploadRecursive(ctx, req.Source, req.Dest, req.Policy, req.ContinueOnError, req.NoHash, req.IncludeEmptyDirs, opts)
	})
}

// Download asks for one remote file to be downloaded, as
// service.App.DownloadFile does.
type Download struct {
	// Source is the remote path to download.
	Source string
	// Dest is the local destination as the command was given it.
	Dest   string
	Policy service.ConflictPolicy
	// Options are the download call's own settings. Its Observer still sees
	// every report of the call.
	Options service.DownloadOptions
}

// downloadOptions is how a download's settings are stored for retrying it.
type downloadOptions struct {
	Policy          service.ConflictPolicy `json:"policy"`
	ContinueOnError bool                   `json:"continue_on_error,omitempty"`
}

// SubmitDownload records the download as a queued Transfer and starts it,
// under the same ctx contract as SubmitUpload. The Transfer reports the
// downloading stage with byte progress; its Source is the remote path and
// Dest the local file, replaced by the path actually written once it
// completes.
func (m *Manager) SubmitDownload(ctx context.Context, req Download) (*Handle[*service.DownloadResult], error) {
	ctx, channel := m.pinChannel(ctx)
	options, _ := json.Marshal(downloadOptions{Policy: req.Policy})
	t := Transfer{
		Kind: KindDownload, Channel: channel, Source: req.Source, Dest: absPath(req.Dest),
		ItemsTotal: 1,
	}
	return submit(ctx, m, t, string(options), func(ctx context.Context, tr *tracker) (*service.DownloadResult, error) {
		opts := req.Options
		opts.Observer = tr.observe(opts.Observer)
		res, err := m.app.DownloadFile(ctx, req.Source, req.Dest, req.Policy, opts)
		if err == nil {
			tr.landed(absPath(res.Dest), res.Size, !res.Skipped)
		}
		return res, err
	})
}

// RecursiveDownload asks for one remote directory tree to be downloaded, as
// service.App.DownloadRecursive does. The whole tree is one Transfer whose
// item counts track the files.
type RecursiveDownload struct {
	// Source is the remote directory.
	Source string
	// Dest is the local directory as the command was given it.
	Dest            string
	Policy          service.ConflictPolicy
	ContinueOnError bool
	// Options are the download call's own settings. Its Observer still sees
	// every report of the call.
	Options service.DownloadOptions
}

// SubmitRecursiveDownload records the recursive download as a queued
// Transfer and starts it, under the same ctx contract as SubmitUpload. The
// listing discovers the files as it runs, so the Transfer's item total
// grows with each item reported.
func (m *Manager) SubmitRecursiveDownload(ctx context.Context, req RecursiveDownload) (*Handle[*service.RecursiveDownloadResult], error) {
	ctx, channel := m.pinChannel(ctx)
	options, _ := json.Marshal(downloadOptions{Policy: req.Policy, ContinueOnError: req.ContinueOnError})
	t := Transfer{
		Kind: KindRecursiveDownload, Channel: channel, Source: req.Source, Dest: absPath(req.Dest),
	}
	return submit(ctx, m, t, string(options), func(ctx context.Context, tr *tracker) (*service.RecursiveDownloadResult, error) {
		opts := req.Options
		opts.Observer = tr.observe(opts.Observer)
		return m.app.DownloadRecursive(ctx, req.Source, req.Dest, req.Policy, req.ContinueOnError, opts)
	})
}

// Handle is a Transfer submitted to this process's Manager.
type Handle[T any] struct {
	id     string
	done   chan struct{}
	result T
	err    error
}

// ID is the Transfer ID.
func (h *Handle[T]) ID() string { return h.id }

// Wait blocks until the Transfer ends, and returns the result and error of
// the call it ran. A cancelled Transfer ends promptly, after the call's
// cleanup.
func (h *Handle[T]) Wait() (T, error) {
	<-h.done
	return h.result, h.err
}

func submit[T any](ctx context.Context, m *Manager, t Transfer, options string,
	run func(ctx context.Context, tr *tracker) (T, error),
) (*Handle[T], error) {
	now := time.Now().UTC()
	t.ID = uuid.NewString()
	t.Stage = StageQueued
	t.FrontEnd = m.opts.FrontEnd
	t.CreatedAt, t.UpdatedAt = now, now
	row := t.row()
	row.Options = options
	row.OwnerToken = m.owner
	// The owner leases the Transfer from submission on, renewed by the
	// heartbeat below: a queued Transfer whose process dies is interrupted
	// by its readers just like a running one.
	row.LeaseExpiresAt = formatTime(now.Add(m.app.LockTTL()))
	if err := m.app.DB.InsertTransfer(ctx, row); err != nil {
		return nil, err
	}
	tctx, cancel := context.WithCancel(ctx)
	// A Transfer submitted without an item total (the recursive kinds, whose
	// item count only the walk discovers) grows it as items report; one
	// submitted with a total keeps it.
	tr := &tracker{m: m, t: t, written: now, ctx: context.WithoutCancel(ctx), growItems: t.ItemsTotal == 0}
	stop := m.leaseHeartbeat(t.ID, tr.ctx, cancel)
	tr.notify(m.opts.Observer.OnStage)
	h := &Handle[T]{id: t.ID, done: make(chan struct{})}
	go func() {
		// finish records the ending before the heartbeat stops and Wait
		// unblocks, so a finished Transfer's lease never advances again.
		defer close(h.done)
		defer stop()
		defer cancel()
		select {
		case m.slots <- struct{}{}:
		case <-tctx.Done():
			h.err = apperr.Cancelled()
			tr.finish(h.err)
			return
		}
		defer func() { <-m.slots }()
		h.result, h.err = run(tctx, tr)
		// A Transfer ends cancelled, not failed, when its context stopped
		// it; AfterCancel keeps the code of a failure that left durable
		// state, which still ends the Transfer failed.
		if h.err != nil && tctx.Err() != nil {
			h.err = apperr.AfterCancel(h.err)
		}
		tr.finish(h.err)
	}()
	return h, nil
}

// leaseHeartbeat renews the Transfer's lease and polls its cancel-requested
// flag on the Operation-lock heartbeat: same TTL, renewed at one third of
// it. A set flag — written by any process sharing the index — cancels the
// Transfer's context, as does a renewal that finds the lease lost, so a
// Transfer never runs unowned. It writes with writeCtx, which outlives the
// Transfer's cancellation. The returned stop waits for the heartbeat to
// exit.
func (m *Manager) leaseHeartbeat(id string, writeCtx context.Context, cancel context.CancelFunc) (stop func()) {
	ttl := m.app.LockTTL()
	interval := ttl / 3
	if interval <= 0 {
		interval = time.Second
	}
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				ok, err := m.app.DB.RenewTransferLease(writeCtx, id, m.owner, formatTime(time.Now().UTC().Add(ttl)))
				if err != nil || !ok {
					cancel()
					return
				}
				if requested, err := m.app.DB.TransferCancelRequested(writeCtx, id); err == nil && requested {
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(stopCh); <-done }
}

// Cancel requests the cancellation of a non-terminal Transfer, from any
// process sharing the index: the owner notices the flag on its lease
// heartbeat and ends the Transfer cancelled. Cancelling while the request
// is already recorded is a no-op; an ended Transfer is ERR_USAGE and an
// unknown ID is ERR_TRANSFER_NOT_FOUND.
func (m *Manager) Cancel(ctx context.Context, id string) (*Transfer, error) {
	set, err := m.app.DB.SetTransferCancelRequested(ctx, id)
	if err != nil {
		return nil, err
	}
	t, err := m.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !set {
		return nil, apperr.New(apperr.ErrUsage, "transfer already ended ("+string(t.Stage)+"): "+id)
	}
	return t, nil
}

// Filter selects Transfers to list. The zero Filter selects the active
// (not yet ended) Transfers.
type Filter struct {
	// All selects every Transfer, ended ones included.
	All bool
	// Stage, when set, selects only the Transfers in that stage.
	Stage Stage
}

// List returns the Transfers f selects, newest first.
func (m *Manager) List(ctx context.Context, f Filter) ([]Transfer, error) {
	var stages []string
	switch {
	case f.Stage != "":
		stages = []string{string(f.Stage)}
	case !f.All:
		for _, s := range lifecycle {
			if !s.Terminal() {
				stages = append(stages, string(s))
			}
		}
	}
	rows, err := m.app.DB.ListTransfers(ctx, stages)
	if err != nil {
		return nil, err
	}
	out := make([]Transfer, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// Get returns the Transfer with id, failing with ERR_TRANSFER_NOT_FOUND when
// there is none.
func (m *Manager) Get(ctx context.Context, id string) (*Transfer, error) {
	r, err := m.app.DB.GetTransfer(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, apperr.New(apperr.ErrTransferNotFound, "no transfer with id "+id)
	}
	t := fromRow(*r)
	return &t, nil
}

// tracker records one running Transfer from the reports of the call it runs.
type tracker struct {
	m *Manager
	// ctx writes to the index; it outlives the Transfer's cancellation so
	// a cancelled Transfer still records how it ended.
	ctx context.Context
	mu  sync.Mutex
	t   Transfer
	// growItems counts each reported item into ItemsTotal.
	growItems bool
	written   time.Time
}

// observe is next with this Transfer's recording in front, so the index
// already shows a change when next reports it.
func (tr *tracker) observe(next service.Observer) service.Observer {
	return service.Observer{
		OnStage: func(it service.Item, st service.Stage) {
			if s, ok := stageOf[st]; ok {
				tr.enter(s)
			}
			if next.OnStage != nil {
				next.OnStage(it, st)
			}
		},
		OnProgress: func(p service.Progress) {
			tr.progress(p.Done, p.Total)
			if next.OnProgress != nil {
				next.OnProgress(p)
			}
		},
		OnItem: func(r service.ItemResult) {
			tr.item(r)
			if next.OnItem != nil {
				next.OnItem(r)
			}
		},
	}
}

var stageOf = map[service.Stage]Stage{
	service.StageHashing:     StageHashing,
	service.StageUploading:   StageUploading,
	service.StageDownloading: StageDownloading,
	service.StagePublishing:  StagePublishing,
}

func (tr *tracker) enter(s Stage) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if s.rank() <= tr.t.Stage.rank() {
		return
	}
	tr.t.Stage = s
	tr.write(time.Now().UTC())
	tr.notify(tr.m.opts.Observer.OnStage)
}

// tracksBytes reports whether a Transfer of this kind records byte
// progress: single-file Transfers do; multi-item ones count items instead.
func (k Kind) tracksBytes() bool {
	return k == KindUpload || k == KindDownload
}

func (tr *tracker) progress(done, total int64) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if !tr.t.Kind.tracksBytes() {
		return
	}
	tr.t.BytesDone = done
	if total > 0 {
		tr.t.BytesTotal = total
	}
	tr.throttledWrite(time.Now().UTC())
}

// item records one item's outcome. Done counts completed and skipped items,
// so done and failed together account for every item reported.
func (tr *tracker) item(r service.ItemResult) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.growItems {
		tr.t.ItemsTotal++
	}
	if r.Status == service.ItemFailed {
		tr.t.ItemsFailed++
	} else {
		tr.t.ItemsDone++
	}
	tr.throttledWrite(time.Now().UTC())
}

// throttledWrite saves the Transfer's progress at most once per
// progressInterval; stage changes and the ending write happen at once.
func (tr *tracker) throttledWrite(now time.Time) {
	if now.Sub(tr.written) < progressInterval {
		return
	}
	tr.write(now)
	tr.notify(tr.m.opts.Observer.OnProgress)
}

// landed records where a completed call wrote: dest, and size bytes when
// it sent any.
func (tr *tracker) landed(dest string, size int64, sent bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.t.Dest = dest
	if sent {
		tr.t.BytesDone, tr.t.BytesTotal = size, size
	}
}

// finish ends the Transfer: completed when err is nil, cancelled when err
// is the cancellation of the Transfer's context, and failed with err's
// code and message otherwise. A cancellation is recorded once the caller
// has normalized the error with apperr.AfterCancel, so a failure that left
// durable state (an orphaned upload, a repair) keeps its code and still
// ends the Transfer failed.
func (tr *tracker) finish(err error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	now := time.Now().UTC()
	switch {
	case err == nil:
		tr.t.Stage = StageCompleted
		// A call can succeed with items failed (its own lenient mode
		// reports them in the result), so done is what did not fail.
		tr.t.ItemsDone = tr.t.ItemsTotal - tr.t.ItemsFailed
	case apperr.IsCancelled(err):
		tr.t.Stage = StageCancelled
	default:
		tr.t.Stage = StageFailed
		if ae, ok := apperr.As(err); ok {
			tr.t.ErrorCode, tr.t.ErrorMessage = ae.Code, ae.Message
		} else {
			tr.t.ErrorCode, tr.t.ErrorMessage = "ERR_UNKNOWN", err.Error()
		}
	}
	tr.t.FinishedAt = &now
	tr.write(now)
	tr.notify(tr.m.opts.Observer.OnStage)
}

// write saves the Transfer's state. A failed write is not the call's
// failure: the call goes on, and the next write catches the index up.
func (tr *tracker) write(now time.Time) {
	tr.t.UpdatedAt = now
	tr.written = now
	_ = tr.m.app.DB.UpdateTransferState(tr.ctx, tr.t.row())
}

func (tr *tracker) notify(fn func(Transfer)) {
	if fn != nil {
		fn(tr.t)
	}
}

func newOwnerToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
