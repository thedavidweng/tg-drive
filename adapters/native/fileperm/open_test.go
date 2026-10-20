package fileperm

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity")
	if err := os.WriteFile(path, []byte("owned key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restrict(path); err != nil {
		t.Fatal(err)
	}
	file, err := OpenPrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "owned key" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if file, err := OpenPrivate(dir); err == nil {
		_ = file.Close()
		t.Fatal("directory accepted")
	}
	if file, err := OpenPrivate(filepath.Join(dir, "missing")); err == nil {
		_ = file.Close()
		t.Fatal("missing identity accepted")
	}
}

func TestCreatePrivateExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	file, err := CreatePrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("secret"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	private, err := IsPrivate(path)
	if err != nil || !private {
		t.Fatalf("private=%v err=%v", private, err)
	}
	if duplicate, err := CreatePrivate(path); err == nil {
		_ = duplicate.Close()
		t.Fatal("existing key overwritten")
	} else if !os.IsExist(err) {
		t.Fatalf("expected exists error, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "secret" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}
