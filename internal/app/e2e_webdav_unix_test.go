//go:build !windows

package app

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestE2EWebDAVLoopbackRangeAndCancellation(t *testing.T) {
	dir := t.TempDir()
	bin, cfg, db, state, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfg, db, state)
	runE2EJSON(t, bin, cfg, db, state, "init", root, "--create-channel=Drive")
	source := e2eLocalFile(t, dir, "file.txt", "0123456789")
	runE2EJSON(t, bin, cfg, db, state, "cp", source, "/safe/file.txt")
	cmd, scanner, stderr := startTD(t, bin, cfg, db, state, nil, "--json", "webdav", "/safe", "--listen=127.0.0.1:0")
	watchdog := time.AfterFunc(20*time.Second, func() { _ = cmd.Process.Kill() })
	defer watchdog.Stop()
	if !scanner.Scan() {
		t.Fatalf("no startup receipt: %s", stderr)
	}
	var envelope e2eEnvelope
	if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil || !envelope.OK {
		t.Fatalf("startup=%s err=%v", scanner.Text(), err)
	}
	address := envelope.Data["address"].(string)
	req, err := http.NewRequest("GET", "http://"+address+"/file.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=7-9")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 206 || string(data) != "789" {
		t.Fatalf("response=%d bytes=%q err=%v", response.StatusCode, data, err)
	}
	prop, err := http.NewRequest("PROPFIND", "http://"+address+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	prop.Header.Set("Depth", "1")
	listing, err := client.Do(prop)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(listing.Body)
	_ = listing.Body.Close()
	if err != nil || listing.StatusCode != 207 || !strings.Contains(string(body), "file.txt") {
		t.Fatalf("listing=%d %s %v", listing.StatusCode, body, err)
	}
	lines, code, took := interruptAndWait(t, cmd, scanner)
	assertCancelled(t, lines, code, took, stderr.String())
	runE2EExpectError(t, bin, cfg, db, state, 2, "ERR_USAGE", "webdav", "/safe", "--listen=0.0.0.0:8080")
	// The read-only server does not alter its source.
	runE2EJSON(t, bin, cfg, db, state, "get", "/safe/file.txt", filepath.Join(dir, "downloaded.txt"))
}
