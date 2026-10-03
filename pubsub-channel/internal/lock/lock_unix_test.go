//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package lock

import (
	"errors"
	"syscall"
	"testing"
)

func TestFlockError(t *testing.T) {
	tests := map[string]struct {
		err  error
		want error
	}{
		"success: a successful flock locks": {
			err:  nil,
			want: nil,
		},
		"error: would block means another open file holds the lock": {
			err:  syscall.EWOULDBLOCK,
			want: ErrHeld,
		},
		"error: a bad descriptor is a failure, not a success": {
			err:  syscall.EBADF,
			want: syscall.EBADF,
		},
		"error: no lock available is a failure, not held": {
			err:  syscall.ENOLCK,
			want: syscall.ENOLCK,
		},
		"error: an unsupported operation is a failure, not held": {
			err:  syscall.EOPNOTSUPP,
			want: syscall.EOPNOTSUPP,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// errors.Is with a nil target holds only for a nil error.
			if got := flockError(tt.err); !errors.Is(got, tt.want) {
				t.Errorf("flockError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
