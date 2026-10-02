//go:build gui

package gui

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
)

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
