// Package cryptarchive stores a portable private tree as an authenticated age archive.
package cryptarchive

import (
	"archive/tar"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"filippo.io/age"
	"lukechampine.com/blake3"
)

const (
	manifestName     = ".td-archive-v1.json"
	maxManifestBytes = 8 << 20
)

type (
	Entry struct {
		Path      string `json:"path"`
		Size      int64  `json:"size"`
		Hash      string `json:"hash"`
		Directory bool   `json:"directory"`
	}
	Manifest struct {
		Version int     `json:"version"`
		Entries []Entry `json:"entries"`
	}
	reader struct {
		ctx context.Context
		r   io.Reader
	}
)

func (r reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func validName(name string) bool {
	if name == "." || name == manifestName || !fs.ValidPath(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.TrimRight(part, " .") != part || strings.ContainsAny(part, `<>:"\|?*`) {
			return false
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return false
			}
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" {
			return false
		}
		if (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && len(stem) == 4 && stem[3] >= '1' && stem[3] <= '9' {
			return false
		}
	}
	return true
}

func digest(r io.Reader) (string, int64, error) {
	h := blake3.New(32, nil)
	n, err := io.Copy(h, r)
	return "blake3:" + hex.EncodeToString(h.Sum(nil)), n, err
}

// Pack keeps names, the versioned manifest and content hashes inside ciphertext.
// The caller owns an unpublished output and discards it on every error.
func Pack(ctx context.Context, source string, dst io.Writer, recipient age.Recipient) (Manifest, error) {
	manifest := Manifest{Version: 1, Entries: []Entry{}}
	info, err := os.Lstat(source)
	if err != nil {
		return manifest, err
	}
	if !info.IsDir() {
		return manifest, fmt.Errorf("archive source must be a directory")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return manifest, err
	}
	defer func() { _ = root.Close() }()
	opened, err := root.Stat(".")
	if err != nil {
		return manifest, err
	}
	if !os.SameFile(info, opened) {
		return manifest, fmt.Errorf("archive source changed while opening")
	}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if !validName(name) {
			return fmt.Errorf("nonportable or reserved archive path %q", name)
		}
		item := Entry{Path: name, Directory: entry.IsDir()}
		if !item.Directory {
			if !entry.Type().IsRegular() {
				return fmt.Errorf("archive rejects symlink or special file %q", name)
			}
			file, err := root.Open(name)
			if err != nil {
				return err
			}
			defer func() { _ = file.Close() }()
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("archive source changed type: %s", name)
			}
			item.Hash, item.Size, err = digest(reader{ctx, file})
			if err != nil {
				return err
			}
		}
		manifest.Entries = append(manifest.Entries, item)
		return nil
	})
	if err != nil {
		return manifest, err
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	metadata, err := json.Marshal(manifest)
	if err != nil {
		return manifest, err
	}
	if len(metadata) > maxManifestBytes {
		return manifest, fmt.Errorf("archive manifest exceeds %d bytes", maxManifestBytes)
	}
	encrypted, err := age.Encrypt(dst, recipient)
	if err != nil {
		return manifest, err
	}
	tarOut := tar.NewWriter(encrypted)
	if err := tarOut.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(metadata))}); err != nil {
		return manifest, err
	}
	if _, err := tarOut.Write(metadata); err != nil {
		return manifest, err
	}
	for _, item := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return manifest, err
		}
		header := &tar.Header{Name: item.Path, Mode: 0o600, Size: item.Size, Typeflag: tar.TypeReg}
		if item.Directory {
			header.Typeflag = tar.TypeDir
			header.Mode = 0o700
		}
		if err := tarOut.WriteHeader(header); err != nil {
			return manifest, err
		}
		if item.Directory {
			continue
		}
		file, err := root.Open(item.Path)
		if err != nil {
			return manifest, err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return manifest, statErr
		}
		if !info.Mode().IsRegular() {
			_ = file.Close()
			return manifest, fmt.Errorf("archive source changed type: %s", item.Path)
		}
		hash := blake3.New(32, nil)
		n, copyErr := io.Copy(io.MultiWriter(tarOut, hash), reader{ctx, file})
		closeErr := file.Close()
		if copyErr != nil {
			return manifest, copyErr
		}
		if closeErr != nil {
			return manifest, closeErr
		}
		if n != item.Size || "blake3:"+hex.EncodeToString(hash.Sum(nil)) != item.Hash {
			return manifest, fmt.Errorf("archive source changed: %s", item.Path)
		}
	}
	if err := tarOut.Close(); err != nil {
		return manifest, err
	}
	if err := encrypted.Close(); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// Unpack writes only to a caller-owned private empty staging directory.
// It consumes the authenticated stream fully before reporting success.
func Unpack(ctx context.Context, src io.Reader, staging string, identity age.Identity) (Manifest, error) {
	var manifest Manifest
	encrypted, err := age.Decrypt(reader{ctx, src}, identity)
	if err != nil {
		return manifest, err
	}
	archive := tar.NewReader(encrypted)
	header, err := archive.Next()
	if err != nil {
		return manifest, err
	}
	if header.Name != manifestName || header.Typeflag != tar.TypeReg || header.Size > maxManifestBytes {
		return manifest, fmt.Errorf("invalid archive manifest")
	}
	decoder := json.NewDecoder(archive)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return manifest, fmt.Errorf("trailing manifest data")
	}
	if manifest.Version != 1 {
		return manifest, fmt.Errorf("unsupported archive version %d", manifest.Version)
	}
	seen := map[string]bool{}
	previous := ""
	for _, item := range manifest.Entries {
		if item.Path <= previous {
			return manifest, fmt.Errorf("archive paths are not strictly ordered")
		}
		previous = item.Path
		if !validName(item.Path) || seen[item.Path] || item.Size < 0 {
			return manifest, fmt.Errorf("invalid or duplicate archive path %q", item.Path)
		}
		if item.Directory {
			if item.Size != 0 || item.Hash != "" {
				return manifest, fmt.Errorf("invalid directory metadata")
			}
		} else {
			if len(item.Hash) != 71 || !strings.HasPrefix(item.Hash, "blake3:") {
				return manifest, fmt.Errorf("invalid content hash")
			}
			if _, err := hex.DecodeString(strings.TrimPrefix(item.Hash, "blake3:")); err != nil {
				return manifest, fmt.Errorf("invalid content hash: %w", err)
			}
		}
		seen[item.Path] = true
	}
	root, err := os.OpenRoot(staging)
	if err != nil {
		return manifest, err
	}
	defer func() { _ = root.Close() }()
	for _, item := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return manifest, err
		}
		header, err := archive.Next()
		if err != nil {
			return manifest, err
		}
		want := byte(tar.TypeReg)
		if item.Directory {
			want = tar.TypeDir
		}
		if header.Name != item.Path || header.Typeflag != want || header.Size != item.Size || header.Linkname != "" {
			return manifest, fmt.Errorf("archive entry differs from manifest: %s", item.Path)
		}
		if item.Directory {
			if err := root.MkdirAll(item.Path, 0o700); err != nil {
				return manifest, err
			}
			continue
		}
		if parent := filepath.Dir(item.Path); parent != "." {
			if err := root.MkdirAll(parent, 0o700); err != nil {
				return manifest, err
			}
		}
		file, err := root.OpenFile(item.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return manifest, err
		}
		h := blake3.New(32, nil)
		n, copyErr := io.Copy(io.MultiWriter(file, h), reader{ctx, archive})
		closeErr := file.Close()
		if copyErr != nil {
			return manifest, copyErr
		}
		if closeErr != nil {
			return manifest, closeErr
		}
		if n != item.Size || "blake3:"+hex.EncodeToString(h.Sum(nil)) != item.Hash {
			return manifest, fmt.Errorf("archive content verification failed: %s", item.Path)
		}
	}
	if _, err := archive.Next(); !errors.Is(err, io.EOF) {
		return manifest, fmt.Errorf("unexpected archive entry or invalid trailer: %v", err)
	}
	// tar EOF alone is insufficient: age authenticates the final ciphertext chunk
	// only when its decrypted reader reaches EOF.
	n, err := io.Copy(io.Discard, reader{ctx, encrypted})
	if err != nil {
		return manifest, err
	}
	if n != 0 {
		return manifest, fmt.Errorf("trailing encrypted archive payload")
	}
	return manifest, nil
}
