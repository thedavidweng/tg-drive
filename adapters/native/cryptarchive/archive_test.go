package cryptarchive

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func TestArchiveRoundTripAndAuthentication(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "private-name.txt"), []byte("private content"), 0o600); err != nil {
		t.Fatal(err)
	}
	var encrypted bytes.Buffer
	manifest, err := Pack(context.Background(), source, &encrypted, identity.Recipient())
	if err != nil || len(manifest.Entries) != 2 {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	ciphertext := encrypted.Bytes()
	if bytes.Contains(ciphertext, []byte("private-name")) || bytes.Contains(ciphertext, []byte("private content")) {
		t.Fatal("plaintext exposed")
	}
	restored := t.TempDir()
	if _, err := Unpack(context.Background(), bytes.NewReader(ciphertext), restored, identity); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(restored, "private-name.txt"))
	if err != nil || string(data) != "private content" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if info, err := os.Stat(filepath.Join(restored, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory=%v %v", info, err)
	}
	wrong, _ := age.GenerateX25519Identity()
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 1
	for _, test := range []struct {
		name string
		data []byte
		key  age.Identity
	}{{"wrong key", ciphertext, wrong}, {"tampered final chunk", tampered, identity}, {"truncated final chunk", ciphertext[:len(ciphertext)-1], identity}, {"truncated header", ciphertext[:30], identity}} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Unpack(context.Background(), bytes.NewReader(test.data), t.TempDir(), test.key); err == nil {
				t.Fatal("invalid ciphertext accepted")
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Pack(cancelled, source, io.Discard, identity.Recipient()); err == nil {
		t.Fatal("cancelled pack accepted")
	}
	if _, err := Unpack(cancelled, bytes.NewReader(ciphertext), t.TempDir(), identity); err == nil {
		t.Fatal("cancelled restore accepted")
	}
}

func TestArchiveRejectsInvalidAuthenticatedPayload(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	hash, _, _ := digest(bytes.NewReader([]byte("a")))
	for _, test := range []struct {
		name    string
		version int
		entries []Entry
		kind    byte
		body    string
		trailer bool
	}{
		{name: "future version", version: 2},
		{name: "traversal", version: 1, entries: []Entry{{Path: "../escape", Hash: hash, Size: 1}}, body: "a"},
		{name: "windows path", version: 1, entries: []Entry{{Path: "C:escape", Hash: hash, Size: 1}}, body: "a"},
		{name: "windows device", version: 1, entries: []Entry{{Path: "CON.txt", Hash: hash, Size: 1}}, body: "a"},
		{name: "unordered manifest", version: 1, entries: []Entry{{Path: "z", Hash: hash, Size: 1}, {Path: "a", Hash: hash, Size: 1}}, body: "a"},
		{name: "negative size", version: 1, entries: []Entry{{Path: "a", Hash: hash, Size: -1}}, body: "a"},
		{name: "duplicate", version: 1, entries: []Entry{{Path: "a", Hash: hash, Size: 1}, {Path: "a", Hash: hash, Size: 1}}, body: "a"},
		{name: "symlink", version: 1, entries: []Entry{{Path: "a", Hash: hash, Size: 1}}, kind: tar.TypeSymlink},
		{name: "hash mismatch", version: 1, entries: []Entry{{Path: "a", Hash: hash, Size: 1}}, body: "b"},
		{name: "extra payload", version: 1, trailer: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ciphertext bytes.Buffer
			enc, err := age.Encrypt(&ciphertext, identity.Recipient())
			if err != nil {
				t.Fatal(err)
			}
			archive := tar.NewWriter(enc)
			manifest, _ := json.Marshal(Manifest{Version: test.version, Entries: test.entries})
			if err := archive.WriteHeader(&tar.Header{Name: manifestName, Size: int64(len(manifest)), Mode: 0o600}); err != nil {
				t.Fatal(err)
			}
			if _, err := archive.Write(manifest); err != nil {
				t.Fatal(err)
			}
			for _, entry := range test.entries {
				kind := test.kind
				if kind == 0 {
					kind = tar.TypeReg
				}
				size := int64(len(test.body))
				if kind == tar.TypeSymlink {
					size = 0
				}
				header := &tar.Header{Name: entry.Path, Typeflag: kind, Size: size, Mode: 0o600}
				if kind == tar.TypeSymlink {
					header.Linkname = "../escape"
				}
				if err := archive.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if size > 0 {
					if _, err := archive.Write([]byte(test.body)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if test.trailer {
				if _, err := enc.Write([]byte("unexpected plaintext")); err != nil {
					t.Fatal(err)
				}
			}
			if err := enc.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Unpack(context.Background(), bytes.NewReader(ciphertext.Bytes()), t.TempDir(), identity); err == nil {
				t.Fatal("invalid authenticated payload accepted")
			}
		})
	}
}

func TestArchiveRefusesNonportableNames(t *testing.T) {
	for _, name := range []string{"CON", "nul.txt", "COM1.txt", "LPT9", "file.", "file ", "a:b", "a\\b", "a\ntext"} {
		t.Run(name, func(t *testing.T) {
			if validName(name) {
				t.Fatalf("nonportable name accepted: %q", name)
			}
		})
	}
}
