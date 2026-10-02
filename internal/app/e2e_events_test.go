package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2ECpEventsProgress pins the td cp --events NDJSON stream byte for byte
// (ADR 0005): one cp.progress line per confirmed part of every resumable
// member, in order, then the final cp line. --upload-part-size-kb sets the
// part size the stream reports, so this also pins the per-call override.
func TestE2ECpEventsProgress(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")

	const mib = 1024 * 1024
	big := e2eSizedFile(t, filepath.Join(dir, "single", "big.bin"), 12*mib)
	bigProgress := []string{
		`{"FileName":"big.bin","Part":0,"PartSize":4194304,"Uploaded":4194304,"Total":12582912}`,
		`{"FileName":"big.bin","Part":1,"PartSize":4194304,"Uploaded":8388608,"Total":12582912}`,
		`{"FileName":"big.bin","Part":2,"PartSize":4194304,"Uploaded":12582912,"Total":12582912}`,
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
			want = append(want, fmt.Sprintf(`{"FileName":"cfg.bin","Part":%d,"PartSize":%d,"Uploaded":%d,"Total":%d}`,
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
	if len(lines) != len(progress)+1 {
		t.Fatalf("got %d event lines, want %d progress + 1 cp", len(lines), len(progress))
	}
	for i, want := range progress {
		ev := lines[i]
		if !ev.OK || ev.Meta["command"] != "cp.progress" || ev.Meta["schema_version"] == nil {
			t.Fatalf("line %d envelope = ok:%v meta:%v, want a cp.progress success", i, ev.OK, ev.Meta)
		}
		if string(ev.Data) != want {
			t.Fatalf("line %d data = %s, want %s", i, ev.Data, want)
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
