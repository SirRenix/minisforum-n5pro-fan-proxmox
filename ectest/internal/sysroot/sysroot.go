// Package sysroot resolves every host path n5-ectest reads or writes below
// one root: "/" on a real host, the directory in N5ECTEST_ROOT in tests (a
// fake sys/proc/run/etc/dev tree). With N5ECTEST_ROOT set nothing outside
// that tree is touched, and results are marked as test-root runs.
//
// Reads go through ReadTimeout: a sysfs read that hangs in the driver (EC
// timeout) returns ErrTimeout after the limit instead of stopping the
// caller; the reading goroutine is left to finish on its own.
package sysroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
)

// EnvRoot is the environment variable of the test root.
const EnvRoot = "N5ECTEST_ROOT"

// DefaultTimeout bounds one sysfs/debugfs/proc read.
const DefaultTimeout = 5 * time.Second

// ErrTimeout: the read did not return within the limit.
var ErrTimeout = errors.New("read timed out")

// Root returns the test root, "" on a real host.
func Root() string { return os.Getenv(EnvRoot) }

// Testing reports whether a test root is set.
func Testing() bool { return Root() != "" }

func join(rel string) string {
	r := Root()
	if r == "" {
		return "/" + rel
	}
	return filepath.Join(r, rel)
}

func Sys() string     { return join("sys") }
func Proc() string    { return join("proc") }
func Run() string     { return join("run") }
func Etc() string     { return join("etc") }
func Dev() string     { return join("dev") }
func Debugfs() string { return filepath.Join(Sys(), "kernel", "debug") }

// Roots are the identity roots below the root.
func Roots() identity.Roots { return identity.Roots{Sys: Sys(), Proc: Proc(), Etc: Etc(), Run: Run()} }

// StateDir is n5-ectest's own run directory (<run>/n5-ectest).
func StateDir() string { return filepath.Join(Run(), "n5-ectest") }

// ReadTimeout reads a small file (sysfs attribute, /proc entry) and returns
// its content trimmed of surrounding white space.
func ReadTimeout(path string, d time.Duration) (string, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		b, err := os.ReadFile(path)
		ch <- res{b, err}
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return "", r.err
		}
		return strings.TrimSpace(string(r.b)), nil
	case <-t.C:
		return "", fmt.Errorf("%w after %s: %s", ErrTimeout, d, path)
	}
}

// Read is ReadTimeout with DefaultTimeout; "" on any error.
func Read(path string) string {
	s, _ := ReadTimeout(path, DefaultTimeout)
	return s
}
