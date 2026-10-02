// Package fileperm restricts td's private files (config, session, database)
// to the current user: mode 0600/0700 on POSIX, an owner-only protected DACL
// on Windows.
package fileperm

import "os"

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
