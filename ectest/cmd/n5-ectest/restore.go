package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/writerlock"
)

func init() {
	register("restore", command{usage: "restore [--from DIR | --discard]", help: "put the channels of an interrupted map/handback back", run: cmdRestore})
}

// discardText is what the operator types to give up a state file.
const discardText = "DISCARD"

// cmdRestore puts the channels of an interrupted writing run back into
// their restore values: from <run>/n5-ectest/restore.json, or from the
// restore.json of a bundle (--from DIR) when the run state is gone. Same
// checks as the guardian: n5-fangov stopped, writer lock free, values auto
// or >= 100, the driver device unchanged. --discard gives up a state file
// that cannot be restored (driver device changed) after showing the
// channels; it writes nothing to the EC.
func cmdRestore(args []string) int {
	fl := flag.NewFlagSet("restore", flag.ContinueOnError)
	fl.SetOutput(stderr)
	from := fl.String("from", "", "bundle directory whose restore.json is used (only when no run state is open)")
	discard := fl.Bool("discard", false, "give up the open run state without writing (after a module reload: the device changed)")
	if rc, ok := parseFlags(fl, args, nil); !ok {
		return rc
	}
	if fl.NArg() != 0 || (*from != "" && *discard) {
		fmt.Fprintln(stderr, "usage: n5-ectest restore [--from DIR | --discard]")
		return exitUsage
	}
	if !requireRoot("restore") {
		return exitFail
	}
	if run, st := fangovRunning(); run {
		fmt.Fprintf(stderr, "restore: n5-fangov is %s and sets the channels itself; nothing written\n", st)
		return exitFail
	}
	lock, err := writerlock.Acquire(sysroot.Run(), "n5-ectest restore")
	if err != nil {
		fmt.Fprintf(stderr, "restore: %v (a run is still active; its guardian restores)\n", err)
		return exitFail
	}
	defer lock.Release()
	sp := statePath()
	runSt, runErr := readState(sp)
	if runErr != nil && !errors.Is(runErr, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "restore: %v\n", runErr)
		fmt.Fprintln(stderr, "          the run state is not readable: check it by hand (cat it, read pwmN_enable/pwmN), fix or remove it")
		return exitFail
	}
	if *discard {
		return discardState(runSt, sp)
	}
	if *from == "" {
		if runSt == nil {
			fmt.Fprintf(stdout, "restore: no %s: nothing to restore\n", sp)
			return exitOK
		}
		if !stateOpen(runSt) {
			fmt.Fprintf(stdout, "restore: %s is marked %s (pid %d, %s): nothing to do\n", sp, runSt.State, runSt.PID, runSt.Started)
			return exitOK
		}
		lines, ok := restoreFromState(runSt, sp)
		lines = append([]string{fmt.Sprintf("restore by hand from %s (run pid %d, %s)", sp, runSt.PID, runSt.Started)}, lines...)
		// the bundle path of a run state was written by the worker (root, <run>)
		appendEvents(runSt.Bundle, lines)
		return printRestore(lines, ok)
	}
	// --from: only when no run state is open. The run state is never
	// replaced by bundle data, and only the named bundle is written to.
	if runSt != nil && stateOpen(runSt) {
		fmt.Fprintf(stderr, "restore: %s is open (pid %d, %s, channels %v): run n5-ectest restore without --from\n", sp, runSt.PID, runSt.Started, runSt.Channels)
		return exitFail
	}
	bp := filepath.Join(*from, "restore.json")
	st, err := readState(bp)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(stdout, "restore: no %s: nothing to restore\n", bp)
		return exitOK
	case err != nil:
		fmt.Fprintf(stderr, "restore: %v\n", err)
		return exitFail
	}
	lines, ok := restoreFromState(st, "")
	lines = append([]string{fmt.Sprintf("restore by hand from %s (run pid %d, %s)", bp, st.PID, st.Started)}, lines...)
	appendEvents(*from, lines)
	return printRestore(lines, ok)
}

func printRestore(lines []string, ok bool) int {
	red := newRedactor()
	for _, l := range lines {
		fmt.Fprintln(stdout, red.String(l))
	}
	if !ok {
		fmt.Fprintln(stderr, "restore: not every channel was restored")
		return exitFail
	}
	return exitOK
}

// discardState marks an open run state as given up after the operator saw
// the channels and typed DISCARD at a terminal. Nothing is written to the EC.
func discardState(st *stateFile, sp string) int {
	if st == nil || !stateOpen(st) {
		fmt.Fprintf(stdout, "restore --discard: no open run state in %s: nothing to discard\n", sp)
		return exitOK
	}
	dev, dir, derr := pwmw.Resolve(sysroot.Sys())
	fmt.Fprintf(stdout, "Open run state %s: pid %d, started %s, bundle %s\n", sp, st.PID, st.Started, orDash(st.Bundle))
	fmt.Fprintf(stdout, "driver device at the run: %s\n", st.Device)
	if derr != nil {
		fmt.Fprintf(stdout, "driver device now:        none (%v)\n", derr)
	} else {
		fmt.Fprintf(stdout, "driver device now:        %s\n", dev)
	}
	var chans []int
	for k := range st.Channels {
		if ch, err := strconv.Atoi(k); err == nil && ch >= 1 && ch <= 4 {
			chans = append(chans, ch)
		}
	}
	for _, ch := range sortedIntKeys(chans) {
		now := "not readable"
		if derr == nil {
			en, e1 := sysroot.ReadTimeout(filepath.Join(dir, pwmw.Enable.Name(ch)), sysroot.DefaultTimeout)
			d, e2 := sysroot.ReadTimeout(filepath.Join(dir, pwmw.Duty.Name(ch)), sysroot.DefaultTimeout)
			if e1 == nil && e2 == nil {
				now = fmt.Sprintf("pwm%d_enable=%s pwm%d=%s", ch, en, ch, d)
			}
		}
		fmt.Fprintf(stdout, "pwm%d: restore value %s, now %s\n", ch, st.Channels[strconv.Itoa(ch)], now)
	}
	fmt.Fprintln(stdout, "Discarding writes nothing. Set the channels yourself (pwmN_enable=2, or start n5-fangov) before you leave the box.")
	if detectSession().Stdin == "" {
		fmt.Fprintln(stderr, "restore --discard: standard input is not a terminal; type the confirmation at the device")
		return exitFail
	}
	fmt.Fprintf(stdout, "Type exactly  %s  to give the run state up: ", discardText)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	if strings.TrimSpace(line) != discardText {
		fmt.Fprintln(stdout, "\nNot discarded.")
		return exitFail
	}
	st.State = stateDiscarded
	if err := st.write(sp); err != nil {
		fmt.Fprintf(stderr, "restore --discard: %v\n", err)
		return exitFail
	}
	at := time.Now().UTC().Format(time.RFC3339)
	appendEvents(st.Bundle, []string{fmt.Sprintf("%s run state discarded by hand (%s %s): nothing written; channels %v", at, toolName, version, st.Channels)})
	fmt.Fprintf(stdout, "\n%s marked discarded.\n", sp)
	return exitOK
}
