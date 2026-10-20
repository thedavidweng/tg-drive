package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"filippo.io/age"
	"github.com/thedavidweng/tg-drive/adapters/native/atomicdir"
	"github.com/thedavidweng/tg-drive/adapters/native/cryptarchive"
	"github.com/thedavidweng/tg-drive/adapters/native/fileperm"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

type (
	ArchiveResult struct {
		Path    string `json:"path"`
		Version int    `json:"archive_version"`
		Entries int    `json:"entries"`
		Hash    string `json:"hash"`
		Size    int64  `json:"size"`
	}
	ArchiveRestoreResult struct {
		Destination string `json:"destination"`
		Version     int    `json:"archive_version"`
		Entries     int    `json:"entries"`
	}
)

func (a *App) UploadArchive(ctx context.Context, source, remote, recipientText string) (ArchiveResult, error) {
	var result ArchiveResult
	recipient, err := age.ParseX25519Recipient(recipientText)
	if err != nil {
		return result, apperr.Wrap(apperr.ErrUsage, "parse age X25519 recipient", err)
	}
	dir, err := os.MkdirTemp("", "td-encrypted-archive-")
	if err != nil {
		return result, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	filename := filepath.Join(dir, "archive.tar.age")
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return result, err
	}
	manifest, packErr := cryptarchive.Pack(ctx, source, file, recipient)
	closeErr := file.Close()
	if packErr != nil {
		if err := cancelled(ctx); err != nil {
			return result, err
		}
		return result, apperr.Wrap(apperr.ErrManifestInvalid, "create encrypted archive", packErr)
	}
	if closeErr != nil {
		return result, closeErr
	}
	upload, err := a.UploadFile(ctx, filename, remote, ConflictFail, false, UploadOptions{})
	if err != nil {
		return result, err
	}
	return ArchiveResult{Path: upload.Path, Version: manifest.Version, Entries: len(manifest.Entries), Hash: upload.Hash, Size: upload.Size}, nil
}

func (a *App) RestoreArchive(ctx context.Context, remote, destination string, identity age.Identity) (ArchiveRestoreResult, error) {
	var result ArchiveRestoreResult
	destination, err := filepath.Abs(destination)
	if err != nil {
		return result, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return result, apperr.New(apperr.ErrLocalPathExists, "archive destination already exists")
	} else if !os.IsNotExist(err) {
		return result, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".td-archive-restore-")
	if err != nil {
		return result, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := fileperm.Restrict(staging); err != nil {
		return result, err
	}
	// The downloaded ciphertext is separate from the tree that will be published.
	download, err := os.MkdirTemp("", "td-archive-download-")
	if err != nil {
		return result, err
	}
	defer func() { _ = os.RemoveAll(download) }()
	filename := filepath.Join(download, "archive.tar.age")
	if _, err := a.DownloadFile(ctx, remote, filename, ConflictFail, DownloadOptions{}); err != nil {
		return result, err
	}
	file, err := os.Open(filename)
	if err != nil {
		return result, err
	}
	manifest, unpackErr := cryptarchive.Unpack(ctx, file, staging, identity)
	closeErr := file.Close()
	if unpackErr != nil {
		if err := cancelled(ctx); err != nil {
			return result, err
		}
		return result, apperr.Wrap(apperr.ErrManifestInvalid, "verify encrypted archive", unpackErr)
	}
	if closeErr != nil {
		return result, closeErr
	}
	if err := cancelled(ctx); err != nil {
		return result, err
	}
	if err := atomicdir.Publish(staging, destination); err != nil {
		if os.IsExist(err) {
			return result, apperr.New(apperr.ErrLocalPathExists, "archive destination already exists")
		}
		return result, fmt.Errorf("publish verified archive: %w", err)
	}
	return ArchiveRestoreResult{Destination: destination, Version: manifest.Version, Entries: len(manifest.Entries)}, nil
}
