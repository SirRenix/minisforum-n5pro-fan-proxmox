//go:build linux

package sysroot

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadTimeout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvRoot, dir)
	if !Testing() || Sys() != filepath.Join(dir, "sys") || Debugfs() != filepath.Join(dir, "sys", "kernel", "debug") {
		t.Fatalf("roots %s %s", Sys(), Debugfs())
	}
	p := filepath.Join(dir, "a")
	os.WriteFile(p, []byte(" 42 \n"), 0o644)
	if s, err := ReadTimeout(p, time.Second); err != nil || s != "42" {
		t.Fatalf("%q %v", s, err)
	}
	// a FIFO without a writer blocks open(): like a hung sysfs read
	fifo := filepath.Join(dir, "hang")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	start := time.Now()
	if _, err := ReadTimeout(fifo, 50*time.Millisecond); !errors.Is(err, ErrTimeout) || time.Since(start) > 2*time.Second {
		t.Fatalf("hang: %v", err)
	}
	// unblock the reader goroutine
	if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
		f.Close()
	}
	t.Setenv(EnvRoot, "")
	if Sys() != "/sys" || Run() != "/run" {
		t.Fatal("host roots")
	}
}
