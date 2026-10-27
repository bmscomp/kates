//go:build !windows

package cmd

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory lock on f without waiting. busy
// reports that another process holds it; err is a lock that cannot be taken
// at all (a filesystem without flock), which lockConfig treats as no lock.
func tryLockFile(f *os.File) (busy bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EINTR):
		return true, nil
	}
	return false, err
}

// unlockFile releases the lock tryLockFile took.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
