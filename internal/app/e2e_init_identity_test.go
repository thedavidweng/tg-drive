package app

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestE2EInitRemoteDuplicateTitle(t *testing.T) {
	dir := t.TempDir()
	bin, cfg, db, state, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfg, db, state)
	first := runE2EJSON(t, bin, cfg, db, state, "init", root, "--create-channel=Same")
	second := runE2EJSON(t, bin, cfg, db, state, "init", filepath.Join(dir, "second"), "--create-channel=Same")
	bindRoot := filepath.Join(dir, "new-binding")
	runE2EExpectError(t, bin, cfg, db, state, 2, "ERR_USAGE",
		"init", bindRoot, "--bind-channel=Same")
	runE2EExpectError(t, bin, cfg, db, state, 4, "ERR_CHANNEL_NOT_FOUND",
		"--root", bindRoot, "ls", "/")
	for _, created := range []map[string]any{first, second} {
		id := fmt.Sprint(created["channel_id"])
		got := runE2EJSON(t, bin, cfg, db, state,
			"init", bindRoot, "--bind-channel="+id)
		if fmt.Sprint(got["channel_id"]) != id {
			t.Fatalf("explicit binding = %v; want ID %s", got, id)
		}
	}
}
