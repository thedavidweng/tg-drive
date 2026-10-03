// Package transfer is the Transfer Manager (ADR 0033): every front end
// submits uploads and downloads through it, and it records each one as a
// Transfer in the shared index so other processes see it.
package transfer

import (
	"fmt"
	"strings"
	"time"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

// Kind is what a Transfer moves.
type Kind string

const (
	// KindUpload uploads one local file.
	KindUpload Kind = "upload"
	// KindDownload downloads one remote file. Its Source is the remote
	// path and Dest the local file, the reverse of an upload.
	KindDownload Kind = "download"
	// KindAlbumUpload uploads several local files as Telegram albums to one
	// remote directory. Its Source is empty; the options carry the sources.
	KindAlbumUpload Kind = "album_upload"
	// KindRecursiveUpload uploads one local directory tree.
	KindRecursiveUpload Kind = "recursive_upload"
	// KindRecursiveDownload downloads one remote directory tree. Its Source
	// is the remote path and Dest the local directory.
	KindRecursiveDownload Kind = "recursive_download"
)

// Stage is where a Transfer is in its lifecycle. A Transfer only moves
// forward through the stages, ending in exactly one terminal stage.
type Stage string

const (
	StageQueued      Stage = "queued"
	StageHashing     Stage = "hashing"
	StageUploading   Stage = "uploading"
	StageDownloading Stage = "downloading"
	StagePublishing  Stage = "publishing"
	StageCompleted   Stage = "completed"
	// StageFailed ends a Transfer whose call failed. A cancellation is not
	// a failure: it ends the Transfer cancelled.
	StageFailed Stage = "failed"
	// StageCancelled ends a Transfer its owner cancelled — by Ctrl-C on
	// the owning command or by a cancel request from another process.
	StageCancelled Stage = "cancelled"
	// StageInterrupted ends a Transfer whose owner vanished mid-run
	// (killed or crashed), which a reading process marks once the
	// Transfer's lease expires. Retrying it resumes from saved state.
	StageInterrupted Stage = "interrupted"
)

// lifecycle is every Stage in the order a Transfer moves through them. One
// Transfer never visits both upload and download stages; the shared order
// only fixes how forward each step is.
var lifecycle = []Stage{StageQueued, StageHashing, StageUploading, StageDownloading, StagePublishing, StageCompleted, StageFailed, StageCancelled, StageInterrupted}

func (s Stage) rank() int {
	for i, st := range lifecycle {
		if st == s {
			return i
		}
	}
	return -1
}

// Terminal reports whether a Transfer in s has ended.
func (s Stage) Terminal() bool {
	return s == StageCompleted || s == StageFailed || s == StageCancelled || s == StageInterrupted
}

// ParseStage reads a stage name, failing with ERR_USAGE for an unknown one.
func ParseStage(name string) (Stage, error) {
	if s := Stage(name); s.rank() >= 0 {
		return s, nil
	}
	names := make([]string, len(lifecycle))
	for i, s := range lifecycle {
		names[i] = string(s)
	}
	return "", apperr.New(apperr.ErrUsage,
		fmt.Sprintf("unknown transfer stage %q; valid stages: %s", name, strings.Join(names, ", ")))
}

// FrontEnd is the program that created a Transfer.
type FrontEnd string

// FrontEnd values: the creating program.
const (
	// FrontEndCLI is the td command line.
	FrontEndCLI FrontEnd = "cli"
	// FrontEndGUI is the td-gui desktop app.
	FrontEndGUI FrontEnd = "gui"
)

// Transfer is one user-requested upload or download as the index records
// it.
type Transfer struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	Stage Stage  `json:"stage"`
	// Channel is the Telegram ID of the drive channel the Transfer works on.
	Channel string `json:"channel"`
	// Source is the absolute local path of an upload.
	Source string `json:"source"`
	// Dest is the remote path an upload was asked to write; once it
	// completes, the canonical path it wrote.
	Dest       string `json:"dest"`
	BytesDone  int64  `json:"bytes_done"`
	BytesTotal int64  `json:"bytes_total"`
	// ItemsDone counts the items completed or skipped, ItemsFailed the
	// items that failed, of ItemsTotal. A single-file Transfer is one item;
	// a recursive Transfer's ItemsTotal grows as the walk reports items.
	ItemsDone  int `json:"items_done"`
	ItemsTotal int `json:"items_total"`
	// ItemsFailed is omitted when nothing failed, keeping the shape a
	// clean Transfer has had since the first kind.
	ItemsFailed int `json:"items_failed,omitempty"`
	// ErrorCode and ErrorMessage are the error a failed Transfer ended
	// with.
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	FrontEnd        FrontEnd   `json:"front_end"`
	CancelRequested bool       `json:"cancel_requested"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

// timeLayout is fixed-width so the stored text sorts in time order;
// RFC3339Nano trims trailing zeros and does not.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeLayout, s)
	return t
}

func (t Transfer) row() sqlitestore.TransferRow {
	r := sqlitestore.TransferRow{
		ID:              t.ID,
		Kind:            string(t.Kind),
		ChannelTGID:     t.Channel,
		Source:          t.Source,
		Dest:            t.Dest,
		Stage:           string(t.Stage),
		BytesDone:       t.BytesDone,
		BytesTotal:      t.BytesTotal,
		ItemsDone:       t.ItemsDone,
		ItemsTotal:      t.ItemsTotal,
		ItemsFailed:     t.ItemsFailed,
		ErrorCode:       t.ErrorCode,
		ErrorMessage:    t.ErrorMessage,
		FrontEnd:        string(t.FrontEnd),
		CancelRequested: t.CancelRequested,
		CreatedAt:       formatTime(t.CreatedAt),
		UpdatedAt:       formatTime(t.UpdatedAt),
	}
	if t.FinishedAt != nil {
		r.FinishedAt = formatTime(*t.FinishedAt)
	}
	return r
}

func fromRow(r sqlitestore.TransferRow) Transfer {
	t := Transfer{
		ID:              r.ID,
		Kind:            Kind(r.Kind),
		Stage:           Stage(r.Stage),
		Channel:         r.ChannelTGID,
		Source:          r.Source,
		Dest:            r.Dest,
		BytesDone:       r.BytesDone,
		BytesTotal:      r.BytesTotal,
		ItemsDone:       r.ItemsDone,
		ItemsTotal:      r.ItemsTotal,
		ItemsFailed:     r.ItemsFailed,
		ErrorCode:       r.ErrorCode,
		ErrorMessage:    r.ErrorMessage,
		FrontEnd:        FrontEnd(r.FrontEnd),
		CancelRequested: r.CancelRequested,
		CreatedAt:       parseTime(r.CreatedAt),
		UpdatedAt:       parseTime(r.UpdatedAt),
	}
	if r.FinishedAt != "" {
		f := parseTime(r.FinishedAt)
		t.FinishedAt = &f
	}
	return t
}
