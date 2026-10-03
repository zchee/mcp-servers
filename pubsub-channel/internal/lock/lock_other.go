//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package lock

import (
	"errors"
	"io/fs"
	"os"
)

// openFlags opens the lock file for reading and writing, creating it if
// needed.
const openFlags = os.O_RDWR | os.O_CREATE

// lockFile reports that this system has no supported file lock, so an enabled
// server refuses to receive instead of receiving without the lock.
func lockFile(*os.File) error {
	return errors.ErrUnsupported
}

// unlockFile does nothing, because [lockFile] never locks.
func unlockFile(*os.File) error {
	return nil
}

// linkCount reports no count, so [Acquire] skips the hard-link check and goes
// on to [lockFile], which refuses.
func linkCount(fs.FileInfo) (uint64, bool) {
	return 0, false
}
