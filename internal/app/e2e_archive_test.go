package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive/adapters/native/fileperm"
)

func TestE2EEncryptedArchiveKeyOwnershipAndRestore(t *testing.T) {
	dir := t.TempDir()
	bin, cfg, db, state, root := e2eSetup(t, dir)
	e2eLogin(t, bin, cfg, db, state)
	runE2EJSON(t, bin, cfg, db, state, "init", root, "--create-channel=Drive")
	key := filepath.Join(dir, "identity.txt")
	generated := runE2EJSON(t, bin, cfg, db, state, "archive", "keygen", key)
	recipient := generated["recipient"].(string)
	if private, err := fileperm.IsPrivate(key); err != nil || !private {
		t.Fatalf("identity not private: %v %v", private, err)
	}
	original, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(generated), strings.TrimSpace(string(original))) {
		t.Fatal("private identity exposed in command output")
	}
	runE2EExpectError(t, bin, cfg, db, state, 2, "ERR_LOCAL_PATH_EXISTS", "archive", "keygen", key)
	unchanged, err := os.ReadFile(key)
	if err != nil || string(unchanged) != string(original) {
		t.Fatal("existing key changed")
	}
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	e2eLocalFile(t, source, "private.txt", "encrypted round trip")
	uploaded := runE2EJSON(t, bin, cfg, db, state, "archive", "put", source, "/archive.tar.age", "--recipient", recipient)
	if uploaded["archive_version"] != float64(1) {
		t.Fatalf("upload=%v", uploaded)
	}
	wrongKey := filepath.Join(dir, "wrong-identity.txt")
	runE2EJSON(t, bin, cfg, db, state, "archive", "keygen", wrongKey)
	failed := filepath.Join(dir, "wrong-key-restore")
	runE2EExpectError(t, bin, cfg, db, state, 5, "ERR_MANIFEST_INVALID", "archive", "restore", "/archive.tar.age", failed, "--identity", wrongKey)
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatalf("wrong key published a destination: %v", err)
	}
	runE2EExpectError(t, bin, cfg, db, state, 2, "ERR_USAGE", "archive", "restore", "/archive.tar.age", failed, "--identity", source)
	runE2EExpectError(t, bin, cfg, db, state, 2, "ERR_USAGE", "archive", "put", source, "/invalid.tar.age", "--recipient", "not-a-recipient")
	destination := filepath.Join(dir, "restored")
	runE2EJSON(t, bin, cfg, db, state, "archive", "restore", "/archive.tar.age", destination, "--identity", key)
	bytes, err := os.ReadFile(filepath.Join(destination, "private.txt"))
	if err != nil || string(bytes) != "encrypted round trip" {
		t.Fatalf("bytes=%q err=%v", bytes, err)
	}
	runE2EExpectError(t, bin, cfg, db, state, 2, "ERR_LOCAL_PATH_EXISTS", "archive", "restore", "/archive.tar.age", destination, "--identity", key)
}
