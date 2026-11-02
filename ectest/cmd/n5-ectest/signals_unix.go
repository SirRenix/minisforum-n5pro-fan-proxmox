//go:build unix

package main

import (
	"os/signal"
	"syscall"
)

// ignoreTerminalStops ignores the job-control signals of the terminal and
// SIGPIPE: a stopped worker would leave the channel manual without any limit
// check, a dead pipe reader must not kill a process before it restored.
func ignoreTerminalStops() {
	signal.Ignore(syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU, syscall.SIGPIPE)
}
