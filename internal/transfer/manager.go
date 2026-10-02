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
	// OnProgress reports a Transfer's byte progress, at most as often as it
	// is written to the index.
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
}

// SubmitUpload records the upload as a queued Transfer and starts it. ctx
// bounds the Transfer's whole life: cancelling it cancels the Transfer,
// queued or running, which then ends failed with ERR_CANCELLED. Every
// failure of the upload itself ends the Transfer failed with the error the
// upload use case reports.
func (m *Manager) SubmitUpload(ctx context.Context, req Upload) (*Handle[*service.UploadResult], error) {
	// A channel that does not resolve is recorded empty and left to the use
	// case to report, so its errors keep their precedence (a missing source
	// over an unbound drive) and the command's output stays the same.
	channel, _ := m.app.ChannelTelegramID(ctx)
	source, err := filepath.Abs(req.Source)
	if err != nil {
		source = req.Source
	}
	var size int64
	if st, err := os.Stat(source); err == nil && st.Mode().IsRegular() {
		size = st.Size()
	}
	pres := req.Presentation
	options, _ := json.Marshal(uploadOptions{
		Policy: req.Policy, NoHash: req.NoHash,
		Kind: pres.Kind, DurationSeconds: pres.DurationSeconds, Width: pres.Width, Height: pres.Height,
		SupportsStreaming: pres.SupportsStreaming, ThumbPath: pres.ThumbPath,
		Threads: req.Options.Threads, PartSizeKB: req.Options.PartSizeKB, ConfirmReplace: req.Options.ConfirmReplace,
	})
	t := Transfer{
		Kind: KindUpload, Channel: channel, Source: source, Dest: req.Dest,
		BytesTotal: size, ItemsTotal: 1,
	}
	ctx = service.WithChannel(ctx, channel)
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
	if err := m.app.DB.InsertTransfer(ctx, row); err != nil {
		return nil, err
	}
	tr := &tracker{m: m, t: t, written: now, ctx: context.WithoutCancel(ctx)}
	tr.notify(m.opts.Observer.OnStage)
	h := &Handle[T]{id: t.ID, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		select {
		case m.slots <- struct{}{}:
		case <-ctx.Done():
			h.err = apperr.Cancelled()
			tr.finish(ctx, h.err)
			return
		}
		defer func() { <-m.slots }()
		h.result, h.err = run(ctx, tr)
		tr.finish(ctx, h.err)
	}()
	return h, nil
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
	ctx     context.Context
	mu      sync.Mutex
	t       Transfer
	written time.Time
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
		OnItem: next.OnItem,
	}
}

var stageOf = map[service.Stage]Stage{
	service.StageHashing:    StageHashing,
	service.StageUploading:  StageUploading,
	service.StagePublishing: StagePublishing,
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

func (tr *tracker) progress(done, total int64) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.t.BytesDone = done
	if total > 0 {
		tr.t.BytesTotal = total
	}
	now := time.Now().UTC()
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

// finish ends the Transfer completed when err is nil and failed with err's
// code and message otherwise. ctx is the Transfer's context: an error after
// its cancellation is recorded as the CLI reports it.
func (tr *tracker) finish(ctx context.Context, err error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	now := time.Now().UTC()
	if err == nil {
		tr.t.Stage = StageCompleted
		tr.t.ItemsDone = tr.t.ItemsTotal
	} else {
		if ctx.Err() != nil {
			err = apperr.AfterCancel(err)
		}
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
