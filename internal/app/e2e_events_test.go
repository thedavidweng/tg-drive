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
// (ADR 0045): one transfer.progress line per confirmed part of every
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
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
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

// TestE2EGetEvents: td get runs as a Transfer too, so --events streams one
// transfer.stage line per stage it enters and then the final get line. A
// download confirms no upload parts, so the stream carries no
// transfer.progress. Without --events the command keeps its single-result
// output, so the flag is what turns the stream on rather than a change of the
// default.
func TestE2EGetEvents(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, "evt.txt", "events"), "/evt.txt")

	t.Run("single", func(t *testing.T) {
		dest := filepath.Join(dir, "single", "evt.txt")
		lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath, "get", "/evt.txt", dest, "--events")
		assertStagesThenFinal(t, lines, "get", "download")
		if got, err := os.ReadFile(dest); err != nil || string(got) != "events" {
			t.Fatalf("downloaded %q err=%v", got, err)
		}
	})

	t.Run("recursive", func(t *testing.T) {
		dest := filepath.Join(dir, "tree")
		lines := runE2EEventLines(t, bin, cfgPath, dbPath, statePath, "get", "--recursive", "/evt.txt", dest, "--events")
		assertStagesThenFinal(t, lines, "get", "recursive_download")
	})
}

func TestE2EMultiGroupEventsFirstStageOnly(t *testing.T) {
	dir := t.TempDir()
	bin, cfg, db, state, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfg, db, state)
	runE2EJSON(t, bin, cfg, db, state, "init", root, "--create-channel=Drive")
	tree := filepath.Join(dir, "tree")
	for _, group := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(tree, group), 0o755); err != nil {
			t.Fatal(err)
		}
		e2eLocalFile(t, filepath.Join(tree, group), "file.bin", group)
	}
	upload := runE2EEventLines(t, bin, cfg, db, state, "cp", "--recursive", tree, "/tree", "--events")
	assertUniqueStages(t, upload, "recursive_upload", "cp", []string{"queued", "hashing", "uploading", "publishing", "completed"})
	download := runE2EEventLines(t, bin, cfg, db, state, "get", "--recursive", "/tree", filepath.Join(dir, "download"), "--events")
	assertUniqueStages(t, download, "recursive_download", "get", []string{"queued", "downloading", "completed"})
	for _, group := range []string{"a", "b"} {
		data, err := os.ReadFile(filepath.Join(dir, "download", group, "file.bin"))
		if err != nil || string(data) != group {
			t.Fatalf("download %s = %q, err %v", group, data, err)
		}
	}
	var albumArgs []string
	for i := range 11 {
		albumArgs = append(albumArgs, e2eLocalFile(t, dir, fmt.Sprintf("album-%02d.bin", i), "album"))
	}
	albumArgs = append(albumArgs, "/album/", "--events")
	album := runE2EEventLines(t, bin, cfg, db, state, append([]string{"cp"}, albumArgs...)...)
	assertUniqueStages(t, album, "album_upload", "cp", []string{"queued", "hashing", "uploading", "publishing", "completed"})
}

func assertUniqueStages(t *testing.T, lines []eventLine, kind, command string, want []string) {
	t.Helper()
	var stages []string
	var id string
	for _, line := range lines {
		if line.Meta["command"] != "transfer.stage" {
			continue
		}
		var data struct {
			ID, Kind, Stage string
		}
		if err := json.Unmarshal(line.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.ID == "" || data.Kind != kind || (id != "" && id != data.ID) {
			t.Fatalf("unexpected transfer: %s", line.Data)
		}
		id = data.ID
		stages = append(stages, data.Stage)
	}
	if !slices.Equal(stages, want) || lines[len(lines)-1].Meta["command"] != command {
		t.Fatalf("stages = %v, want %v and final %s", stages, want, command)
	}
}

// assertStagesThenFinal pins one --events stream: every line before the last
// is a transfer.stage of the given kind, and the last is the command's own
// result.
func assertStagesThenFinal(t *testing.T, lines []eventLine, command, kind string) {
	t.Helper()
	if len(lines) < 3 {
		t.Fatalf("got %d event lines, want at least queued, downloading, completed, and %s", len(lines), command)
	}
	for i, ev := range lines[:len(lines)-1] {
		if !ev.OK || ev.Meta["command"] != "transfer.stage" {
			t.Fatalf("line %d envelope = ok:%v meta:%v, want a transfer.stage success", i, ev.OK, ev.Meta)
		}
		var data struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Stage string `json:"stage"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.ID == "" || data.Kind != kind {
			t.Fatalf("line %d data = %s, want a %s Transfer", i, ev.Data, kind)
		}
		if data.Stage == "" {
			t.Fatalf("line %d data = %s, want a stage", i, ev.Data)
		}
	}
	last := lines[len(lines)-1]
	if !last.OK || last.Meta["command"] != command {
		t.Fatalf("last line = ok:%v meta:%v, want the %s result", last.OK, last.Meta, command)
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
