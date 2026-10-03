//go:build gui

package gui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// Maintenance is the facade service for adopting channel messages,
// repairing the drive, and the doctor diagnostics.
type Maintenance struct {
	state *appState
	emit  Emitter
}

func (m *Maintenance) emitEvent(name string, data any) {
	if m.emit != nil {
		m.emit(name, data)
	}
}

// --- adopt ----------------------------------------------------------------

// AdoptOptions carries one adopt's settings. Confirm is the typed
// confirmation the real adopt requires (ADR 0003); a preview needs none.
type AdoptOptions struct {
	// Into is the directory adopted messages land in (the drive root when
	// empty).
	Into string `json:"into,omitempty"`
	// NoHash adopts without downloading the content hash.
	NoHash bool `json:"no_hash,omitempty"`
	// RewriteCaptions restores captions from the machine records instead of
	// adopting new messages.
	RewriteCaptions bool `json:"rewrite_captions,omitempty"`
	Confirm         bool `json:"confirm,omitempty"`
}

// AdoptItem is one channel message in a preview or a result.
type AdoptItem struct {
	MessageID int    `json:"message_id"`
	Kind      string `json:"kind"`
	FileName  string `json:"file_name,omitempty"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size,omitempty"`
	// Action is "adopt", "restore", "skip", or "fail".
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// AdoptOutcome is the dry-run plan or the finished adopt's summary.
type AdoptOutcome struct {
	DryRun  bool        `json:"dry_run"`
	Adopted int         `json:"adopted"`
	Skipped int         `json:"skipped"`
	Failed  int         `json:"failed"`
	Deleted int         `json:"deleted"`
	Items   []AdoptItem `json:"items"`
}

// PreviewAdopt plans adopting the channel's unmanaged messages without
// touching Telegram or the index.
func (m *Maintenance) PreviewAdopt(ctx context.Context, opts AdoptOptions) (*AdoptOutcome, error) {
	return m.adopt(ctx, opts, true)
}

// Adopt claims the channel's unmanaged messages into the index. Confirm is
// the frontend's confirmation sheet; without it the service gate refuses
// with ERR_CONFIRMATION_REQUIRED.
func (m *Maintenance) Adopt(ctx context.Context, opts AdoptOptions) (*AdoptOutcome, error) {
	return m.adopt(ctx, opts, false)
}

func (m *Maintenance) adopt(ctx context.Context, opts AdoptOptions, dryRun bool) (*AdoptOutcome, error) {
	res, err := m.state.current().Adopt(ctx, service.AdoptOptions{
		// The GUI adopts the whole unmanaged backlog; single-message adopt
		// stays a CLI form.
		Unmanaged:       !opts.RewriteCaptions,
		Into:            opts.Into,
		NoHash:          opts.NoHash,
		RewriteCaptions: opts.RewriteCaptions,
		DryRun:          dryRun,
		Confirm:         opts.Confirm,
	})
	if err != nil {
		return nil, toError(err)
	}
	out := &AdoptOutcome{
		DryRun:  res.DryRun,
		Adopted: res.Adopted,
		Skipped: res.Skipped,
		Failed:  res.Failed,
		Deleted: res.Deleted,
		Items:   make([]AdoptItem, 0, len(res.Items)),
	}
	for _, it := range res.Items {
		out.Items = append(out.Items, AdoptItem{
			MessageID: it.MessageID,
			Kind:      it.Kind,
			FileName:  it.FileName,
			Path:      it.Path,
			Size:      it.Size,
			Action:    it.Action,
			Reason:    it.Reason,
		})
	}
	return out, nil
}

// --- repair ---------------------------------------------------------------

// The repair modes RepairOptions.Mode takes, one per td repair form.
const (
	RepairModePending    = "pending"
	RepairModeOrphaned   = "orphaned"
	RepairModeScanErrors = "scan_errors"
	RepairModeHash       = "hash"
	RepairModeCaptions   = "captions"
	RepairModePath       = "path"
)

// RepairOptions selects one repair mode and its settings. Path is required
// for "path" and scopes "captions" and "hash"; it is ignored by the other
// modes. DeleteOrphaned (orphaned mode only) requires Confirm (ADR 0003).
// DryRun and ContinueOnError apply to captions only.
type RepairOptions struct {
	Mode            string `json:"mode"`
	Path            string `json:"path,omitempty"`
	DeleteOrphaned  bool   `json:"delete_orphaned,omitempty"`
	Confirm         bool   `json:"confirm,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
	ContinueOnError bool   `json:"continue_on_error,omitempty"`
}

// RepairOutcome is the selected mode's result; exactly one field beyond
// Mode is set.
type RepairOutcome struct {
	Mode       string                   `json:"mode"`
	Captions   *CaptionsRepairOutcome   `json:"captions,omitempty"`
	Hash       *HashRepairOutcome       `json:"hash,omitempty"`
	Path       *PathRepairOutcome       `json:"path,omitempty"`
	Pending    *PendingRepairOutcome    `json:"pending,omitempty"`
	Orphaned   *OrphanedRepairOutcome   `json:"orphaned,omitempty"`
	ScanErrors *ScanErrorsRepairOutcome `json:"scan_errors,omitempty"`
}

// RepairItem is one file a captions or hash repair touched.
type RepairItem struct {
	Path      string `json:"path"`
	MessageID int    `json:"message_id,omitempty"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
}

type CaptionsRepairOutcome struct {
	DryRun  bool         `json:"dry_run"`
	Total   int          `json:"total"`
	Planned int          `json:"planned"`
	Cleaned int          `json:"cleaned"`
	Skipped int          `json:"skipped"`
	Failed  int          `json:"failed"`
	Items   []RepairItem `json:"items"`
}

type HashRepairOutcome struct {
	Total      int          `json:"total"`
	Backfilled int          `json:"backfilled"`
	Failed     int          `json:"failed"`
	Items      []RepairItem `json:"items"`
}

type PathRepairOutcome struct {
	Repaired string `json:"repaired"`
}

type PendingRepairOutcome struct {
	Repaired     int   `json:"repaired"`
	Invalid      int   `json:"invalid"`
	Orphaned     int   `json:"orphaned"`
	Skipped      int   `json:"skipped"`
	LocksCleared int64 `json:"locks_cleared"`
}

type OrphanedRepairOutcome struct {
	Repaired int `json:"repaired"`
	Deleted  int `json:"deleted"`
	Invalid  int `json:"invalid"`
}

type ScanErrorsRepairOutcome struct {
	Resolved int `json:"resolved"`
	Pending  int `json:"pending"`
}

// Repair runs the selected mode. While it runs it emits ItemEvent on
// EventRepairItem once per item the mode accounts for.
func (m *Maintenance) Repair(ctx context.Context, opts RepairOptions) (*RepairOutcome, error) {
	svcOpts := service.RepairOptions{
		DeleteOrphaned:  opts.DeleteOrphaned,
		Confirm:         opts.Confirm,
		DryRun:          opts.DryRun,
		ContinueOnError: opts.ContinueOnError,
		Observer:        (&itemTracker{emit: m.emitEvent, event: EventRepairItem}).observer(),
	}
	switch opts.Mode {
	case RepairModePending:
		svcOpts.Pending = true
	case RepairModeOrphaned:
		svcOpts.Orphaned = true
	case RepairModeScanErrors:
		svcOpts.ScanErrors = true
	case RepairModeHash:
		svcOpts.Hash = true
	case RepairModeCaptions:
		svcOpts.Captions = true
	case RepairModePath:
		if strings.TrimSpace(opts.Path) == "" {
			return nil, toError(apperr.New(apperr.ErrUsage, "repairing one file requires its path"))
		}
	default:
		return nil, toError(apperr.New(apperr.ErrUsage, fmt.Sprintf("unknown repair mode %q", opts.Mode)))
	}
	// Path scopes captions and hash and names the one file in path mode; a
	// stray path would silently take precedence over the other modes in the
	// service, so it is only forwarded where it is meaningful.
	if opts.Mode == RepairModePath || opts.Mode == RepairModeCaptions || opts.Mode == RepairModeHash {
		if p := strings.TrimSpace(opts.Path); p != "" {
			svcOpts.Path = &p
		}
	}
	res, err := m.state.current().Repair(ctx, svcOpts)
	if err != nil {
		return nil, toError(err)
	}
	out := &RepairOutcome{Mode: opts.Mode}
	switch r := res.(type) {
	case *service.RepairCaptionsResult:
		items := make([]RepairItem, 0, len(r.Items))
		for _, it := range r.Items {
			items = append(items, RepairItem{Path: it.Path, MessageID: it.MessageID, Action: it.Action, Reason: it.Reason})
		}
		out.Captions = &CaptionsRepairOutcome{
			DryRun: r.DryRun, Total: r.Total, Planned: r.Planned,
			Cleaned: r.Cleaned, Skipped: r.Skipped, Failed: r.Failed, Items: items,
		}
	case *service.RepairHashResult:
		items := make([]RepairItem, 0, len(r.Items))
		for _, it := range r.Items {
			items = append(items, RepairItem{Path: it.Path, Action: it.Action, Reason: it.Reason})
		}
		out.Hash = &HashRepairOutcome{Total: r.Total, Backfilled: r.Backfilled, Failed: r.Failed, Items: items}
	case *service.RepairPathResult:
		out.Path = &PathRepairOutcome{Repaired: r.Repaired}
	case *service.RepairPendingResult:
		out.Pending = &PendingRepairOutcome{
			Repaired: r.Repaired, Invalid: r.Invalid, Orphaned: r.Orphaned,
			Skipped: r.Skipped, LocksCleared: r.LocksCleared,
		}
	case *service.RepairOrphanedResult:
		out.Orphaned = &OrphanedRepairOutcome{Repaired: r.Repaired, Deleted: r.Deleted, Invalid: r.Invalid}
	case *service.RepairScanErrorsResult:
		out.ScanErrors = &ScanErrorsRepairOutcome{Resolved: r.Resolved, Pending: r.Pending}
	default:
		return nil, toError(apperr.New("ERR_UNKNOWN", fmt.Sprintf("unexpected repair result %T", res)))
	}
	return out, nil
}

// --- doctor ---------------------------------------------------------------

// DoctorCheck is one capability check's outcome.
type DoctorCheck struct {
	Name string `json:"name"`
	// Status is "pass", "warn", "fail", or "unknown".
	Status string `json:"status"`
	// Hint is how to fix a warn or fail, when the check has one.
	Hint string `json:"hint,omitempty"`
}

// DoctorReport is the doctor run rendered for the frontend: the checks in
// name order plus the upload limit the Telegram probe reported.
type DoctorReport struct {
	Checks         []DoctorCheck `json:"checks"`
	MaxUploadBytes *int64        `json:"max_upload_bytes,omitempty"`
}

// Doctor runs the service's capability checks: auth, storage, and the
// Telegram probes (edit, delete, upload, invite, Saved Messages) — the
// facade never reimplements a check.
func (m *Maintenance) Doctor(ctx context.Context) (*DoctorReport, error) {
	res, err := m.state.current().Doctor(ctx)
	if err != nil {
		return nil, toError(err)
	}
	names := make([]string, 0, len(res.Checks))
	for name := range res.Checks {
		names = append(names, name)
	}
	sort.Strings(names)
	out := &DoctorReport{Checks: make([]DoctorCheck, 0, len(names)), MaxUploadBytes: res.MaxUploadBytes}
	for _, name := range names {
		out.Checks = append(out.Checks, DoctorCheck{Name: name, Status: res.Checks[name], Hint: res.Hints[name]})
	}
	return out, nil
}

// PathCodecReport is the path-codec doctor's result.
type PathCodecReport struct {
	FixedVectors string `json:"fixed_vectors"`
	DBCheck      string `json:"db_check"`
	DBRows       int    `json:"db_rows"`
	CorruptRows  int    `json:"corrupt_rows"`
}

// PathCodecDoctor runs the path codec self-test and verifies the stored
// slug mappings against the database.
func (m *Maintenance) PathCodecDoctor(ctx context.Context) (*PathCodecReport, error) {
	res, err := m.state.current().PathCodecDoctor(ctx)
	if err != nil {
		return nil, toError(err)
	}
	return &PathCodecReport{
		FixedVectors: res.FixedVectors,
		DBCheck:      res.DBCheck,
		DBRows:       res.DBRows,
		CorruptRows:  res.CorruptRows,
	}, nil
}
