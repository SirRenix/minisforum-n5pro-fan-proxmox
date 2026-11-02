package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/writerlock"
)

// The worker is SIGKILLed after its state file and a write: the parent
// still holds the writer lock (n5-fangov cannot start), restores from the
// state file, logs to the bundle's events.log and exits 1.
func TestGuardianRestoresAfterSIGKILL(t *testing.T) {
	e := newEnv(t, fake.Reference())
	bdir := filepath.Join(e.out, "b")
	bw, err := bundle.Create(bdir, nil)
	if err != nil {
		t.Fatal(err)
	}
	bw.Event("start")
	if err := bw.Finish(); err != nil {
		t.Fatal(err)
	}
	old := workerCommand
	workerCommand = func(args []string) (*exec.Cmd, error) {
		c := exec.Command(os.Args[0], args...)
		c.Env = append(os.Environ(), "N5ECTEST_TESTMAIN=dying-worker", "N5ECTEST_TEST_BUNDLE="+bdir)
		return c, nil
	}
	defer func() { workerCommand = old }()
	lockHeld := false
	beforeGuardianRestore = func() {
		_, err := writerlock.Acquire(sysroot.Run(), "n5-fangov")
		lockHeld = errors.Is(err, writerlock.ErrBusy)
	}
	defer func() { beforeGuardianRestore = nil }()
	rc := guardian("map", []string{"pwm3"})
	if rc != exitFail || !lockHeld {
		t.Fatalf("rc %d, lock held by the parent %v\n%s", rc, lockHeld, e.stderr)
	}
	// the child wrote pwm3=217 itself (no driver model there); the parent
	// wrote the restore value 140 through pwmw (with the model)
	if got := e.pwmWrites(); got != "pwm3_enable=1 pwm3=140" {
		t.Fatalf("parent writes %q", got)
	}
	if e.h.Get(e.h.HwmonRel()+"/pwm3") != "140" {
		t.Fatalf("pwm3 = %s", e.h.Get(e.h.HwmonRel()+"/pwm3"))
	}
	st, err := readState(statePath())
	if err != nil || st.State != stateRestored {
		t.Fatalf("state %+v %v", st, err)
	}
	ev, _ := os.ReadFile(filepath.Join(bdir, "events.log"))
	if !strings.Contains(string(ev), "guardian: worker pid") || !strings.Contains(string(ev), "killed by killed") || !strings.Contains(string(ev), "restore pwm3=140 ok by n5-ectest") {
		t.Fatalf("events:\n%s", ev)
	}
	if p := bundle.VerifySums(bdir); len(p) != 0 {
		t.Fatalf("sums %v", p)
	}
	// the lock is free again after the guardian
	l, err := writerlock.Acquire(sysroot.Run(), "x")
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
}

// End to end through the real re-exec: guardian process and worker process
// of the test binary, the worker SIGKILLed in T1 by the test hook.
func TestGuardianEndToEnd(t *testing.T) {
	if os.Getuid() == 0 && os.Getenv("CI") == "" {
		t.Log("running as root")
	}
	h := fake.New(t, fake.Reference())
	out := t.TempDir()
	cmd := exec.Command(os.Args[0], "map", "pwm3", "--boot", "cold", "--out", out)
	cmd.Env = append(os.Environ(), "N5ECTEST_TESTMAIN=1", "N5ECTEST_ROOT="+h.Root, "N5ECTEST_TEST_KILL_AT=80")
	cmd.Stdin = strings.NewReader(confirmText + "\n")
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != exitFail {
		t.Fatalf("exit %v\n%s\n%s", err, so.String(), se.String())
	}
	if !strings.Contains(se.String(), "the worker ended (killed by killed) without restoring") {
		t.Fatalf("stderr:\n%s", se.String())
	}
	if h.Get(h.HwmonRel()+"/pwm3_enable") != "2" {
		t.Fatalf("pwm3_enable = %s, want 2 (restore value auto)", h.Get(h.HwmonRel()+"/pwm3_enable"))
	}
	ents, _ := os.ReadDir(out)
	if len(ents) != 1 {
		t.Fatalf("bundle %v", ents)
	}
	dir := filepath.Join(out, ents[0].Name())
	ev, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	if !strings.Contains(string(ev), "write pwm3=255") || !strings.Contains(string(ev), "guardian: worker pid") || !strings.Contains(string(ev), "restore pwm3=auto ok") {
		t.Fatalf("events:\n%s", ev)
	}
	// only the provisional meta.json (killed): it keeps the provenance, the
	// report says the run did not finish and fails
	var rs bytes.Buffer
	stdout = &rs
	defer func() { stdout = os.Stdout }()
	if rc := cmdReport([]string{dir}); rc != exitFail || !strings.Contains(rs.String(), "meta.json is the provisional one") {
		t.Fatalf("report of a killed run: rc %d\n%s", rc, rs.String())
	}
	if m := readMeta(t, dir); m["provisional"] != true || m["provenance"] == nil || m["restore_values"] == nil {
		t.Fatalf("provisional meta: %v", m)
	}
	// a normal run through the guardian ends with the state restored
	out2 := t.TempDir()
	cmd = exec.Command(os.Args[0], "map", "pwm1", "--boot", "cold", "--out", out2)
	cmd.Env = append(os.Environ(), "N5ECTEST_TESTMAIN=1", "N5ECTEST_ROOT="+h.Root)
	cmd.Stdin = strings.NewReader(confirmText + "\n")
	so.Reset()
	se.Reset()
	cmd.Stdout, cmd.Stderr = &so, &se
	err = cmd.Run()
	// without the driver model the tachs do not follow: T1 is not PASS, but
	// the run completes and restores
	if ee, ok := err.(*exec.ExitError); err != nil && (!ok || ee.ExitCode() != exitOK) {
		t.Fatalf("second run: %v\n%s\n%s", err, so.String(), se.String())
	}
	st, err := readState(filepath.Join(h.Root, "run", "n5-ectest", "restore.json"))
	if err != nil || st.State != stateRestored {
		t.Fatalf("state %+v %v\n%s", st, err, so.String())
	}
}

// restore by hand from an active state file.
func TestRestoreCommand(t *testing.T) {
	e := newEnv(t, fake.Reference())
	if rc := cmdRestore(nil); rc != exitOK || !strings.Contains(e.stdout.String(), "nothing to restore") {
		t.Fatalf("no state: rc %d %s", rc, e.stdout)
	}
	dev := mustResolve(t)
	st := &stateFile{Tool: toolName, PID: 1, State: stateActive, Device: dev, Channels: map[string]string{"2": "auto", "3": "160"}}
	if err := st.write(statePath()); err != nil {
		t.Fatal(err)
	}
	// the lock is held by a running guardian: refused
	l, _ := writerlock.Acquire(sysroot.Run(), "guardian")
	if rc := cmdRestore(nil); rc != exitFail || len(e.h.AllWrites) != 0 {
		t.Fatalf("lock held: rc %d", rc)
	}
	l.Release()
	if rc := cmdRestore(nil); rc != exitOK {
		t.Fatalf("rc %d %s %s", rc, e.stdout, e.stderr)
	}
	if got := e.pwmWrites(); got != "pwm2_enable=2 pwm3_enable=1 pwm3=160" {
		t.Fatalf("writes %q", got)
	}
	if rc := cmdRestore(nil); rc != exitOK || !strings.Contains(e.stdout.String(), "marked restored") {
		t.Fatalf("second: rc %d", rc)
	}
	// a value below 100 in a (forged) file is refused
	st.State, st.Channels = stateActive, map[string]string{"3": "60"}
	if err := st.write(statePath()); err != nil {
		t.Fatal(err)
	}
	n := len(e.h.AllWrites)
	if rc := cmdRestore(nil); rc != exitFail || len(e.h.AllWrites) != n {
		t.Fatalf("60: rc %d", rc)
	}
	e.units["n5-fangov"] = "active"
	if rc := cmdRestore(nil); rc != exitFail {
		t.Fatalf("n5-fangov running: rc %d", rc)
	}
}

// startGroup runs the test binary as n5-ectest in its own process group
// and waits at most a minute (a stopped process would hang the test).
func startGroup(t *testing.T, h *fake.Host, env []string, stdoutW, stderrW *os.File, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(append(os.Environ(), "N5ECTEST_TESTMAIN=1", "N5ECTEST_ROOT="+h.Root), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = strings.NewReader(confirmText + "\n")
	var se bytes.Buffer
	if stdoutW != nil {
		cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	} else {
		cmd.Stdout, cmd.Stderr = &se, &se
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		rc := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			rc = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return rc, se.String()
	case <-time.After(time.Minute):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		t.Fatalf("n5-ectest %v did not end within a minute (stopped?)\n%s", args, se.String())
	}
	return 0, ""
}

// Ctrl-\ sends SIGQUIT to the whole foreground group: the worker restores
// like on Ctrl-C (no Go stack dump, no exit 2), the guardian waits.
func TestCtrlBackslashRestores(t *testing.T) {
	h := fake.New(t, fake.Reference())
	rc, out := startGroup(t, h, []string{"N5ECTEST_TEST_SIG_AT=80", "N5ECTEST_TEST_SIG=QUIT"}, nil, nil, "map", "pwm3", "--boot", "cold", "--out", t.TempDir())
	if rc != exitFail || h.Get(h.HwmonRel()+"/pwm3_enable") != "2" || strings.Contains(out, "goroutine ") {
		t.Fatalf("rc %d, pwm3_enable %s\n%s", rc, h.Get(h.HwmonRel()+"/pwm3_enable"), out)
	}
	st, err := readState(filepath.Join(h.Root, "run", "n5-ectest", "restore.json"))
	if err != nil || st.State != stateRestored || !strings.Contains(out, "cancelled (signal)") {
		t.Fatalf("state %+v %v\n%s", st, err, out)
	}
}

// Ctrl-Z (SIGTSTP to the group) does not stop a run: it goes on with its
// limit checks and ends restored.
func TestCtrlZIgnored(t *testing.T) {
	h := fake.New(t, fake.Reference())
	rc, out := startGroup(t, h, []string{"N5ECTEST_TEST_SIG_AT=80", "N5ECTEST_TEST_SIG=TSTP"}, nil, nil, "map", "pwm1", "--boot", "cold", "--out", t.TempDir())
	st, err := readState(filepath.Join(h.Root, "run", "n5-ectest", "restore.json"))
	if rc != exitOK || err != nil || st.State != stateRestored || strings.Contains(out, "ABORTED") || h.Get(h.HwmonRel()+"/pwm1_enable") != "2" {
		t.Fatalf("rc %d state %+v %v\n%s", rc, st, err, out)
	}
}

// `... | tee log` with tee gone and the worker killed: the guardian
// restores before it writes anything (SIGPIPE ignored, output last).
func TestGuardianRestoresWithDeadPipe(t *testing.T) {
	h := fake.New(t, fake.Reference())
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	rc, _ := startGroup(t, h, []string{"N5ECTEST_TEST_KILL_AT=80"}, w, w, "map", "pwm3", "--boot", "cold", "--out", t.TempDir())
	w.Close()
	st, err := readState(filepath.Join(h.Root, "run", "n5-ectest", "restore.json"))
	if rc != exitFail || err != nil || st.State != stateRestored || h.Get(h.HwmonRel()+"/pwm3_enable") != "2" {
		t.Fatalf("rc %d, state %+v %v, pwm3_enable %s", rc, st, err, h.Get(h.HwmonRel()+"/pwm3_enable"))
	}
}

// A state file that cannot be read blocks the next run (fail closed).
func TestGuardianRefusesUnreadableState(t *testing.T) {
	e := newEnv(t, fake.Reference())
	if err := os.MkdirAll(filepath.Dir(statePath()), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := guardian("map", []string{"pwm3"}); rc != exitFail || !strings.Contains(e.stderr.String(), "not readable") || len(e.h.AllWrites) != 0 {
		t.Fatalf("rc %d %s", rc, e.stderr)
	}
	if _, err := os.Stat(statePath()); err != nil {
		t.Fatal("unreadable state file removed")
	}
}

// restore --from never replaces an open run state, and writes only into
// the named bundle, never through a symlink.
func TestRestoreFrom(t *testing.T) {
	e := newEnv(t, fake.Reference())
	dev := mustResolve(t)
	active := &stateFile{Tool: toolName, PID: 4711, Started: "2026-10-10T09:00:00Z", State: stateActive, Device: dev, Channels: map[string]string{"3": "140"}}
	if err := active.write(statePath()); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(e.out, "oldbundle")
	if err := os.MkdirAll(b, 0o750); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(e.out, "victim.txt")
	os.WriteFile(victim, []byte("keep\n"), 0o600)
	if err := os.Symlink(victim, filepath.Join(b, "events.log")); err != nil {
		t.Fatal(err)
	}
	old := &stateFile{Tool: toolName, PID: 42, State: stateActive, Device: dev, Channels: map[string]string{"2": "auto"}, Bundle: e.out}
	if err := old.write(filepath.Join(b, "restore.json")); err != nil {
		t.Fatal(err)
	}
	if rc := cmdRestore([]string{"--from", b}); rc != exitFail || len(e.h.AllWrites) != 0 {
		t.Fatalf("open run state: rc %d", rc)
	}
	if st, _ := readState(statePath()); st.PID != 4711 || st.State != stateActive || st.Channels["3"] != "140" {
		t.Fatalf("run state replaced: %+v", st)
	}
	// run state restored: --from restores from the bundle, leaves the run
	// state as it is, and does not follow the events.log symlink
	if rc := cmdRestore(nil); rc != exitOK {
		t.Fatalf("restore: rc %d %s", rc, e.stderr)
	}
	before, _ := os.ReadFile(statePath())
	if rc := cmdRestore([]string{"--from", b}); rc != exitOK || !strings.Contains(e.pwmWrites(), "pwm2_enable=2") {
		t.Fatalf("--from: rc %d writes %q %s", rc, e.pwmWrites(), e.stderr)
	}
	after, _ := os.ReadFile(statePath())
	if string(before) != string(after) {
		t.Fatalf("run state changed by --from:\n%s", after)
	}
	if v, _ := os.ReadFile(victim); string(v) != "keep\n" {
		t.Fatalf("symlink followed: %q", v)
	}
	if _, err := os.Stat(filepath.Join(e.out, "SHA256SUMS")); err == nil {
		t.Fatal("wrote into the bundle_dir named in the bundle's restore.json")
	}
}

// restore --discard gives up a state whose device changed, after the
// confirmation, without writing.
func TestRestoreDiscard(t *testing.T) {
	e := newEnv(t, fake.Reference())
	dev := mustResolve(t)
	dev.SrcVersion = "OTHERSRCVERSION"
	st := &stateFile{Tool: toolName, PID: 7, State: stateActive, Device: dev, Channels: map[string]string{"3": "140"}}
	if err := st.write(statePath()); err != nil {
		t.Fatal(err)
	}
	if rc := cmdRestore(nil); rc != exitFail {
		t.Fatalf("restore with a changed device: rc %d", rc)
	}
	stdin = strings.NewReader("no\n")
	if rc := cmdRestore([]string{"--discard"}); rc != exitFail {
		t.Fatalf("discard without confirmation: rc %d", rc)
	}
	stdin = strings.NewReader(discardText + "\n")
	if rc := cmdRestore([]string{"--discard"}); rc != exitOK || !strings.Contains(e.stdout.String(), "pwm3: restore value 140, now pwm3_enable=") {
		t.Fatalf("discard: rc %d\n%s%s", rc, e.stdout, e.stderr)
	}
	if got, _ := readState(statePath()); got.State != stateDiscarded || len(e.h.AllWrites) != 0 {
		t.Fatalf("state %+v writes %v", got, e.h.AllWrites)
	}
	// a discarded state no longer blocks a run
	if got, _ := readState(statePath()); stateOpen(got) {
		t.Fatal("discarded state still blocks the guardian")
	}
}
