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
)

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
