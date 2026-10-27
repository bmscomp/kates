//go:build windows

package cmd

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes an exclusive lock on the first byte of f without waiting,
// as flock does on Unix: Windows has no advisory whole-file lock, and every
// kates locks the same byte of the same lock file. busy reports that another
// process holds it.
func tryLockFile(f *os.File) (busy bool, err error) {
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return true, nil
	}
	return false, err
}

// unlockFile releases the lock tryLockFile took.
func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
