//go:build !unix

package writerlock

import (
	"errors"
	"os"
	"path/filepath"
)

const (
	Name = "writer.lock"
	Dir  = "n5-fangov"
)

var (
	ErrBusy     = errors.New("another pwm writer holds the lock")
	ErrReplaced = errors.New("writer lock file removed or replaced while held")
	errNoFlock  = errors.New("writerlock: flock(2) is not available on this platform")
)

type Lock struct{}

func Path(runRoot string) string            { return filepath.Join(runRoot, Dir, Name) }
func Acquire(string, string) (*Lock, error) { return nil, errNoFlock }
func Adopt(*os.File, string) (*Lock, error) { return nil, errNoFlock }
func (l *Lock) File() *os.File              { return nil }
func (l *Lock) LockPath() string            { return "" }
func (l *Lock) Verify() error               { return errNoFlock }
func (l *Lock) Release()                    {}
