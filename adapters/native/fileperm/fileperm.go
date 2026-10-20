// Package fileperm restricts td's private files (config, session, database)
// to the current user: mode 0600/0700 on POSIX, an owner-only protected DACL
// on Windows.
package fileperm

import (
	"fmt"
	"os"
)

// Restrict limits path to the current user. Missing paths are ignored so
// callers can pass optional SQLite sidecar files (-wal, -shm).
func Restrict(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return restrict(path, info.IsDir())
}

// IsPrivate reports whether path is accessible only by the current user.
// Missing paths count as private.
func IsPrivate(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return isPrivate(path, info)
}

// OpenPrivate opens a regular private file and checks the same handle the caller reads.
func OpenPrivate(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("private file must be regular")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("private file changed while opening")
	}
	private, err := isPrivateFile(file, opened)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !private {
		_ = file.Close()
		return nil, fmt.Errorf("private file is accessible to other users")
	}
	return file, nil
}

// CreatePrivate exclusively creates an owner-private file before any secret is written.
func CreatePrivate(path string) (*os.File, error) {
	return createPrivate(path)
}
