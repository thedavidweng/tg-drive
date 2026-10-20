package atomicdir

import "golang.org/x/sys/unix"

// Publish atomically publishes a verified directory without replacing any target.
func Publish(staging, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, staging, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
}
