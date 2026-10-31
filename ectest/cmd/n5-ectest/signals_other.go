//go:build !unix

package main

import (
	"os/signal"
	"syscall"
)

// ignoreTerminalStops: no job control here; SIGPIPE only.
func ignoreTerminalStops() { signal.Ignore(syscall.SIGPIPE) }
