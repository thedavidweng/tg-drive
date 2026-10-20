package atomicdir

import "golang.org/x/sys/windows"

// Publish atomically publishes a verified directory without replacing any target.
func Publish(staging, destination string) error {
	source, err := windows.UTF16PtrFromString(staging)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFile(source, target)
}
