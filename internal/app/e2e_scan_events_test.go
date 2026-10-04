package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestE2EScanEvents pins the td scan --events NDJSON stream: the reading and
// indexing stages, one scan.item per indexed file with running tallies, then
// the final scan result, every line sharing one request_id. Without --json
// stdout carries the stream and nothing else.
func TestE2EScanEvents(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	for _, name := range []string{"a.bin", "b.bin"} {
		runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", e2eLocalFile(t, dir, name, name), "/s/"+name)
	}

	for _, args := range [][]string{
		{"scan", "--full", "--events"},
		{"--json", "scan", "--full", "--events"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", args...)
			if err != nil {
				t.Fatalf("%v: %v stdout=%s stderr=%s", args, err, stdout, stderr)
			}
			var lines []eventLine
			for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
				var ev eventLine
				if err := json.Unmarshal([]byte(line), &ev); err != nil {
					t.Fatalf("bad event line %q: %v", line, err)
				}
				lines = append(lines, ev)
			}

			var got []string
			requestID := lines[0].Meta["request_id"]
			for i, ev := range lines {
				if !ev.OK || ev.Meta["schema_version"] == nil {
					t.Fatalf("line %d envelope = ok:%v meta:%v, want a success", i, ev.OK, ev.Meta)
				}
				if id := ev.Meta["request_id"]; id == nil || id == "" || id != requestID {
					t.Fatalf("line %d request_id = %v, want %v shared by every line", i, id, requestID)
				}
				cmd := fmt.Sprint(ev.Meta["command"])
				switch cmd {
				case "scan.item":
					var it struct {
						MessageID int    `json:"message_id"`
						Path      string `json:"path"`
						Status    string `json:"status"`
						Indexed   int    `json:"indexed"`
						Failed    int    `json:"failed"`
					}
					if err := json.Unmarshal(ev.Data, &it); err != nil {
						t.Fatal(err)
					}
					if it.MessageID <= 0 {
						t.Fatalf("line %d message_id = %d, want the indexed message", i, it.MessageID)
					}
					got = append(got, fmt.Sprintf("%s %s %s %d/%d", cmd, it.Path, it.Status, it.Indexed, it.Failed))
				case "scan":
					var res struct {
						Mode   string `json:"mode"`
						Active int    `json:"active"`
					}
					if err := json.Unmarshal(ev.Data, &res); err != nil {
						t.Fatal(err)
					}
					got = append(got, fmt.Sprintf("%s %s %d", cmd, res.Mode, res.Active))
				default:
					got = append(got, cmd+" "+string(ev.Data))
				}
			}
			want := []string{
				`scan.stage {"stage":"reading"}`,
				`scan.stage {"stage":"indexing"}`,
				"scan.item /s/a.bin completed 1/0",
				"scan.item /s/b.bin completed 2/0",
				"scan full 2",
			}
			if !slices.Equal(got, want) {
				t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
