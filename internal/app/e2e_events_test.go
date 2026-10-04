package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestE2ECpEventsProgress pins the td cp --events part reports byte for byte
// (ADR 0042): one transfer.progress line per confirmed part of every
// resumable member, in order, each carrying its Transfer, then the final cp
// line. --upload-part-size-kb sets the part size the stream reports, so this
// also pins the per-call override.
func TestE2ECpEventsProgress(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "single", "big.bin"), 12*mib)
	bigProgress := []string{
		`{"file_name":"big.bin","index":0,"size":4194304,"uploaded":4194304,"total":12582912}`,
		`{"file_name":"big.bin","index":1,"size":4194304,"uploaded":8388608,"total":12582912}`,
		`{"file_name":"big.bin","index":2,"size":4194304,"uploaded":12582912,"total":12582912}`,
	}

	t.Run("single", func(t *testing.T) {
		lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath,
			"cp", big, "/single/big.bin", "--events", "--upload-part-size-kb", "4096", "--upload-threads", "2")
		assertProgressThenCp(t, lines, bigProgress)
	})

	t.Run("multi", func(t *testing.T) {
		small := e2eLocalFile(t, dir, "small.bin", "small")
		lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath,
			"cp", big, small, "/multi/", "--events", "--upload-part-size-kb", "4096")
		assertProgressThenCp(t, lines, bigProgress)
	})

	t.Run("recursive", func(t *testing.T) {
		tree := filepath.Join(dir, "tree")
		e2eSizedFile(t, filepath.Join(tree, "big.bin"), 12*mib)
		e2eLocalFile(t, tree, "small.bin", "small")
		lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath,
			"cp", "--recursive", tree, "/tree", "--events", "--upload-part-size-kb", "4096")
		assertProgressThenCp(t, lines, bigProgress)
	})

	t.Run("config part size", func(t *testing.T) {
		// Without the flag the configured part size applies; the default 0
		// leaves the choice to the client, which the fake makes 128 KiB.
		e2eSizedFile(t, filepath.Join(dir, "cfg", "cfg.bin"), 11*mib)
		lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath,
			"cp", filepath.Join(dir, "cfg", "cfg.bin"), "/cfg.bin", "--events")
		var want []string
		const part, total = 128 * 1024, 11 * mib
		for i := 0; i*part < total; i++ {
			want = append(want, fmt.Sprintf(`{"file_name":"cfg.bin","index":%d,"size":%d,"uploaded":%d,"total":%d}`,
				i, part, min((i+1)*part, total), total))
		}
		assertProgressThenCp(t, lines, want)
	})
}

// eventLine is one NDJSON envelope with its data kept as the exact bytes
// td wrote.
type eventLine struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
	Meta map[string]any  `json:"meta"`
}

func runE2EEventLines(t *testing.T, bin, cfgPath, dbPath, statePath string, args ...string) []eventLine {
	t.Helper()
	stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", prependJSON(args...)...)
	if err != nil {
		t.Fatalf("%v: stdout=%s stderr=%s", args, stdout, stderr)
	}
	var out []eventLine
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var ev eventLine
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("bad event line %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

func assertProgressThenCp(t *testing.T, lines []eventLine, progress []string) {
	t.Helper()
	lines = slices.DeleteFunc(slices.Clone(lines), func(ev eventLine) bool {
		return ev.Meta["command"] == "transfer.stage"
	})
	if len(lines) != len(progress)+1 {
		t.Fatalf("got %d event lines, want %d progress + 1 cp", len(lines), len(progress))
	}
	for i, want := range progress {
		ev := lines[i]
		if !ev.OK || ev.Meta["command"] != "transfer.progress" || ev.Meta["schema_version"] == nil {
			t.Fatalf("line %d envelope = ok:%v meta:%v, want a transfer.progress success", i, ev.OK, ev.Meta)
		}
		var data struct {
			ID   string          `json:"id"`
			Part json.RawMessage `json:"part"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.ID == "" {
			t.Fatalf("line %d data = %s, want the Transfer the part belongs to", i, ev.Data)
		}
		if string(data.Part) != want {
			t.Fatalf("line %d part = %s, want %s", i, data.Part, want)
		}
	}
	last := lines[len(lines)-1]
	if !last.OK || last.Meta["command"] != "cp" {
		t.Fatalf("last line = ok:%v meta:%v, want the cp result", last.OK, last.Meta)
	}
}

// e2eSizedFile writes size bytes of a repeating pattern to path, creating its
// directory.
func e2eSizedFile(t *testing.T, path string, size int) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("0123456789abcdef"), size/16), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
