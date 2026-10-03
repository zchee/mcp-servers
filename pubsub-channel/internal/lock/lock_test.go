package lock

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

const (
	subA = "projects/my-proj/subscriptions/alerts"
	subB = "projects/my-proj/subscriptions/builds"
)

func TestPath(t *testing.T) {
	tests := map[string]struct {
		dir          string
		subscription string
		want         string
	}{
		"success: file is the hex SHA-256 of the full subscription name": {
			dir:          "/cache/pubsub-channel",
			subscription: subA,
			// printf %s projects/my-proj/subscriptions/alerts | shasum -a 256
			want: "/cache/pubsub-channel/50a349222f27125b389aca2f9151a7e27c1b9f89a51f43e4d8c249416d2129af.lock",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := gocmp.Diff(filepath.FromSlash(tt.want), Path(filepath.FromSlash(tt.dir), tt.subscription)); diff != "" {
				t.Errorf("Path(%q, %q) (-want +got):\n%s", tt.dir, tt.subscription, diff)
			}
		})
	}
}

func TestAcquire(t *testing.T) {
	tests := map[string]struct {
		// held lists the subscriptions locked before the attempt, each through
		// its own Acquire, as another process would.
		held []string
		// releaseHeld releases the held locks before the attempt.
		releaseHeld  bool
		subscription string
		wantErr      error
	}{
		"success: first acquirer of a free subscription": {
			subscription: subA,
		},
		"error: second acquirer while the first holds the lock": {
			held:         []string{subA},
			subscription: subA,
			wantErr:      ErrHeld,
		},
		"success: acquirer after the holder released the lock": {
			held:         []string{subA},
			releaseHeld:  true,
			subscription: subA,
		},
		"success: different subscriptions do not conflict": {
			held:         []string{subB},
			subscription: subA,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, sub := range tt.held {
				l, err := Acquire(dir, sub)
				if err != nil {
					t.Fatalf("Acquire(%q) for the earlier holder: %v", sub, err)
				}
				if tt.releaseHeld {
					if err := l.Release(); err != nil {
						t.Fatalf("Release of %q: %v", sub, err)
					}
					continue
				}
				t.Cleanup(func() {
					if err := l.Release(); err != nil {
						t.Errorf("Release of %q: %v", sub, err)
					}
				})
			}

			l, err := Acquire(dir, tt.subscription)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Acquire(%q) error = %v, want %v", tt.subscription, err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if l != nil {
					t.Errorf("Acquire(%q) returned a lock with error %v", tt.subscription, err)
				}
				return
			}
			if diff := gocmp.Diff(Path(dir, tt.subscription), l.Path()); diff != "" {
				t.Errorf("lock path (-want +got):\n%s", diff)
			}
			if err := l.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		})
	}
}

func TestAcquireCreatesDirectory(t *testing.T) {
	tests := map[string]struct {
		// dir returns the lock directory, relative to a fresh temporary
		// directory root.
		dir     func(t *testing.T, root string) string
		wantErr string
		// wantPerm is the permission bits of the lock directory afterwards.
		wantPerm os.FileMode
	}{
		"success: missing parent directories are created": {
			dir:      func(_ *testing.T, root string) string { return filepath.Join(root, "a", "b", "pubsub-channel") },
			wantPerm: 0o700,
		},
		"success: an existing directory with wider permissions is used as it is": {
			dir: func(t *testing.T, root string) string {
				t.Helper()
				dir := filepath.Join(root, "pubsub-channel")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				// Mkdir applies the umask; set the mode the case is about.
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			wantPerm: 0o755,
		},
		"error: a regular file in the path": {
			dir: func(t *testing.T, root string) string {
				t.Helper()
				file := filepath.Join(root, "file")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(file, "pubsub-channel")
			},
			wantErr: "lock: create directory: ",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := tt.dir(t, t.TempDir())

			l, err := Acquire(dir, subA)

			if tt.wantErr != "" {
				if err == nil || errors.Is(err, ErrHeld) || !strings.HasPrefix(err.Error(), tt.wantErr) {
					t.Fatalf("Acquire in %s error = %v, want one starting with %q", dir, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Acquire in %s: %v", dir, err)
			}
			t.Cleanup(func() { _ = l.Release() })
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				t.Fatalf("lock directory %s: info %v, error %v", dir, info, err)
			}
			if got := info.Mode().Perm(); got != tt.wantPerm {
				t.Errorf("lock directory %s mode = %v, want %v", dir, got, tt.wantPerm)
			}
		})
	}
}

func TestHolder(t *testing.T) {
	started := time.Date(2026, 10, 3, 6, 30, 15, 0, time.FixedZone("JST", 9*60*60))
	wantHolder := "pid=" + strconv.Itoa(os.Getpid()) + " started=2026-10-02T21:30:15Z subscription=" + subA

	tests := map[string]struct {
		// loserAttempts makes a second Acquire fail against the holder before
		// the content is read.
		loserAttempts bool
		release       bool
		want          string
	}{
		"success: holder content names the PID, start time and subscription": {
			want: wantHolder,
		},
		"success: a losing acquirer leaves the holder content intact": {
			loserAttempts: true,
			want:          wantHolder,
		},
		"success: release clears the holder content": {
			release: true,
			want:    "",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			l, err := Acquire(dir, subA)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if err := l.WriteHolder(started, subA); err != nil {
				t.Fatalf("WriteHolder: %v", err)
			}
			if tt.loserAttempts {
				if _, err := Acquire(dir, subA); !errors.Is(err, ErrHeld) {
					t.Fatalf("second Acquire error = %v, want %v", err, ErrHeld)
				}
			}
			if tt.release {
				if err := l.Release(); err != nil {
					t.Fatalf("Release: %v", err)
				}
			} else {
				t.Cleanup(func() { _ = l.Release() })
			}

			if diff := gocmp.Diff(tt.want, Holder(dir, subA)); diff != "" {
				t.Errorf("Holder (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHolderWithoutLockFile(t *testing.T) {
	tests := map[string]struct {
		subscription string
	}{
		"success: no lock file reads as empty": {
			subscription: subA,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := Holder(t.TempDir(), tt.subscription); got != "" {
				t.Errorf("Holder(%q) = %q, want empty", tt.subscription, got)
			}
		})
	}
}

// helperDirEnv makes the test binary act as a separate process that holds a
// lock; see TestAcquireAcrossProcesses.
const helperDirEnv = "PUBSUB_CHANNEL_LOCK_TEST_HELPER_DIR"

func TestMain(m *testing.M) {
	if dir := os.Getenv(helperDirEnv); dir != "" {
		holdLock(dir)
		return
	}
	os.Exit(m.Run())
}

// holdLock takes the lock on subA in dir, reports it on stdout, and holds it
// until stdin closes or the process is killed.
func holdLock(dir string) {
	if _, err := Acquire(dir, subA); err != nil {
		_, _ = os.Stdout.WriteString("error: " + err.Error() + "\n")
		os.Exit(1)
	}
	_, _ = os.Stdout.WriteString("locked\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// TestAcquireAcrossProcesses checks the lock against another process, and that
// the kernel releases it when that process is killed without a chance to clean
// up.
func TestAcquireAcrossProcesses(t *testing.T) {
	tests := map[string]struct {
		// end stops the holder process.
		end func(t *testing.T, cmd *exec.Cmd, stdin io.Closer)
	}{
		"success: SIGKILL of the holder releases the lock": {
			end: func(t *testing.T, cmd *exec.Cmd, _ io.Closer) {
				t.Helper()
				if err := cmd.Process.Kill(); err != nil {
					t.Fatalf("kill holder: %v", err)
				}
			},
		},
		"success: normal exit of the holder releases the lock": {
			end: func(t *testing.T, _ *exec.Cmd, stdin io.Closer) {
				t.Helper()
				_ = stdin.Close()
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), helperDirEnv+"="+dir)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start holder process: %v", err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
			line, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil || line != "locked\n" {
				t.Fatalf("holder process reported %q, %v; want %q", line, err, "locked\n")
			}

			if _, err := Acquire(dir, subA); !errors.Is(err, ErrHeld) {
				t.Fatalf("Acquire while another process holds the lock: error = %v, want %v", err, ErrHeld)
			}

			tt.end(t, cmd, stdin)
			_ = cmd.Wait()

			l, err := Acquire(dir, subA)
			if err != nil {
				t.Fatalf("Acquire after the holder process ended: %v", err)
			}
			if err := l.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		})
	}
}

func TestDefaultDir(t *testing.T) {
	tests := map[string]struct {
		goos    string
		env     map[string]string
		want    string
		wantErr string
	}{
		"success: macOS uses Application Support under HOME": {
			goos: "darwin",
			env:  map[string]string{"HOME": "/Users/u", "XDG_STATE_HOME": "/ignored"},
			want: filepath.Join("/Users/u", "Library", "Application Support", "pubsub-channel"),
		},
		"error: macOS without HOME": {
			goos:    "darwin",
			wantErr: "lock: $HOME is not set",
		},
		"success: Linux uses an absolute XDG_STATE_HOME": {
			goos: "linux",
			env:  map[string]string{"HOME": "/home/u", "XDG_STATE_HOME": "/state"},
			want: filepath.Join("/state", "pubsub-channel"),
		},
		"success: Linux without XDG_STATE_HOME uses HOME/.local/state": {
			goos: "linux",
			env:  map[string]string{"HOME": "/home/u", "XDG_CACHE_HOME": "/cache"},
			want: filepath.Join("/home/u", ".local", "state", "pubsub-channel"),
		},
		"success: Linux ignores a relative XDG_STATE_HOME": {
			goos: "linux",
			env:  map[string]string{"HOME": "/home/u", "XDG_STATE_HOME": "state"},
			want: filepath.Join("/home/u", ".local", "state", "pubsub-channel"),
		},
		"success: other systems follow the XDG rule": {
			goos: "freebsd",
			env:  map[string]string{"HOME": "/home/u"},
			want: filepath.Join("/home/u", ".local", "state", "pubsub-channel"),
		},
		"error: Linux with neither XDG_STATE_HOME nor HOME": {
			goos:    "linux",
			env:     map[string]string{"XDG_STATE_HOME": "relative"},
			wantErr: "lock: neither an absolute $XDG_STATE_HOME nor $HOME is set",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := defaultDir(tt.goos, func(k string) string { return tt.env[k] })
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("defaultDir(%q) = %q, %v; want error %q", tt.goos, got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("defaultDir(%q): %v", tt.goos, err)
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("defaultDir(%q) (-want +got):\n%s", tt.goos, diff)
			}
		})
	}
}

func TestAcquireSymlink(t *testing.T) {
	const targetContent = "precious content\n"

	tests := map[string]struct {
		// targetExists creates the link target with targetContent.
		targetExists bool
	}{
		"error: a link to an existing file leaves the file unchanged": {
			targetExists: true,
		},
		"error: a dangling link does not create its target": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(t.TempDir(), "target")
			if tt.targetExists {
				if err := os.WriteFile(target, []byte(targetContent), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := Path(dir, subA)
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}

			l, err := Acquire(dir, subA)

			wantErr := "lock: " + path + " is a symbolic link; refusing to use it as the lock file"
			if err == nil || err.Error() != wantErr {
				if l != nil {
					_ = l.Release()
				}
				t.Fatalf("Acquire through a symbolic link: error = %v, want %q", err, wantErr)
			}
			got, err := os.ReadFile(target)
			switch {
			case tt.targetExists && err != nil:
				t.Fatalf("read link target: %v", err)
			case tt.targetExists:
				if diff := gocmp.Diff(targetContent, string(got)); diff != "" {
					t.Errorf("link target content (-want +got):\n%s", diff)
				}
			case !errors.Is(err, os.ErrNotExist):
				t.Errorf("dangling link target: read %q, error %v; want it not to exist", got, err)
			}
		})
	}
}

func TestAcquireHardLink(t *testing.T) {
	const victimContent = "precious content\n"

	tests := map[string]struct {
		// linkVictim makes the lock path a second hard link of a file with
		// victimContent before the attempt.
		linkVictim bool
		// lockVictim holds a lock on the linked file through its other name
		// during the attempt.
		lockVictim bool
		wantErr    bool
	}{
		"error: a hard link at the lock path leaves the linked file unchanged": {
			linkVictim: true,
			wantErr:    true,
		},
		"error: a hard link to a file locked elsewhere is refused, not reported as held": {
			linkVictim: true,
			lockVictim: true,
			wantErr:    true,
		},
		"success: a lock file with one link is used": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := Path(dir, subA)
			// In dir, so that the link is on the same file system.
			victim := filepath.Join(dir, "victim")
			if tt.linkVictim {
				if err := os.WriteFile(victim, []byte(victimContent), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(victim, path); err != nil {
					t.Fatal(err)
				}
			}
			if tt.lockVictim {
				vf, err := os.OpenFile(victim, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = vf.Close() })
				if err := lockFile(vf); err != nil {
					t.Fatalf("lock the linked file: %v", err)
				}
			}

			l, err := Acquire(dir, subA)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Acquire: %v", err)
				}
				if err := l.Release(); err != nil {
					t.Errorf("Release: %v", err)
				}
				return
			}
			wantErr := "lock: " + path + " has 2 hard links; refusing to use it as the lock file"
			if err == nil || err.Error() != wantErr {
				if l != nil {
					_ = l.Release()
				}
				t.Fatalf("Acquire through a hard link: error = %v, want %q", err, wantErr)
			}
			got, err := os.ReadFile(victim)
			if err != nil {
				t.Fatalf("read linked file: %v", err)
			}
			if diff := gocmp.Diff(victimContent, string(got)); diff != "" {
				t.Errorf("linked file content (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAcquireIdentityCheck makes the lock path name another file when Acquire
// checks it after locking, as when another process replaces the file between
// the open and the lock.
func TestAcquireIdentityCheck(t *testing.T) {
	tests := map[string]struct {
		// replaced is how many checks see another file at the lock path
		// before the real file is seen.
		replaced  int
		wantCalls int
		wantErr   error
	}{
		"success: the path names another file once and the next attempt locks": {
			replaced:  1,
			wantCalls: 2,
		},
		"error: the path keeps naming another file": {
			replaced:  acquireAttempts,
			wantCalls: acquireAttempts,
			wantErr:   errLost,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			other := filepath.Join(t.TempDir(), "other")
			if err := os.WriteFile(other, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			lstat = func(name string) (os.FileInfo, error) {
				calls++
				if calls <= tt.replaced {
					return os.Lstat(other)
				}
				return os.Lstat(name)
			}
			t.Cleanup(func() { lstat = os.Lstat })

			l, err := Acquire(dir, subA)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Acquire error = %v, want %v", err, tt.wantErr)
			}
			if diff := gocmp.Diff(tt.wantCalls, calls); diff != "" {
				t.Errorf("identity checks (-want +got):\n%s", diff)
			}
			if tt.wantErr == nil {
				if err := l.Release(); err != nil {
					t.Errorf("Release: %v", err)
				}
				return
			}
			if l != nil {
				t.Errorf("Acquire returned a lock with error %v", err)
			}
			// Every failed attempt closed its descriptor, so the lock is free.
			lstat = os.Lstat
			free, err := Acquire(dir, subA)
			if err != nil {
				t.Fatalf("Acquire after the failed attempts: %v", err)
			}
			if err := free.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		})
	}
}

func TestRelease(t *testing.T) {
	started := time.Date(2026, 10, 3, 6, 30, 15, 0, time.UTC)

	tests := map[string]struct {
		subscription string
	}{
		"success: release empties the lock file and frees the lock for the next acquirer": {
			subscription: subA,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			l, err := Acquire(dir, tt.subscription)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if err := l.WriteHolder(started, tt.subscription); err != nil {
				t.Fatalf("WriteHolder: %v", err)
			}

			if err := l.Release(); err != nil {
				t.Fatalf("Release: %v", err)
			}

			got, err := os.ReadFile(Path(dir, tt.subscription))
			if err != nil {
				t.Fatalf("read the lock file after Release: %v", err)
			}
			if diff := gocmp.Diff("", string(got)); diff != "" {
				t.Errorf("lock file content after Release (-want +got):\n%s", diff)
			}
			next, err := Acquire(dir, tt.subscription)
			if err != nil {
				t.Fatalf("Acquire after Release: %v", err)
			}
			if err := next.Release(); err != nil {
				t.Errorf("Release of the next holder: %v", err)
			}
		})
	}
}

// replaceLockFile keeps a hard link to the file at the lock path, then puts a
// new file with content at the path, as a process that recreated a removed lock
// file would. It returns the path of the hard link to the original file.
func replaceLockFile(t *testing.T, path, content string) string {
	t.Helper()
	kept := filepath.Join(t.TempDir(), "kept.lock")
	if err := os.Link(path, kept); err != nil {
		t.Fatalf("keep the original lock file: %v", err)
	}
	next := path + ".next"
	if err := os.WriteFile(next, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, path); err != nil {
		t.Fatalf("replace the lock file: %v", err)
	}
	return kept
}

// TestHolderThroughDescriptor checks that the holder text is written and
// cleared in the locked file, not in whatever file the path names later.
func TestHolderThroughDescriptor(t *testing.T) {
	started := time.Date(2026, 10, 3, 6, 30, 15, 0, time.UTC)
	wantHolder := "pid=" + strconv.Itoa(os.Getpid()) + " started=2026-10-03T06:30:15Z subscription=" + subA + "\n"
	const otherHolder = "pid=1 started=2026-10-03T00:00:00Z subscription=" + subA + "\n"

	tests := map[string]struct {
		// release releases the lock after writing the holder text.
		release    bool
		wantLocked string
	}{
		"success: holder text goes to the locked file only": {
			wantLocked: wantHolder,
		},
		"success: release clears the locked file only": {
			release:    true,
			wantLocked: "",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			l, err := Acquire(dir, subA)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			kept := replaceLockFile(t, l.Path(), otherHolder)

			if err := l.WriteHolder(started, subA); err != nil {
				t.Fatalf("WriteHolder: %v", err)
			}
			if tt.release {
				if err := l.Release(); err != nil {
					t.Fatalf("Release: %v", err)
				}
			} else {
				t.Cleanup(func() { _ = l.Release() })
			}

			got, err := os.ReadFile(kept)
			if err != nil {
				t.Fatal(err)
			}
			if diff := gocmp.Diff(tt.wantLocked, string(got)); diff != "" {
				t.Errorf("locked file content (-want +got):\n%s", diff)
			}
			got, err = os.ReadFile(l.Path())
			if err != nil {
				t.Fatal(err)
			}
			if diff := gocmp.Diff(otherHolder, string(got)); diff != "" {
				t.Errorf("content of the file now at the lock path (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHolderOverwritesLongerLine(t *testing.T) {
	tests := map[string]struct {
		leftover string
	}{
		"success: a longer line left by a killed holder is cut off": {
			leftover: "pid=1234567 started=2026-10-03T00:00:00Z subscription=" + subA + " and much more text\n",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(Path(dir, subA), []byte(tt.leftover), 0o600); err != nil {
				t.Fatal(err)
			}
			l, err := Acquire(dir, subA)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			t.Cleanup(func() { _ = l.Release() })
			started := time.Date(2026, 10, 3, 6, 30, 15, 0, time.UTC)
			if err := l.WriteHolder(started, subA); err != nil {
				t.Fatalf("WriteHolder: %v", err)
			}
			want := "pid=" + strconv.Itoa(os.Getpid()) + " started=2026-10-03T06:30:15Z subscription=" + subA
			if diff := gocmp.Diff(want, Holder(dir, subA)); diff != "" {
				t.Errorf("Holder (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	tests := map[string]struct {
		// change alters the lock path while the lock is held.
		change  func(t *testing.T, path string)
		wantErr error
	}{
		"success: an untouched lock file": {
			change: func(*testing.T, string) {},
		},
		"error: the lock file was removed": {
			change: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: errLost,
		},
		"error: another file replaced the lock file": {
			change: func(t *testing.T, path string) {
				t.Helper()
				replaceLockFile(t, path, "")
			},
			wantErr: errLost,
		},
		"error: a link to the locked file replaced it": {
			change: func(t *testing.T, path string) {
				t.Helper()
				kept := filepath.Join(t.TempDir(), "kept.lock")
				if err := os.Link(path, kept); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(kept, path); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: errLost,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			l, err := Acquire(t.TempDir(), subA)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			t.Cleanup(func() { _ = l.Release() })
			tt.change(t, l.Path())

			if err := l.Check(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Check() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestWatchRemovedLockFile covers what happens when the lock file is removed
// while its holder is alive. The next acquirer creates and locks a new file and
// cannot see the old holder, so Acquire succeeds; Watch is what makes the old
// holder notice and stop, so that the two do not stay holders together.
func TestWatchRemovedLockFile(t *testing.T) {
	const interval = 10 * time.Millisecond

	tests := map[string]struct {
		// remove deletes the lock file and lets a second acquirer lock a new
		// one while the first holder runs Watch.
		remove  bool
		wantErr error
	}{
		"success: Watch returns nil when its context ends": {},
		"error: the holder notices that its lock file was removed and a second acquirer locked a new one": {
			remove:  true,
			wantErr: errLost,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			first, err := Acquire(dir, subA)
			if err != nil {
				t.Fatalf("Acquire for the first holder: %v", err)
			}
			t.Cleanup(func() { _ = first.Release() })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			watched := make(chan error, 1)
			go func() { watched <- first.Watch(ctx, interval) }()

			if tt.remove {
				if err := os.Remove(first.Path()); err != nil {
					t.Fatal(err)
				}
				second, err := Acquire(dir, subA)
				if err != nil {
					t.Fatalf("Acquire for the second holder after the removal: %v", err)
				}
				t.Cleanup(func() { _ = second.Release() })
			} else {
				// Several checks pass before the context ends.
				time.Sleep(5 * interval)
				cancel()
			}

			select {
			case err := <-watched:
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Watch() error = %v, want %v", err, tt.wantErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Watch did not return within 2s")
			}
		})
	}
}
