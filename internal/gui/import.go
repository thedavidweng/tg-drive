//go:build gui

package gui

import (
	"context"
	"fmt"
	"sync"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// Import is the facade service for Saved Messages import.
type Import struct {
	state *appState
	emit  Emitter

	mu      sync.Mutex
	prompts map[string]chan promptAnswer
	seq     int
}

// ImportOptions carries one import's settings. Confirm is the typed
// confirmation the real run requires (ADR 0003); DeleteSource requires it
// even on a preview. An empty PhotosAs asks through EventImportPrompt when
// the plan holds photos.
type ImportOptions struct {
	// Into is the destination directory (the service default when empty).
	Into string `json:"into,omitempty"`
	// PhotosAs is "document" or "photo"; empty asks via import.prompt.
	PhotosAs string `json:"photos_as,omitempty"`
	// Policy resolves destination path conflicts: "fail" (the default),
	// "replace", "skip", or "rename".
	Policy        string `json:"policy,omitempty"`
	MergeCaptions bool   `json:"merge_captions,omitempty"`
	DeleteSource  bool   `json:"delete_source,omitempty"`
	NoDedupe      bool   `json:"no_dedupe,omitempty"`
	Confirm       bool   `json:"confirm,omitempty"`
}

// ImportItem is one saved message in a preview or a result.
type ImportItem struct {
	MessageID int    `json:"message_id"`
	Kind      string `json:"kind"`
	// Action is "import", "skip", or "fail".
	Action string `json:"action"`
	Path   string `json:"path,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Reason string `json:"reason,omitempty"`
	// DuplicateOf names the drive file whose bytes matched a skipped item.
	DuplicateOf string `json:"duplicate_of,omitempty"`
	// SourceDeleted reports the saved original was deleted.
	SourceDeleted bool   `json:"source_deleted,omitempty"`
	Error         string `json:"error,omitempty"`
}

// ImportOutcome is the dry-run plan or the finished run's summary.
type ImportOutcome struct {
	DryRun          bool         `json:"dry_run"`
	Into            string       `json:"into"`
	PhotosAs        string       `json:"photos_as,omitempty"`
	HistoryComplete bool         `json:"history_complete"`
	Imported        int          `json:"imported"`
	Skipped         int          `json:"skipped"`
	Failed          int          `json:"failed"`
	Duplicates      int          `json:"duplicates"`
	CaptionsMerged  int          `json:"captions_merged"`
	SourcesDeleted  int          `json:"sources_deleted"`
	Photos          int          `json:"photos"`
	Items           []ImportItem `json:"items"`
}

// Preview plans the import without touching Telegram beyond reading the
// saved chat. The photo choice, when the plan holds photos and PhotosAs is
// empty, arrives as an import.prompt event here too, because the plan's
// presentation depends on the answer.
func (im *Import) Preview(ctx context.Context, opts ImportOptions) (*ImportOutcome, error) {
	return im.run(ctx, opts, true)
}

// Run executes the import. Confirm is the frontend's confirmation sheet;
// without it the service gate refuses with ERR_CONFIRMATION_REQUIRED, and
// DeleteSource is refused even with a preview. A run continues past
// per-item failures so the item list shows every outcome. While it runs it
// emits ItemEvent on EventImportItem.
func (im *Import) Run(ctx context.Context, opts ImportOptions) (*ImportOutcome, error) {
	return im.run(ctx, opts, false)
}

func (im *Import) run(ctx context.Context, opts ImportOptions, dryRun bool) (*ImportOutcome, error) {
	// The service defaults an empty policy and treats a conflict per it, but
	// an unknown one fails deep in the plan; reject it at the facade.
	switch service.ConflictPolicy(opts.Policy) {
	case "", service.ConflictFail, service.ConflictReplace, service.ConflictSkip, service.ConflictRename:
	default:
		return nil, toError(apperr.New(apperr.ErrUsage,
			fmt.Sprintf("unknown conflict policy %q (want fail, replace, skip, or rename)", opts.Policy)))
	}
	tracker := &itemTracker{emit: im.emitEvent, event: EventImportItem}
	res, err := im.state.current().ImportSaved(ctx, service.ImportSavedOptions{
		Into:          opts.Into,
		PhotosAs:      opts.PhotosAs,
		Policy:        service.ConflictPolicy(opts.Policy),
		MergeCaptions: opts.MergeCaptions,
		DeleteSource:  opts.DeleteSource,
		NoDedupe:      opts.NoDedupe,
		DryRun:        dryRun,
		Confirm:       opts.Confirm,
		// The GUI runs a batch the user watches item by item; a single
		// failure should not hide the rest of the outcomes.
		ContinueErr: true,
		PhotoPrompt: func(photos int) (string, error) {
			return im.askPhotos(ctx, photos)
		},
		Observer: tracker.observer(),
	})
	if err != nil {
		return nil, toError(err)
	}
	return importOutcome(res), nil
}

func (im *Import) emitEvent(name string, data any) {
	if im.emit != nil {
		im.emit(name, data)
	}
}

// AnswerPrompt supplies the photo presentation for the pending prompt:
// "document" or "photo".
func (im *Import) AnswerPrompt(_ context.Context, id, choice string) error {
	return im.resolve(id, promptAnswer{value: choice})
}

// CancelPrompt aborts the pending prompt, cancelling the import that asked.
func (im *Import) CancelPrompt(_ context.Context, id string) error {
	return im.resolve(id, promptAnswer{err: apperr.Cancelled()})
}

func (im *Import) resolve(id string, answer promptAnswer) error {
	im.mu.Lock()
	ch, ok := im.prompts[id]
	im.mu.Unlock()
	if !ok {
		return toError(apperr.New(apperr.ErrUsage, "no pending prompt with that id"))
	}
	// The buffer holds one answer; a duplicate drops instead of parking this
	// binding call on a channel nobody reads any more.
	select {
	case ch <- answer:
	default:
	}
	return nil
}

// askPhotos emits the photo prompt and waits for the frontend's answer.
func (im *Import) askPhotos(ctx context.Context, photos int) (string, error) {
	im.mu.Lock()
	im.seq++
	prompt := ImportPrompt{ID: fmt.Sprintf("import-prompt-%d", im.seq), Kind: PromptPhotos, Photos: photos}
	ch := make(chan promptAnswer, 1)
	im.prompts[prompt.ID] = ch
	emit := im.emit
	im.mu.Unlock()
	defer func() {
		im.mu.Lock()
		delete(im.prompts, prompt.ID)
		im.mu.Unlock()
	}()
	if emit == nil {
		return "", apperr.New(apperr.ErrUsage, "import prompt emitter not wired")
	}
	emit(EventImportPrompt, prompt)
	select {
	case answer := <-ch:
		return answer.value, answer.err
	case <-ctx.Done():
		return "", apperr.Cancelled()
	}
}

func importOutcome(res *service.ImportSavedResult) *ImportOutcome {
	out := &ImportOutcome{
		DryRun:          res.DryRun,
		Into:            res.Into,
		PhotosAs:        res.PhotosAs,
		HistoryComplete: res.HistoryComplete,
		Imported:        res.Imported,
		Skipped:         res.Skipped,
		Failed:          res.Failed,
		Duplicates:      res.Duplicates,
		CaptionsMerged:  res.CaptionsMerged,
		SourcesDeleted:  res.SourcesDeleted,
		Photos:          res.Photos,
		Items:           make([]ImportItem, 0, len(res.Items)),
	}
	for _, it := range res.Items {
		out.Items = append(out.Items, ImportItem{
			MessageID:     it.MessageID,
			Kind:          it.Kind,
			Action:        it.Action,
			Path:          it.Path,
			Size:          it.Size,
			Reason:        it.Reason,
			DuplicateOf:   it.DuplicateOf,
			SourceDeleted: it.SourceDeleted,
			Error:         it.Error,
		})
	}
	return out
}
