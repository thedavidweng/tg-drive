package telegramgotd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoginStateFilePermissions(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "cfg", "session.json")
	saveLoginState(sessionPath, loginState{Phone: "+1000", PhoneCodeHash: "abc", SentAt: time.Now().UTC()})
	info, err := os.Stat(loginStatePath(sessionPath))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

// TestLoginStateIsPerSession: the CLI and GUI sessions share a directory
// (ADR 0034). A code sent for one session's login is never offered to the
// other, and finishing one login keeps the other's pending code.
func TestLoginStateIsPerSession(t *testing.T) {
	dir := t.TempDir()
	cli, gui := filepath.Join(dir, "session.json"), filepath.Join(dir, "gui-session.json")
	now := time.Now().UTC()
	saveLoginState(gui, loginState{Phone: "+1000", PhoneCodeHash: "gui-hash", SentAt: now})
	if _, ok := loadLoginState(cli, "+1000", now); ok {
		t.Fatal("the CLI session must not reuse a code sent for the GUI session")
	}
	saveLoginState(cli, loginState{Phone: "+1000", PhoneCodeHash: "cli-hash", SentAt: now})
	clearLoginState(cli)
	if st, ok := loadLoginState(gui, "+1000", now); !ok || st.PhoneCodeHash != "gui-hash" {
		t.Fatalf("GUI pending login after the CLI's finished = %+v ok=%v, want gui-hash kept", st, ok)
	}
}

func TestLoginStateRejectsOtherPhone(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	saveLoginState(sessionPath, loginState{Phone: "+1000", PhoneCodeHash: "abc", SentAt: time.Now().UTC()})
	if _, ok := loadLoginState(sessionPath, "+2000", time.Now().UTC()); ok {
		t.Fatal("state for another phone must not be reused")
	}
}

func TestLoginStateExpires(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	sent := time.Now().UTC().Add(-loginStateTTL - time.Minute)
	saveLoginState(sessionPath, loginState{Phone: "+1000", PhoneCodeHash: "abc", SentAt: sent})
	if _, ok := loadLoginState(sessionPath, "+1000", time.Now().UTC()); ok {
		t.Fatal("expired state must not be reused")
	}
}

func TestLoginStateMissingFile(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if _, ok := loadLoginState(sessionPath, "+1000", time.Now().UTC()); ok {
		t.Fatal("missing file must not produce state")
	}
}

func TestClearLoginState(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	saveLoginState(sessionPath, loginState{Phone: "+1000", PhoneCodeHash: "abc", SentAt: time.Now().UTC()})
	clearLoginState(sessionPath)
	if _, err := os.Stat(loginStatePath(sessionPath)); !os.IsNotExist(err) {
		t.Fatalf("state file should be removed, stat err = %v", err)
	}
}

func TestLoginStateCorruptFile(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(loginStatePath(sessionPath), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadLoginState(sessionPath, "+1000", time.Now().UTC()); ok {
		t.Fatal("corrupt file must not produce state")
	}
}
