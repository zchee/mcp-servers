// Package lock lets one pubsub-channel process per machine and user receive
// from a given subscription.
//
// Claude Code starts an enabled plugin's MCP server in every one of its
// processes, and the server cannot tell whether its own session registered the
// channel. A process that receives in a session without the channel acks
// messages that Claude Code then drops, so receiving is guarded by an exclusive
// advisory lock on a file named after the subscription. The kernel releases the
// lock when the holding process exits, however it exits.
//
// The lock belongs to the open file, not to the path. If the file is unlinked
// while it is held, a later process creates a new file at the same path and
// locks that one, so a holder must keep checking that the path still names the
// file it locked; see [Lock.Watch].
package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// dirName is the directory under the per-user base directory that holds the
// lock files.
const dirName = "pubsub-channel"

// acquireAttempts bounds how often [Acquire] retries when the path stops naming
// the file it locked between opening and checking it.
const acquireAttempts = 3

// CheckInterval is how often a receiving holder should call [Lock.Check],
// through [Lock.Watch]. It bounds how long two processes can receive at once
// after the lock file is removed or replaced; a check costs two stat calls.
const CheckInterval = time.Second

// ErrHeld is returned by [Acquire] when another lock already holds the
// subscription.
var ErrHeld = errors.New("lock: subscription is held by another pubsub-channel process")

// errLost reports that the lock path no longer names the locked file.
var errLost = errors.New("lock: the lock file was removed or replaced")

// lstat reads what the lock path names in [Lock.Check]. Tests replace it to
// make the path name another file between locking and checking.
var lstat = os.Lstat

// Lock is a held lock on one subscription. Every write goes through the locked
// file descriptor, never through the path, so a file that replaced the locked
// one at the path, or a symbolic link, is never written.
type Lock struct {
	path string
	file *os.File
}

// DefaultDir returns the directory for lock files: pubsub-channel under a
// per-user base directory meant for persistent application data. That is
// $HOME/Library/Application Support on macOS, and $XDG_STATE_HOME or
// $HOME/.local/state elsewhere.
func DefaultDir() (string, error) {
	return defaultDir(runtime.GOOS, os.Getenv)
}

// defaultDir returns the lock directory for goos, reading the environment
// through getenv.
//
// The directory must survive as long as a holder runs, because a holder whose
// file is deleted no longer excludes anyone. A user cache directory does not
// qualify: on every platform its documented purpose is data that can be
// deleted and re-created, so cache cleaners and the operating system may
// empty it. Therefore:
//   - darwin and ios: $HOME/Library/Application Support, the per-user place
//     for persistent application data (the directory os.UserConfigDir
//     returns).
//   - every other system: $XDG_STATE_HOME, or $HOME/.local/state when it is
//     unset, empty or relative. The XDG Base Directory Specification defines
//     it for state that should persist between restarts and says to ignore a
//     relative value. $XDG_RUNTIME_DIR, the specification's place for runtime
//     files, is not used: its files may be cleaned up periodically, and it is
//     unset outside a login session.
func defaultDir(goos string, getenv func(string) string) (string, error) {
	var base string
	switch goos {
	case "darwin", "ios":
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("lock: $HOME is not set")
		}
		base = filepath.Join(home, "Library", "Application Support")
	default:
		base = getenv("XDG_STATE_HOME")
		if !filepath.IsAbs(base) {
			home := getenv("HOME")
			if home == "" {
				return "", errors.New("lock: neither an absolute $XDG_STATE_HOME nor $HOME is set")
			}
			base = filepath.Join(home, ".local", "state")
		}
	}
	return filepath.Join(base, dirName), nil
}

// Path returns the lock file in dir for subscription, the full resource name
// projects/<project>/subscriptions/<id>. The file is named by the hex SHA-256
// of the name, because a resource name contains slashes and characters that
// are not portable in file names.
func Path(dir, subscription string) string {
	sum := sha256.Sum256([]byte(subscription))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".lock")
}

// Acquire takes the lock for subscription in dir without waiting, creating dir
// with mode 0700 if needed. It returns [ErrHeld] when another lock holds the
// subscription, whether in this process or another one, and an error when the
// lock file is a symbolic link or has more than one hard link.
//
// After locking it checks that the path still names the locked file: a
// process that opened the file just before another process unlinked it would
// otherwise hold a lock on a file nobody else can find. It retries a few times
// when the check fails.
func Acquire(dir, subscription string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("lock: create directory: %w", err)
	}
	path := Path(dir, subscription)
	var lastErr error
	for range acquireAttempts {
		// The file is opened without truncation, so a process that loses the
		// race leaves the holder's diagnostics intact.
		f, err := os.OpenFile(path, openFlags, 0o600)
		if err != nil {
			if symErr := symlinkError(path); symErr != nil {
				return nil, symErr
			}
			return nil, fmt.Errorf("lock: %w", err)
		}
		// A second name for the file, made before this open, may be any file
		// of the user's; the holder text would overwrite it and Release would
		// empty it. The count is checked before the lock attempt, so that a
		// linked file another process has locked is refused instead of being
		// locked, reported as held and read for its holder text. A count of 0
		// means the file was unlinked after the open, which the identity check
		// below retries.
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("lock: stat %s: %w", path, err)
		}
		if n, ok := linkCount(info); ok && n > 1 {
			_ = f.Close()
			return nil, fmt.Errorf("lock: %s has %d hard links; refusing to use it as the lock file", path, n)
		}
		if err := lockFile(f); err != nil {
			_ = f.Close()
			if errors.Is(err, ErrHeld) {
				return nil, ErrHeld
			}
			return nil, fmt.Errorf("lock: lock %s: %w", path, err)
		}
		l := &Lock{path: path, file: f}
		lastErr = l.Check()
		if lastErr == nil {
			return l, nil
		}
		// Closing the file releases its lock.
		_ = f.Close()
		if symErr := symlinkError(path); symErr != nil {
			return nil, symErr
		}
	}
	return nil, fmt.Errorf("lock: %s kept changing while it was being locked: %w", path, lastErr)
}

// symlinkError returns an error when path is a symbolic link, and nil
// otherwise.
func symlinkError(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		return nil
	}
	return fmt.Errorf("lock: %s is a symbolic link; refusing to use it as the lock file", path)
}

// Check returns an error when the lock path no longer names the locked file:
// the file was removed, renamed, or replaced by another file or a link. Another
// process can then lock a new file at the path, so a holder that gets an error
// must stop receiving.
func (l *Lock) Check() error {
	locked, err := l.file.Stat()
	if err != nil {
		return fmt.Errorf("lock: stat the locked file: %w", err)
	}
	named, err := lstat(l.path)
	if err != nil {
		return fmt.Errorf("%w: %w", errLost, err)
	}
	if !os.SameFile(locked, named) {
		return fmt.Errorf("%w: %s names another file", errLost, l.path)
	}
	return nil
}

// Watch calls [Lock.Check] every interval until ctx is done. It returns the
// first error Check returns, or nil once ctx is done.
func (l *Lock) Watch(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := l.Check(); err != nil {
				return err
			}
		}
	}
}

// WriteHolder records this process's PID, started as its start time, and
// subscription in the lock file for diagnostics; see [Holder]. The content is
// informational only, so a caller can log a failure and keep the lock.
func (l *Lock) WriteHolder(started time.Time, subscription string) error {
	holder := "pid=" + strconv.Itoa(os.Getpid()) + " started=" + started.UTC().Format(time.RFC3339) + " subscription=" + subscription + "\n"
	n, err := l.file.WriteAt([]byte(holder), 0)
	if err == nil {
		// Cuts off the rest of a longer line a killed holder left behind.
		err = l.file.Truncate(int64(n))
	}
	if err != nil {
		return fmt.Errorf("lock: write holder: %w", err)
	}
	return nil
}

// Holder returns the diagnostics the current or last holder of subscription's
// lock in dir wrote, without the trailing newline. It is empty when the file
// does not exist or the holder has released the lock. The lock, not this
// content, decides who holds the lock.
func Holder(dir, subscription string) string {
	b, err := os.ReadFile(Path(dir, subscription))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Path returns the lock file's path.
func (l *Lock) Path() string {
	return l.path
}

// Release clears the holder diagnostics and releases the lock. The file is left
// in place: removing it would let a process that opened the old file and one
// that creates a new file both hold a lock on the same subscription.
func (l *Lock) Release() error {
	// Cleared through the descriptor while the lock is still held, so it never
	// erases the content of the next holder, even when the locked file is no
	// longer the one at the path.
	truncErr := l.file.Truncate(0)
	if truncErr != nil {
		truncErr = fmt.Errorf("lock: clear holder: %w", truncErr)
	}
	unlockErr := unlockFile(l.file)
	if unlockErr != nil {
		unlockErr = fmt.Errorf("lock: unlock %s: %w", l.path, unlockErr)
	}
	closeErr := l.file.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("lock: close %s: %w", l.path, closeErr)
	}
	return errors.Join(truncErr, unlockErr, closeErr)
}
