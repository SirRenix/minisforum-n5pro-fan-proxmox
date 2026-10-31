//go:build unix

// Package writerlock is the lock every pwm writer of the N5 Pro EC holds
// while it writes: the n5-fangov daemon for its lifetime, n5-fangov
// test/validate and the writing commands of n5-ectest for their run. It is an
// flock(2) on <run>/n5-fangov/writer.lock, released by the kernel when the
// last descriptor of the open file description is closed (also on SIGKILL).
//
// A lock taken by one process can be handed to a child through an inherited
// descriptor (exec.Cmd.ExtraFiles). The child adopts it with Adopt and must
// only Close it: LOCK_UN on the shared open file description would release
// the lock for the parent too.
package writerlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Name is the lock file inside Dir.
const Name = "writer.lock"

// Dir is the lock directory relative to the run root ("/run"); it is the
// RuntimeDirectory of n5-fangov and survives a daemon stop.
const Dir = "n5-fangov"

// ErrBusy: another process holds the lock.
var ErrBusy = errors.New("another pwm writer holds the lock")

// ErrReplaced: the lock file was removed or replaced while held (postrm and
// uninstall scripts delete /run/n5-fangov); the held descriptor no longer
// guards anything.
var ErrReplaced = errors.New("writer lock file removed or replaced while held")

// Lock is a held writer lock.
type Lock struct {
	f       *os.File
	path    string
	adopted bool
}

// Path returns <runRoot>/n5-fangov/writer.lock.
func Path(runRoot string) string { return filepath.Join(runRoot, Dir, Name) }

// Acquire takes the exclusive lock at Path(runRoot), creating the directory
// when n5-fangov is not installed. who is written into the file for the
// message another writer gets.
func Acquire(runRoot, who string) (*Lock, error) {
	p := Path(runRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		b := make([]byte, 256)
		n, _ := f.ReadAt(b, 0)
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			holder := strings.TrimSpace(string(b[:n]))
			if holder == "" {
				holder = "unknown process"
			}
			return nil, fmt.Errorf("%w: %s (%s)", ErrBusy, p, holder)
		}
		return nil, fmt.Errorf("writer lock %s: %w", p, err)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%s pid %d since %s\n", who, os.Getpid(), time.Now().Format(time.RFC3339))), 0)
	return &Lock{f: f, path: p}, nil
}

// Adopt takes over a lock handed down by the parent as descriptor f. It
// checks that f is the lock file at Path(runRoot) (same inode) and that its
// open file description holds the lock (flock on it succeeds without
// blocking only for the holder). The descriptor is marked close-on-exec, so
// programs the adopter runs (systemctl, dpkg-query, ...) do not inherit the
// lock.
func Adopt(f *os.File, runRoot string) (*Lock, error) {
	syscall.CloseOnExec(int(f.Fd()))
	l := &Lock{f: f, path: Path(runRoot), adopted: true}
	if err := l.Verify(); err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("inherited descriptor does not hold the writer lock: %w", err)
	}
	return l, nil
}

// File is the held descriptor (passed to a child in ExtraFiles).
func (l *Lock) File() *os.File { return l.f }

// LockPath is the path the lock was taken at.
func (l *Lock) LockPath() string { return l.path }

// Verify checks that the lock file still exists at its path and is the
// file the descriptor holds (stat inode = fstat inode).
func (l *Lock) Verify() error {
	var a, b syscall.Stat_t
	if err := syscall.Stat(l.path, &a); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrReplaced, l.path, err)
	}
	if err := syscall.Fstat(int(l.f.Fd()), &b); err != nil {
		return fmt.Errorf("%w: fstat: %v", ErrReplaced, err)
	}
	if a.Ino != b.Ino || a.Dev != b.Dev {
		return fmt.Errorf("%w: %s is another file now", ErrReplaced, l.path)
	}
	return nil
}

// Release gives the lock up. For an adopted lock it only closes the
// descriptor: the parent still holds the lock.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	if !l.adopted {
		_ = l.f.Truncate(0)
		_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	}
	l.f.Close()
	l.f = nil
}
