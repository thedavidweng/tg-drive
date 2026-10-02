package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2EObservedCommandsOutput pins, byte for byte, the human and JSON
// stdout of the long-running commands whose service calls report to an
// observer: get (single and recursive), scan, every repair mode, adopt, and
// import saved. Reporting is a service concern; none of it may reach the
// CLI's output.
func TestE2EObservedCommandsOutput(t *testing.T) {
	dir := t.TempDir()
	bin, cfgPath, dbPath, statePath, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfgPath, dbPath, statePath)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "init", root, "--create-channel=Drive")
	src := filepath.Join(dir, "src")
	e2eSizedFile(t, filepath.Join(src, "a.bin"), 2048)
	e2eSizedFile(t, filepath.Join(src, "sub", "b.bin"), 4096)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", "--recursive", src, "/docs")
	plain := e2eSizedFile(t, filepath.Join(dir, "plain.bin"), 1024)
	runE2EJSON(t, bin, cfgPath, dbPath, statePath, "cp", "--no-hash", plain, "/plain.bin")

	steps := []struct {
		args []string
		want string
	}{
		{
			[]string{"get", "/docs/a.bin", filepath.Join(dir, "out", "a.bin")},
			"downloaded /docs/a.bin -> <dir>/out/a.bin (2.0 KB)\n",
		},
		{
			[]string{"--json", "get", "/docs/a.bin", filepath.Join(dir, "out", "a.bin"), "--skip-existing"},
			`{"path":"/docs/a.bin","local":"<dir>/out/a.bin","size":2048,"skipped":true}`,
		},
		{
			[]string{"get", "--recursive", "/docs", filepath.Join(dir, "tree")},
			"downloaded /docs -> <dir>/tree (2 files, 0 skipped, 0 failed)\n",
		},
		{
			[]string{"--json", "get", "--recursive", "/docs", filepath.Join(dir, "tree"), "--skip-existing"},
			`{"downloaded":0,"errors":null,"failed":0,"local":"<dir>/tree","path":"/docs","skipped":2}`,
		},
		{
			[]string{"scan", "--full"},
			"scan complete (full): 3 active, 0 deleted, 0 invalid, 0 missing\n",
		},
		{
			[]string{"--json", "scan"},
			`{"active":3,"channel":"1001","deleted":0,"invalid":0,"missing":0,"mode":"incremental"}`,
		},
		{
			[]string{"repair", "--pending"},
			"invalid: 0\nlocks_cleared: 0\norphaned: 0\nrepaired: 0\nskipped: 0\n",
		},
		{
			[]string{"--json", "repair", "--orphaned"},
			`{"deleted":0,"invalid":0,"repaired":0}`,
		},
		{
			[]string{"--json", "repair", "--scan-errors"},
			`{"pending":0,"resolved":0}`,
		},
		{
			[]string{"--json", "repair", "--hash"},
			`{"backfilled":1,"failed":0,"items":[{"path":"/plain.bin","action":"backfilled"}],"total":1}`,
		},
		{
			[]string{"--json", "repair", "--captions", "--dry-run"},
			`{"cleaned":0,"dry_run":true,"failed":0,"items":[{"path":"/docs/a.bin","message_id":1,"action":"skipped","reason":"already clean or scaffold not exact"},{"path":"/docs/sub/b.bin","message_id":4,"action":"skipped","reason":"already clean or scaffold not exact"},{"path":"/plain.bin","message_id":7,"action":"skipped","reason":"already clean or scaffold not exact"}],"planned":0,"skipped":3,"total":3}`,
		},
		{
			[]string{"--json", "repair", "/docs/a.bin"},
			`{"repaired":"/docs/a.bin"}`,
		},
		{
			[]string{"adopt", "--unmanaged", "--dry-run"},
			"adopt dry-run: 0 adopted, 3 skipped, 0 failed\n" +
				"  skip  msg 7  already indexed\n" +
				"  skip  msg 4  already indexed\n" +
				"  skip  msg 1  already indexed\n",
		},
		{
			[]string{"import", "saved", "--dry-run", "--photos-as", "document"},
			"import saved dry-run: 0 imported, 0 skipped (0 duplicate), 0 failed\n",
		},
	}
	for _, step := range steps {
		stdout, stderr, err := runTD(t, bin, cfgPath, dbPath, statePath, nil, "", step.args...)
		if err != nil {
			t.Fatalf("%v: %v stdout=%s stderr=%s", step.args, err, stdout, stderr)
		}
		got := strings.ReplaceAll(stdout, dir, "<dir>")
		if step.args[0] == "--json" {
			// The envelope's meta carries a request id and a duration; the
			// payload is what must not change.
			var env eventLine
			if err := json.Unmarshal([]byte(got), &env); err != nil || !env.OK {
				t.Fatalf("%v: bad envelope %q: %v", step.args, got, err)
			}
			got = string(env.Data)
		}
		if got != step.want {
			t.Errorf("%v stdout:\n%s\nwant:\n%s", step.args, got, step.want)
		}
	}
}
