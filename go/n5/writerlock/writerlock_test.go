//go:build unix

package writerlock

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestAcquireBusyVerifyRelease(t *testing.T) {
	run := t.TempDir()
	l, err := Acquire(run, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(run, "second"); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "test pid") {
		t.Fatalf("second acquire: %v", err)
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
	// postrm/uninstall delete /run/n5-fangov: the held lock guards nothing
	if err := os.Remove(Path(run)); err != nil {
		t.Fatal(err)
	}
	if err := l.Verify(); !errors.Is(err, ErrReplaced) {
		t.Fatalf("removed: %v", err)
	}
	// a new file at the path is another inode
	if err := os.WriteFile(Path(run), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Verify(); !errors.Is(err, ErrReplaced) {
		t.Fatalf("replaced: %v", err)
	}
	l.Release()
	l.Release()
}

// An adopted descriptor shares the lock; its Release must not unlock the
// parent's open file description.
func TestAdoptKeepsParentLock(t *testing.T) {
	run := t.TempDir()
	l, err := Acquire(run, "parent")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	fd, err := syscall.Dup(int(l.File().Fd()))
	if err != nil {
		t.Fatal(err)
	}
	child, err := Adopt(os.NewFile(uintptr(fd), "lock"), run)
	if err != nil {
		t.Fatal(err)
	}
	// programs the adopter runs must not inherit the lock
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
		t.Fatalf("adopted descriptor without FD_CLOEXEC (flags %#x, errno %v)", flags, errno)
	}
	child.Release()
	if _, err := Acquire(run, "other"); !errors.Is(err, ErrBusy) {
		t.Fatalf("lock released by the adopted copy: %v", err)
	}
	// a descriptor of another file is refused
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := Adopt(f, run); err == nil {
		t.Fatal("foreign descriptor adopted")
	}
}
