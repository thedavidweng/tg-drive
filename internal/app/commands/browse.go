package commands

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/output"
	"github.com/thedavidweng/tg-drive/internal/service"
)

// Scan, listing, and tree browsing commands plus their display helpers.

func NewScanCmd(rt Runtime) *cobra.Command {
	var full, strict, repair, includeDeleted, events bool
	c := &cobra.Command{
		Use:   "scan [remote-root]",
		Short: "Scan Telegram channel and rebuild index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			root := "/"
			if len(args) > 0 {
				root = args[0]
			}
			opts := service.ScanOptions{
				Full: full, Strict: strict, Repair: repair, IncludeDeleted: includeDeleted, Root: root,
			}
			if events {
				opts.Observer = (&scanEvents{r: r}).observer()
			}
			data, err := app.Scan(cmd.Context(), opts)
			if err != nil {
				return r.Error(err)
			}
			if events {
				return r.Event("scan", data)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			if data.FullScanWarning != "" {
				fmt.Fprintln(os.Stderr, "warning: "+data.FullScanWarning)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "scan complete (%v): %v active, %v deleted, %v invalid, %v missing\n",
				data.Mode, data.Active, data.Deleted, data.Invalid, data.Missing)
			return nil
		},
	}
	c.Flags().BoolVar(&full, "full", false, "full scan")
	c.Flags().BoolVar(&strict, "strict", false, "exit on invalid messages")
	c.Flags().BoolVar(&repair, "repair", false, "repair DB-only inconsistencies")
	c.Flags().BoolVar(&includeDeleted, "include-deleted", false, "record tombstoned files")
	c.Flags().BoolVar(&events, "events", false, "emit NDJSON progress events during the scan")
	return c
}

// ScanStageEvent is the payload of a scan.stage event.
type ScanStageEvent struct {
	Stage service.Stage `json:"stage"`
}

// ScanItemEvent is the payload of a scan.item event: one message the scan
// indexed or recorded a scan error for, plus the running tallies so far.
type ScanItemEvent struct {
	MessageID int                `json:"message_id"`
	Path      string             `json:"path,omitempty"`
	Status    service.ItemStatus `json:"status"`
	Error     *ScanItemError     `json:"error,omitempty"`
	Indexed   int                `json:"indexed"`
	Failed    int                `json:"failed"`
}

// ScanItemError is the scan error a failed scan.item recorded.
type ScanItemError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// scanEvents turns the scan's observer callbacks into NDJSON events.
// Observer callbacks may run on several goroutines at once; the lock also
// keeps the tallies in the order the lines are written.
type scanEvents struct {
	r *output.Renderer

	mu      sync.Mutex
	indexed int
	failed  int
}

func (s *scanEvents) observer() service.Observer {
	return service.Observer{
		OnStage: func(_ service.Item, st service.Stage) {
			s.mu.Lock()
			defer s.mu.Unlock()
			_ = s.r.Event("scan.stage", ScanStageEvent{Stage: st})
		},
		OnItem: func(res service.ItemResult) {
			s.mu.Lock()
			defer s.mu.Unlock()
			ev := ScanItemEvent{MessageID: res.Item.MessageID, Path: res.Item.Path, Status: res.Status}
			if res.Status == service.ItemFailed {
				s.failed++
				ev.Error = &ScanItemError{Code: apperr.ErrScanFailed, Message: fmt.Sprint(res.Err)}
				if ae, ok := apperr.As(res.Err); ok {
					ev.Error = &ScanItemError{Code: ae.Code, Message: ae.Message}
				}
			} else {
				s.indexed++
			}
			ev.Indexed, ev.Failed = s.indexed, s.failed
			_ = s.r.Event("scan.item", ev)
		},
	}
}

func NewLsCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "ls [remote-path]",
		Short: "List remote directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			p := "/"
			if len(args) > 0 {
				p = args[0]
			}
			entries, err := app.ListDir(cmd.Context(), p)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]any{"path": p, "entries": entries})
			}
			width := 0
			for _, e := range entries {
				n := displayWidth(e.Name)
				if e.Type == "dir" {
					n++
				}
				if n > width {
					width = n
				}
			}
			for _, e := range entries {
				if e.Type == "dir" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-4s %s/\n", "DIR", e.Name)
				} else {
					pad := width - displayWidth(e.Name)
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-4s %s%s  %8s\n", "FILE", e.Name, strings.Repeat(" ", pad), humanSize(e.Size))
				}
			}
			return nil
		},
	}
}

// displayWidth approximates the terminal cell width of s: East Asian wide
// glyphs occupy two cells, everything else one.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, symbols, punctuation
		r >= 0x3041 && r <= 0x33FF, // Hiragana..CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x2FFFD, // CJK ext B+
		r >= 0x30000 && r <= 0x3FFFD:
		return true
	}
	return false
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func NewTreeCmd(rt Runtime) *cobra.Command {
	var depth int
	c := &cobra.Command{
		Use:   "tree [remote-path]",
		Short: "Show remote tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			p := "/"
			if len(args) > 0 {
				p = args[0]
			}
			nodes, err := app.Tree(cmd.Context(), p, depth)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]any{"path": p, "tree": nodes})
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, p)
			renderTree(out, nodes, "")
			return nil
		},
	}
	c.Flags().IntVar(&depth, "depth", 0, "max depth")
	return c
}

func renderTree(w io.Writer, nodes []service.TreeNode, prefix string) {
	for i, n := range nodes {
		connector, childPrefix := "├── ", prefix+"│   "
		if i == len(nodes)-1 {
			connector, childPrefix = "└── ", prefix+"    "
		}
		_, _ = fmt.Fprintf(w, "%s%s%s\n", prefix, connector, n.Name)
		renderTree(w, n.Children, childPrefix)
	}
}
