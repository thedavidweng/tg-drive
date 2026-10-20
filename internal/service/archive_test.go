package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestEncryptedArchiveReconstructsAndPublishesOnlyAfterValidation(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	watchWrite(t, source, "nested/private.txt", "private bytes")
	uploaded, err := a.UploadArchive(ctx, source, "/backups/one.tar.age", identity.Recipient().String())
	if err != nil || uploaded.Version != 1 || uploaded.Entries != 2 {
		t.Fatalf("upload=%+v err=%v", uploaded, err)
	}
	ch, err := a.channel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Raw().Exec(`delete from files where channel_id=?`, ch.rowID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	result, err := a.RestoreArchive(ctx, uploaded.Path, dest, identity)
	if err != nil || result.Version != 1 {
		t.Fatalf("restore=%+v err=%v", result, err)
	}
	bytes, err := os.ReadFile(filepath.Join(dest, "nested/private.txt"))
	if err != nil || string(bytes) != "private bytes" {
		t.Fatalf("bytes=%q err=%v", bytes, err)
	}
	if _, err := a.RestoreArchive(ctx, uploaded.Path, dest, identity); err == nil {
		t.Fatal("existing destination accepted")
	}
	wrong, _ := age.GenerateX25519Identity()
	failed := filepath.Join(t.TempDir(), "never-publish")
	if _, err := a.RestoreArchive(ctx, uploaded.Path, failed, wrong); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatalf("failed restore exposed destination: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(failed))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed staging retained: %v %v", entries, err)
	}
}

func TestEncryptedArchiveLateAuthenticationFailureRemovesStaging(t *testing.T) {
	a, tg := testApp(t)
	loginAndInit(t, a, tg)
	ctx := context.Background()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	watchWrite(t, source, "large.txt", strings.Repeat("private", 20000))
	uploaded, err := a.UploadArchive(ctx, source, "/valid.tar.age", identity.Recipient().String())
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := filepath.Join(t.TempDir(), "tampered.tar.age")
	if _, err := a.DownloadFile(ctx, uploaded.Path, ciphertext, ConflictFail, DownloadOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(ciphertext, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UploadFile(ctx, ciphertext, "/tampered.tar.age", ConflictFail, false, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "never-publish")
	if _, err := a.RestoreArchive(ctx, "/tampered.tar.age", destination, identity); err == nil {
		t.Fatal("late authentication failure accepted")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed restore retained plaintext: %v %v", entries, err)
	}
}
