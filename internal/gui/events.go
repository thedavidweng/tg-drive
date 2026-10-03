//go:build gui

package gui

import (
	"sync"

	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// Emitter emits a typed event to the frontend. cmd/td-gui adapts the Wails
// event manager to it; tests record calls. A nil Emitter drops events.
type Emitter func(name string, data any)

// Typed event names. cmd/td-gui/events.go registers exactly these with
// application.RegisterEvent, so the binding generator types them for the
// frontend.
const (
	// EventDirectoryChanged carries DirectoryChanged: the index changed
	// under another writer and the shown directory was re-read.
	EventDirectoryChanged = "directory-changed"
	// EventScanProgress carries ScanProgress while a scan runs.
	EventScanProgress = "scan-progress"
	// EventTransferStage carries Transfer: a Transfer is new or entered a
	// stage, terminal stages included. Both the GUI's own Transfers (via
	// the Manager's Observer) and other processes' (via index sync) feed
	// it.
	EventTransferStage = "transfer-stage"
	// EventTransferProgress carries Transfer: a Transfer moved within its
	// stage (bytes, items, or its cancel flag), at most as often as the
	// Manager writes progress to the index.
	EventTransferProgress = "transfer-progress"
	// EventTransferRemoved carries TransferRemoved: a Transfer left the
	// index (cleared or pruned).
	EventTransferRemoved = "transfer-removed"
	// EventFilesDropped carries FilesDropped: files were dropped onto a
	// drop-target element of the window.
	EventFilesDropped = "files-dropped"
	// EventImportPrompt carries ImportPrompt: an import found photo
	// messages and needs to know how to republish them. The frontend
	// answers with Import.AnswerPrompt (or Import.CancelPrompt).
	EventImportPrompt = "import.prompt"
	// EventImportItem carries ItemEvent once per imported saved message.
	EventImportItem = "import.item"
	// EventRepairItem carries ItemEvent once per repaired item.
	EventRepairItem = "repair.item"
	// EventChannelsChanged carries ChannelsChanged: the bound channels or
	// the active one changed, by this GUI or another process (td init).
	EventChannelsChanged = "channels-changed"
)

// ChannelsChanged is the payload of EventChannelsChanged: the bound
// channels as Channels.List returns them.
type ChannelsChanged struct {
	Channels []ChannelInfo `json:"channels"`
}

// TransferRemoved is the payload of EventTransferRemoved.
type TransferRemoved struct {
	ID string `json:"id"`
}

// FilesDropped is the payload of EventFilesDropped: the absolute local
// paths the OS reported for the drop. cmd/td-gui translates the window's
// native drop event into it; the facade itself never sees the window.
type FilesDropped struct {
	Paths []string `json:"paths"`
}

// ImportPrompt is the import.prompt event payload. The photo presentation
// has no safe default (documents keep the bytes, native photos are
// recompressed), so an import that meets photos without a choice asks.
type ImportPrompt struct {
	ID string `json:"id"`
	// Kind is PromptPhotos, the only import prompt.
	Kind string `json:"kind"`
	// Photos is how many photo messages wait on the answer.
	Photos int `json:"photos"`
}

// Prompt kinds emitted on the import.prompt event.
const PromptPhotos = "photos"

// ItemEvent is the import.item and repair.item payload: one item's outcome
// plus the running tally, so the frontend renders item-by-item progress.
type ItemEvent struct {
	Path      string `json:"path,omitempty"`
	MessageID int    `json:"message_id,omitempty"`
	// Status is "completed", "skipped", or "failed" (service.ItemStatus).
	Status string `json:"status"`
	// Error is why a failed item failed.
	Error     string `json:"error,omitempty"`
	Completed int    `json:"completed"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
}

// itemTracker turns a call's per-item observer results into ItemEvent
// emissions with a running tally. Observer callbacks may run on several
// goroutines at once.
type itemTracker struct {
	emit  func(name string, data any)
	event string

	mu                         sync.Mutex
	completed, skipped, failed int
}

func (t *itemTracker) observer() service.Observer {
	return service.Observer{OnItem: t.onItem}
}

func (t *itemTracker) onItem(r service.ItemResult) {
	t.mu.Lock()
	switch r.Status {
	case service.ItemCompleted:
		t.completed++
	case service.ItemSkipped:
		t.skipped++
	case service.ItemFailed:
		t.failed++
	}
	ev := ItemEvent{
		Path:      r.Item.Path,
		MessageID: r.Item.MessageID,
		Status:    string(r.Status),
		Completed: t.completed,
		Skipped:   t.skipped,
		Failed:    t.failed,
	}
	if r.Err != nil {
		ev.Error = r.Err.Error()
	}
	t.mu.Unlock()
	t.emit(t.event, ev)
}

// DirectoryChanged is the payload of EventDirectoryChanged: the refreshed
// listing of the directory the frontend is showing.
type DirectoryChanged struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

// ScanProgress is the payload of EventScanProgress.
type ScanProgress struct {
	// Stage is the scan's current stage: "reading" or "indexing".
	Stage string `json:"stage"`
	// Indexed is how many files the scan has indexed so far.
	Indexed int `json:"indexed"`
	// Failed is how many scan errors the scan has recorded so far.
	Failed int `json:"failed"`
}
