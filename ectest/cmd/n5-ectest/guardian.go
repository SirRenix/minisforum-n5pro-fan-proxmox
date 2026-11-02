package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/runner"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/writerlock"
)

// Guardian (design §7): a writing command re-executes itself as a worker
// and waits. The parent takes the writer lock and hands the descriptor to
// the worker, so a killed worker does not release it and n5-fangov cannot
// start before the restore. The parent catches SIGINT/SIGHUP/SIGTERM/SIGQUIT
// and forwards them, and ignores the terminal stop signals (SIGTSTP,
// SIGTTIN, SIGTTOU) and SIGPIPE, so Ctrl-\, Ctrl-Z or a dead `tee` never
// leaves a channel manual. Before its first write the worker writes the
// state file (restore values and device identity); a worker that ends
// without marking it "restored" (SIGKILL, runtime fatal, OOM) is cleaned up
// by the parent from that file. If both are killed, `n5-ectest restore`
// does it by hand.

const (
	envWorker = "N5ECTEST_WORKER"
	envState  = "N5ECTEST_STATE"
	// the lock descriptor of the worker (first entry of ExtraFiles)
	workerLockFD = 3

	stateActive   = "active"
	stateRestored = "restored"
	// stateDiscarded: the operator gave up a state file that cannot be
	// restored (driver device changed) with `restore --discard`
	stateDiscarded = "discarded"
)

// stopSignals are the signals that end a run: caught (never the Go default
// action) by guardian and worker, forwarded by the guardian.
var stopSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}

// stateOpen reports a state file that still blocks a new run.
func stateOpen(st *stateFile) bool { return st.State != stateRestored && st.State != stateDiscarded }

// stateFile is <run>/n5-ectest/restore.json.
type stateFile struct {
	Tool     string            `json:"tool"`
	Version  string            `json:"version"`
	PID      int               `json:"pid"`
	Started  string            `json:"started"`
	State    string            `json:"state"` // active, restored
	Device   pwmw.Device       `json:"device"`
	Channels map[string]string `json:"channels"` // "3": "140" / "auto"
	Bundle   string            `json:"bundle_dir"`
	TestRoot bool              `json:"test_root,omitempty"`
}

func (s *stateFile) write(p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return bundle.WriteAtomic(p, append(b, '\n'), 0o600)
}

func readState(p string) (*stateFile, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s stateFile
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &s, nil
}

func statePath() string { return filepath.Join(sysroot.StateDir(), "restore.json") }

// workerCommand builds the worker process (tests replace it).
var workerCommand = func(args []string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(exe, args...), nil
}

// beforeGuardianRestore is a test hook, called when the parent is about to
// restore after a worker died.
var beforeGuardianRestore func()

// guardian runs a writing command (map/handback) through a worker.
func guardian(test string, args []string) int {
	// first: a dead pipe reader or Ctrl-Z must not stop the guardian
	ignoreTerminalStops()
	if !requireRoot(test) {
		return exitFail
	}
	if run, st := fangovRunning(); run {
		fmt.Fprintf(stderr, "%s: the n5-fangov daemon is %s. Stop it first: systemctl stop n5-fangov\n", test, st)
		fmt.Fprintln(stderr, "          (TESTSPEC G1: no other pwm writer; there is no override)")
		return exitFail
	}
	lock, err := writerlock.Acquire(sysroot.Run(), fmt.Sprintf("n5-ectest %s (guardian)", test))
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", test, err)
		return exitFail
	}
	defer lock.Release()
	sp := statePath()
	st0, err := readState(sp)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		// fail closed: an unreadable state file may hold an unrestored run
		fmt.Fprintf(stderr, "%s: state file of an earlier run not readable: %v\n", test, err)
		fmt.Fprintf(stderr, "          Check it, then run: n5-ectest restore (or --discard)\n")
		return exitFail
	case stateOpen(st0):
		fmt.Fprintf(stderr, "%s: %s says a run (pid %d, %s) left pwm channel(s) %v not restored. Run first: n5-ectest restore\n", test, sp, st0.PID, st0.Started, st0.Channels)
		return exitFail
	}
	// a restored state file of an earlier run is replaced by this run's
	if err := os.Remove(sp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "%s: %v\n", test, err)
		return exitFail
	}
	cmd, err := workerCommand(append([]string{test}, args...))
	if err != nil {
		fmt.Fprintf(stderr, "%s: worker: %v\n", test, err)
		return exitFail
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.ExtraFiles = []*os.File{lock.File()}
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, envWorker+"=1", envState+"="+sp)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, stopSignals...)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "%s: worker: %v\n", test, err)
		return exitFail
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var werr error
wait:
	for {
		select {
		case s := <-sigs:
			_ = cmd.Process.Signal(s)
		case werr = <-done:
			break wait
		}
	}
	rc := 0
	var ee *exec.ExitError
	switch {
	case werr == nil:
	case errors.As(werr, &ee):
		rc = ee.ExitCode()
	default:
		rc = exitFail
	}
	st, err := readState(sp)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr, "!! %s: state file not readable after the worker ended: %v - check pwm channels, run: n5-ectest restore\n", test, err)
			return exitFail
		}
		// no state file: the worker never got to its first write
		if rc < 0 {
			return exitFail
		}
		return rc
	}
	if st.State == stateRestored {
		if rc < 0 {
			return exitFail
		}
		return rc
	}
	how := "exit 0"
	if werr != nil {
		how = werr.Error()
	}
	if ee != nil {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			how = "killed by " + ws.Signal().String()
		}
	}
	if beforeGuardianRestore != nil {
		beforeGuardianRestore()
	}
	// restore first, then any output (stderr may be a dead pipe)
	lines, ok := restoreFromState(st, sp)
	lines = append([]string{fmt.Sprintf("guardian: worker pid %d ended (%s) without the restore marker; restore from %s", cmd.Process.Pid, how, sp)}, lines...)
	// st.Bundle comes from the state file the worker wrote below <run> (root)
	appendEvents(st.Bundle, lines)
	fmt.Fprintf(stderr, "!! %s: the worker ended (%s) without restoring; restored from %s\n", test, how, sp)
	for _, l := range lines {
		fmt.Fprintln(stderr, l)
	}
	if !ok {
		fmt.Fprintln(stderr, "!! RESTORE FAILED - run: n5-ectest restore")
	}
	return exitFail
}

// restoreFromState writes the restore values of a state file through pwmw
// (device identity checked) and, on success, writes it marked restored to sp
// ("" = write no state file).
func restoreFromState(st *stateFile, sp string) (lines []string, ok bool) {
	w := &pwmw.Writer{SysRoot: sysroot.Sys(), Want: st.Device}
	ok = true
	var chans []int
	for k := range st.Channels {
		ch, err := strconv.Atoi(k)
		if err != nil || ch < 1 || ch > 4 {
			lines = append(lines, fmt.Sprintf("restore: invalid channel %q in the state file: skipped", k))
			ok = false
			continue
		}
		chans = append(chans, ch)
	}
	for _, ch := range sortedIntKeys(chans) {
		val := st.Channels[strconv.Itoa(ch)]
		if ch == spec.PCIeChannel && val != runner.PCIeRestore {
			// a state file of an older version may say "auto": DCR5 has no
			// EC automatic, the restore value is 255 (decision 3, 2026-10-16)
			lines = append(lines, fmt.Sprintf("restore pwm4: state file value %q replaced by %s (no EC automatic)", val, runner.PCIeRestore))
			val = runner.PCIeRestore
		}
		if err := checkRestoreValue(val); err != nil {
			lines = append(lines, fmt.Sprintf("restore pwm%d=%s REFUSED: %v", ch, val, err))
			ok = false
			continue
		}
		at := time.Now().UTC().Format(time.RFC3339)
		if err := w.RestoreTo(ch, val); err != nil {
			lines = append(lines, fmt.Sprintf("%s restore pwm%d=%s FAILED by %s %s: %v", at, ch, val, toolName, version, err))
			ok = false
			continue
		}
		lines = append(lines, fmt.Sprintf("%s restore pwm%d=%s ok by %s %s (device %s)", at, ch, val, toolName, version, st.Device))
	}
	if ok && sp != "" {
		st.State = stateRestored
		if err := st.write(sp); err != nil {
			lines = append(lines, "state file not updated: "+err.Error())
		}
	}
	return lines, ok
}

// checkRestoreValue accepts "auto" or a duty 100..255 (the same check as
// --restore).
func checkRestoreValue(v string) error {
	if v == "auto" {
		return nil
	}
	d, err := strconv.Atoi(v)
	if err != nil || d > 255 {
		return fmt.Errorf("%q is not auto or a duty", v)
	}
	if d < pwmw.MinDuty {
		return fmt.Errorf("duty %d below %d is never written", d, pwmw.MinDuty)
	}
	return nil
}

// appendEventsSwap runs between Lstat and OpenFile in appendEvents; tests
// swap events.log there.
var appendEventsSwap = func() {}

// appendEvents adds lines to a bundle's events.log and rewrites its
// SHA256SUMS (the worker died before it could). Without a usable events.log
// it says so on stderr: the restore lines are then only on the terminal.
func appendEvents(dir string, lines []string) {
	if dir == "" {
		return
	}
	// only an existing regular events.log, never through a symlink (a bundle
	// from elsewhere must not make root append to another file); every
	// access, SHA256SUMS included, goes through one handle of the bundle
	// directory (#15), so a writable parent cannot redirect it later
	p := filepath.Join(dir, bundle.Events)
	missing := func(why string) {
		fmt.Fprintf(stderr, "note: %s %s: the restore lines are not recorded in the bundle\n", p, why)
	}
	root, err := bundle.OpenDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		missing("not found (" + err.Error() + ")")
		return
	}
	if err != nil {
		missing("not usable (" + err.Error() + ")")
		return
	}
	defer root.Close()
	lfi, err := root.Lstat(bundle.Events)
	if err != nil {
		missing("not found (" + err.Error() + ")")
		return
	}
	if !lfi.Mode().IsRegular() {
		missing("is not a regular file")
		return
	}
	appendEventsSwap()
	f, err := root.OpenFile(bundle.Events, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		missing("not writable (" + err.Error() + ")")
		return
	}
	// the opened file must be the regular file Lstat saw: an events.log
	// swapped between Lstat and OpenFile (a new file, or a symlink to
	// another file of the bundle, which os.Root follows) is not written
	if fi, err := f.Stat(); err != nil || !os.SameFile(fi, lfi) {
		f.Close()
		missing("changed while opened")
		return
	}
	red := newRedactor()
	for _, l := range lines {
		_, _ = f.WriteString(red.String(l) + "\n")
	}
	f.Close()
	_ = bundle.WriteSumsIn(root)
}

func sortedIntKeys(v []int) []int {
	out := append([]int(nil), v...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// workerMain is the worker side: adopt the lock handed down, then run.
func workerMain(args []string) int {
	if len(args) == 0 || (args[0] != "map" && args[0] != "handback") {
		fmt.Fprintln(stderr, "n5-ectest: worker mode without a writing command")
		return exitUsage
	}
	lock, err := writerlock.Adopt(os.NewFile(workerLockFD, "writer.lock"), sysroot.Run())
	if err != nil {
		fmt.Fprintf(stderr, "%s: worker without the writer lock of the guardian: %v\n", args[0], err)
		return exitFail
	}
	defer lock.Release()
	o, rc := parseRunOpts(args[0], args[1:])
	if rc != exitOK {
		return exitUsage
	}
	if o.dry {
		return runValidation(o, nil, "")
	}
	sp := os.Getenv(envState)
	if err := checkWorkerState(sp); err != nil {
		fmt.Fprintf(stderr, "%s: worker: %v\n", o.test, err)
		return exitFail
	}
	return runValidation(o, lock, sp)
}

// checkWorkerState accepts only the state file the guardian hands down:
// exactly statePath() (not any path ending in restore.json, #16), and no
// state file there that still records an unrestored run (the guardian
// removed a closed one before it started the worker).
func checkWorkerState(sp string) error {
	if sp == "" {
		return errors.New("no state file path")
	}
	if want := statePath(); sp != want {
		return fmt.Errorf("state file path %q is not %s", sp, want)
	}
	st, err := readState(sp)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("state file not readable: %w", err)
	case stateOpen(st):
		return fmt.Errorf("%s records an unrestored run (pid %d); run: n5-ectest restore", sp, st.PID)
	}
	return nil
}
