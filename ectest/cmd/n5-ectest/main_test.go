package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/regulator"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/report"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/session"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/writerlock"
)

// The test binary doubles as n5-ectest for the guardian tests:
// N5ECTEST_TESTMAIN=1 runs it as the program (guardian or worker) with the
// test stubs; =dying-worker writes a guardian state file and kills itself.
func TestMain(m *testing.M) {
	mode := os.Getenv("N5ECTEST_TESTMAIN")
	if os.Getenv(envWorker) == "1" && mode != "1" && mode != "dying-worker" {
		// a re-executed test binary must never run the test suite again
		os.Exit(97)
	}
	switch mode {
	case "1":
		installProcessStubs()
		if os.Getenv(envWorker) == "1" {
			os.Exit(workerMain(os.Args[1:]))
		}
		os.Exit(run(os.Args[1:]))
	case "dying-worker":
		// the worker of TestGuardianRestoresAfterSIGKILL: adopt the lock,
		// write the state file as before a first write, die without restoring
		if _, err := writerlock.Adopt(os.NewFile(workerLockFD, "lock"), sysroot.Run()); err != nil {
			os.Exit(90)
		}
		dev, _, err := pwmw.Resolve(sysroot.Sys())
		if err != nil {
			os.Exit(91)
		}
		st := &stateFile{Tool: toolName, PID: os.Getpid(), State: stateActive, Device: dev, Channels: map[string]string{"3": "140"}, Bundle: os.Getenv("N5ECTEST_TEST_BUNDLE")}
		if err := st.write(os.Getenv(envState)); err != nil {
			os.Exit(92)
		}
		w := &pwmw.Writer{SysRoot: sysroot.Sys(), Want: dev}
		_ = w.EnterManual(3)
		_ = w.SetDuty(3, 217)
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	}
	os.Exit(m.Run())
}

// installProcessStubs are the stubs of a re-executed test binary.
func installProcessStubs() {
	regulator.Systemctl = func(string) (string, error) { return "inactive", nil }
	detectSession = func() session.Session { return session.Session{Stdin: "tty", Console: true} }
	dpkgQuery = func(string) string { return "0.2.1-sr1~rc1" }
	killAt, _ := strconv.Atoi(os.Getenv("N5ECTEST_TEST_KILL_AT"))
	// N5ECTEST_TEST_SIG_AT=n with N5ECTEST_TEST_SIG=QUIT|TSTP: the worker
	// sends the signal to its whole process group at sample n, as the
	// terminal does for Ctrl-\ and Ctrl-Z
	sigAt, _ := strconv.Atoi(os.Getenv("N5ECTEST_TEST_SIG_AT"))
	sig := map[string]syscall.Signal{"QUIT": syscall.SIGQUIT, "TSTP": syscall.SIGTSTP}[os.Getenv("N5ECTEST_TEST_SIG")]
	fc := &fakeClock{t: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	fc.hook = func(n int, ctx context.Context) bool {
		if killAt > 0 && n == killAt && os.Getenv(envWorker) == "1" {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			time.Sleep(time.Second)
		}
		if sigAt > 0 && n == sigAt && sig != 0 && os.Getenv(envWorker) == "1" {
			_ = syscall.Kill(-syscall.Getpgrp(), sig)
			time.Sleep(200 * time.Millisecond)
		}
		return true
	}
	now, sleepCtx = fc.Now, fc.Sleep
	workerCommand = func(args []string) (*exec.Cmd, error) { return exec.Command(os.Args[0], args...), nil }
}

type sessionT = session.Session

// fakeClock is the virtual clock of hardware-path tests.
type fakeClock struct {
	mu   sync.Mutex
	t    time.Time
	n    int
	hook func(n int, ctx context.Context) bool
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.n++
	n, h := c.n, c.hook
	c.mu.Unlock()
	if h != nil && !h(n, ctx) {
		return false
	}
	return ctx.Err() == nil
}

// env is one in-process test environment: fake host, stubs, output.
type env struct {
	t      *testing.T
	h      *fake.Host
	out    string
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	clock  *fakeClock
	units  map[string]string
	sess   session.Session
	writes []string // every pwm write, checked against the rules
	badW   []string
}

func newEnv(t *testing.T, o fake.Options) *env {
	e := &env{t: t, h: fake.New(t, o), out: t.TempDir(), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		clock: &fakeClock{t: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}, units: map[string]string{},
		sess: session.Session{Stdin: "tty", Console: true}}
	e.h.Install()
	apply := pwmw.Recorder
	pwmw.Recorder = func(p, v string) {
		e.writes = append(e.writes, p+"="+v)
		n, _ := strconv.Atoi(v)
		base := filepath.Base(p)
		switch {
		case strings.HasSuffix(base, "_enable"):
			if n != 1 && n != 2 {
				e.badW = append(e.badW, base+"="+v)
			}
		case n < 100:
			e.badW = append(e.badW, base+"="+v)
		}
		apply(p, v)
	}
	oldOut, oldErr, oldIn, oldSess, oldNow, oldSleep, oldDpkg, oldSys := stdout, stderr, stdin, detectSession, now, sleepCtx, dpkgQuery, regulator.Systemctl
	stdout, stderr = e.stdout, e.stderr
	stdin = strings.NewReader(confirmText + "\n")
	detectSession = func() session.Session { return e.sess }
	now, sleepCtx = e.clock.Now, e.clock.Sleep
	dpkgQuery = func(string) string { return "0.2.1-sr1~rc1" }
	regulator.Systemctl = func(u string) (string, error) {
		if s, ok := e.units[u]; ok {
			return s, nil
		}
		return "inactive", nil
	}
	t.Cleanup(func() {
		stdout, stderr, stdin, detectSession, now, sleepCtx, dpkgQuery, regulator.Systemctl = oldOut, oldErr, oldIn, oldSess, oldNow, oldSleep, oldDpkg, oldSys
		if len(e.badW) > 0 {
			t.Errorf("writes outside the rules (duty < 100 or enable not 1/2): %v", e.badW)
		}
		for _, w := range e.writes {
			if !strings.Contains(w, fake.SysDevice) {
				t.Errorf("write outside the EC driver's device: %s", w)
			}
		}
	})
	return e
}

// hw runs map/handback in-process on the hardware path (worker side, with
// a writer lock as the guardian would hand it down).
func (e *env) hw(test string, args ...string) int {
	e.t.Helper()
	lock, err := writerlock.Acquire(sysroot.Run(), "test guardian")
	if err != nil {
		e.t.Fatal(err)
	}
	defer lock.Release()
	o, rc := parseRunOpts(test, append(args, "--out", e.out))
	if rc != exitOK {
		return rc
	}
	return runValidation(o, lock, statePath())
}

func (e *env) bundles() []string {
	ents, _ := os.ReadDir(e.out)
	var out []string
	for _, x := range ents {
		if x.IsDir() {
			out = append(out, filepath.Join(e.out, x.Name()))
		}
	}
	return out
}

func (e *env) bundle() string {
	e.t.Helper()
	b := e.bundles()
	if len(b) != 1 {
		e.t.Fatalf("want one bundle, have %v\n%s", b, e.stdout)
	}
	return b[0]
}

func (e *env) pwmWrites() string { return strings.Join(e.h.Writes, " ") }

func readVerdict(t *testing.T, dir string) map[string]spec.Result {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, bundle.Verdict))
	if err != nil {
		t.Fatal(err)
	}
	var v report.StoredVerdict
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out := map[string]spec.Result{}
	for _, r := range v.Tests {
		out[r.ID] = r
	}
	return out
}

func readMeta(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, bundle.Meta))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// recomputeEqual checks that the report recomputes every verdict of the
// bundle and that its checksums hold.
func recomputeEqual(t *testing.T, dir string) *report.Result {
	t.Helper()
	l, err := bundle.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.SumProblems) != 0 {
		t.Fatalf("checksums: %v", l.SumProblems)
	}
	r, err := report.Recompute(l)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Mismatches) != 0 {
		t.Fatalf("recomputed verdicts differ: %+v\nnotes %v", r.Mismatches, r.Notes)
	}
	return r
}
