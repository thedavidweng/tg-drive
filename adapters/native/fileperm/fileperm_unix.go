//go:build !windows

package fileperm

import "os"

func restrict(path string, isDir bool) error {
	if isDir {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}

func isPrivate(_ string, info os.FileInfo) (bool, error) {
	return info.Mode().Perm()&0o077 == 0, nil
}

func isPrivateFile(_ *os.File, info os.FileInfo) (bool, error) { return isPrivate("", info) }

func createPrivate(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}
