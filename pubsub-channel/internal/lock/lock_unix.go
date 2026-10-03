//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package lock

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// openFlags opens the lock file for reading and writing, creating it if
// needed. O_NOFOLLOW makes the open fail on a symbolic link, so the lock never
// opens, and the holder text never overwrites, the file a link points to.
const openFlags = os.O_RDWR | os.O_CREATE | syscall.O_NOFOLLOW

// lockFile takes an exclusive flock on f without waiting, returning [ErrHeld]
// when another open file holds it.
func lockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			return flockError(err)
		}
	}
}

// flockError maps the result of a non-blocking flock to the result of
// [lockFile]: "would block" means another open file holds the lock, and every
// other errno is a failure to lock, never a success.
func flockError(err error) error {
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrHeld
	}
	return err
}

// unlockFile releases the flock on f.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// linkCount returns the number of hard links of the file info describes.
func linkCount(info fs.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Nlink), true
}
