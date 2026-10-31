package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/logger"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/runner"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/writerlock"
)

// Tests of the audit of 2026-10-16 (audit-20261016/BACKLOG.md #5, #13,
// #14, #16).

// #5 (M2, decision 3): map pwm4 with an n5-fangov config that has no
// pwm = 4 channel ends with pwm4 at 255, never with the EC automatic
// (DCR5 has none); "auto" means 255 from driver 0.2.1+sr1~rc2, so no
// deviation (TESTSPEC 0.4 §4).
func TestMapPWM4RestoresTo255(t *testing.T) {
	e := newEnv(t, fake.Reference())
	e.h.Put("etc/n5-fangov/config.toml", "[[channel]]\nname = \"cpu\"\npwm = 1\nstop = \"auto\"\n")
	rc := e.hw("map", "pwm4", "--boot", "cold")
	w := e.pwmWrites()
	if strings.Contains(w, "pwm4_enable=2") {
		t.Fatalf("pwm4 put into the EC automatic: rc %d writes %q\n%s", rc, w, e.stdout)
	}
	if !strings.HasSuffix(w, "pwm4=255") {
		t.Fatalf("last write is not pwm4=255: rc %d writes %q\n%s%s", rc, w, e.stdout, e.stderr)
	}
	m := readMeta(t, e.bundle())
	if rv := m["restore_values"].(map[string]any); rv["4"] != "255" {
		t.Fatalf("restore values %v", rv)
	}
	if src := m["restore_value_sources"].(map[string]any)["4"]; src != runner.SourcePCIe {
		t.Fatalf("restore source %v", src)
	}
	for _, d := range m["spec_deviations"].([]any) {
		if strings.Contains(d.(string), "restore value of pwm4") {
			t.Fatalf("spec_deviations name pwm4 \"auto\": %v", m["spec_deviations"])
		}
	}
}

// #5: a state file of an older version with pwm4 "auto" is restored to 255.
func TestRestoreOldStatePWM4Auto(t *testing.T) {
	e := newEnv(t, fake.Reference())
	st := &stateFile{Tool: toolName, PID: 3, State: stateActive, Device: mustResolve(t), Channels: map[string]string{"4": "auto"}}
	if err := st.write(statePath()); err != nil {
		t.Fatal(err)
	}
	if rc := cmdRestore(nil); rc != exitOK {
		t.Fatalf("rc %d %s %s", rc, e.stdout, e.stderr)
	}
	if got := e.pwmWrites(); got != "pwm4_enable=1 pwm4=255" {
		t.Fatalf("writes %q", got)
	}
	if !strings.Contains(e.stdout.String(), `state file value "auto" replaced by 255`) {
		t.Fatalf("stdout:\n%s", e.stdout)
	}
}

// #13 (L1): with the default --out "." the state file holds the absolute
// bundle path, so `n5-ectest restore` from another directory still logs
// into the bundle.
func TestStateBundlePathAbsolute(t *testing.T) {
	e := newEnv(t, fake.Reference())
	t.Chdir(e.out)
	lock, err := writerlock.Acquire(sysroot.Run(), "test guardian")
	if err != nil {
		t.Fatal(err)
	}
	o, rc := parseRunOpts("map", []string{"pwm3", "--boot", "cold"})
	if rc != exitOK || o.out != "." {
		t.Fatalf("opts rc %d out %q", rc, o.out)
	}
	if rc := runValidation(o, lock, statePath()); rc != exitOK {
		t.Fatalf("rc %d\n%s\n%s", rc, e.stdout, e.stderr)
	}
	lock.Release()
	st, err := readState(statePath())
	if err != nil || !filepath.IsAbs(st.Bundle) {
		t.Fatalf("state %+v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(st.Bundle, "events.log")); err != nil {
		t.Fatal(err)
	}
	// the run "left" the channel: restore by hand from another directory
	st.State = stateActive
	if err := st.write(statePath()); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if rc := cmdRestore(nil); rc != exitOK {
		t.Fatalf("restore rc %d %s", rc, e.stderr)
	}
	ev, _ := os.ReadFile(filepath.Join(st.Bundle, "events.log"))
	if !strings.Contains(string(ev), "restore by hand from") {
		t.Fatalf("events.log lacks the restore lines:\n%s", ev)
	}
	if strings.Contains(e.stderr.String(), "not recorded in the bundle") {
		t.Fatalf("stderr: %s", e.stderr)
	}
}

// #13: a bundle without events.log is reported on stderr, not skipped
// silently.
func TestAppendEventsMissingBundle(t *testing.T) {
	e := newEnv(t, fake.Reference())
	appendEvents(filepath.Join(e.out, "gone"), []string{"restore pwm3=auto ok"})
	if !strings.Contains(e.stderr.String(), "events.log not found") || !strings.Contains(e.stderr.String(), "not recorded in the bundle") {
		t.Fatalf("stderr: %q", e.stderr)
	}
}

// #14 (L2) and #16: log never resolves --run-id against the working
// directory, refuses . and .., and refuses a --mail-to that starts with '-'.
func TestLogRunIDAndMailTo(t *testing.T) {
	e := newEnv(t, fake.Reference())
	oldF, oldJ, oldFree := logFangov, logJournal, logFree
	defer func() { logFangov, logJournal, logFree = oldF, oldJ, oldFree }()
	logFangov = func() (string, int, error) { return "inactive", 0, nil }
	logJournal = func(string, string, time.Time) ([]logger.JLine, string, error) { return nil, "c", nil }
	logFree = func(string) (uint64, error) { return 50 << 30, nil }
	out, cwd := t.TempDir(), t.TempDir()
	victim := filepath.Join(cwd, "victim")
	if err := os.Mkdir(victim, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	if rc := cmdLog([]string{"--duration", "2h", "--interval", "30s", "--run-id", "victim", "--out", out}); rc != exitOK {
		t.Fatalf("rc %d %s", rc, e.stderr)
	}
	if ents, _ := os.ReadDir(victim); len(ents) != 0 {
		t.Fatalf("log wrote into the cwd directory named like the run id: %v", ents)
	}
	ents, _ := os.ReadDir(out)
	if len(ents) == 0 || !strings.HasSuffix(ents[0].Name(), "-log-victim") {
		t.Fatalf("run not below --out: %v", ents)
	}
	for _, id := range []string{".", ".."} {
		e.stderr.Reset()
		if rc := cmdLog([]string{"--duration", "2h", "--run-id", id, "--out", out}); rc != exitUsage || !strings.Contains(e.stderr.String(), "not . or ..") {
			t.Errorf("--run-id %s: rc %d %s", id, rc, e.stderr)
		}
	}
	for _, args := range [][]string{
		{"--duration", "2h", "--run-id", "m", "--mail-to", "-oQ/tmp/x", "--out", out},
		{"--duration", "2h", "--run-id", "m", "--mail-to=-froot", "--out", out},
		{"check", "victim", "--out", out, "--mail-to", "-x"},
	} {
		e.stderr.Reset()
		if rc := cmdLog(args); rc != exitUsage || !strings.Contains(e.stderr.String(), "--mail-to must not start with '-'") {
			t.Errorf("%v: rc %d %s", args, rc, e.stderr)
		}
	}
	if ents2, _ := os.ReadDir(out); len(ents2) != len(ents) {
		t.Fatalf("a refused run created files: %v", ents2)
	}
	if err := logger.CommandMailer([]string{"/bin/true"}, "-x")("s", "b"); err == nil || !strings.Contains(err.Error(), "starts with '-'") {
		t.Fatalf("CommandMailer: %v", err)
	}
}

// #15: appendEvents never follows a symlinked bundle directory (a parent
// writable by others could swap the directory for a link elsewhere).
func TestAppendEventsSymlinkedBundleDir(t *testing.T) {
	e := newEnv(t, fake.Reference())
	real := filepath.Join(e.out, "real")
	if err := os.Mkdir(real, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "events.log"), []byte("x\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.out, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	appendEvents(link, []string{"restore pwm3=auto ok"})
	if b, _ := os.ReadFile(filepath.Join(real, "events.log")); string(b) != "x\n" {
		t.Fatalf("appended through a symlinked directory: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(real, "SHA256SUMS")); err == nil {
		t.Fatal("SHA256SUMS written through a symlinked directory")
	}
	if !strings.Contains(e.stderr.String(), "not recorded in the bundle") {
		t.Fatalf("stderr: %q", e.stderr)
	}
	// the real directory works
	appendEvents(real, []string{"restore pwm3=auto ok"})
	if b, _ := os.ReadFile(filepath.Join(real, "events.log")); !strings.Contains(string(b), "restore pwm3=auto ok") {
		t.Fatalf("events.log: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(real, "SHA256SUMS")); err != nil {
		t.Fatal(err)
	}
}

// #16: the worker takes only the state file path of the guardian, and no
// state file that still records an unrestored run.
func TestWorkerStatePathExact(t *testing.T) {
	newEnv(t, fake.Reference())
	sp := statePath()
	for _, bad := range []string{
		"",
		filepath.Join(t.TempDir(), "restore.json"),
		filepath.Dir(sp) + "/../n5-ectest/restore.json",
		filepath.Dir(sp) + "//restore.json",
		sp + "/",
		"x" + sp,
	} {
		if err := checkWorkerState(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := checkWorkerState(sp); err != nil {
		t.Fatalf("no state file: %v", err)
	}
	st := &stateFile{Tool: toolName, PID: 4242, State: stateActive, Channels: map[string]string{"3": "140"}}
	if err := st.write(sp); err != nil {
		t.Fatal(err)
	}
	if err := checkWorkerState(sp); err == nil || !strings.Contains(err.Error(), "unrestored") {
		t.Fatalf("open state file: %v", err)
	}
	st.State = stateRestored
	if err := st.write(sp); err != nil {
		t.Fatal(err)
	}
	if err := checkWorkerState(sp); err != nil {
		t.Fatalf("restored state file: %v", err)
	}
	if err := os.WriteFile(sp, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkWorkerState(sp); err == nil {
		t.Fatal("unreadable state file accepted")
	}
}

// #15: an events.log swapped between Lstat and OpenFile (a new regular file,
// or a symlink to another file of the bundle) is not written.
func TestAppendEventsSwappedBetweenLstatAndOpen(t *testing.T) {
	e := newEnv(t, fake.Reference())
	old := appendEventsSwap
	t.Cleanup(func() { appendEventsSwap = old })
	for _, c := range []struct {
		name string
		swap func(dir string) error
	}{
		{"new file", func(dir string) error {
			p := filepath.Join(dir, "events.log")
			if err := os.Rename(p, p+".old"); err != nil {
				return err
			}
			return os.WriteFile(p, []byte("x\n"), 0o640)
		}},
		{"symlink", func(dir string) error {
			p := filepath.Join(dir, "events.log")
			if err := os.Rename(p, p+".old"); err != nil {
				return err
			}
			return os.Symlink("meta.json", p)
		}},
	} {
		dir := filepath.Join(e.out, strings.ReplaceAll(c.name, " ", "-"))
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"events.log", "meta.json"} {
			if err := os.WriteFile(filepath.Join(dir, n), []byte("x\n"), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		e.stderr.Reset()
		appendEventsSwap = func() {
			if err := c.swap(dir); err != nil {
				t.Fatal(err)
			}
		}
		appendEvents(dir, []string{"restore pwm3=auto ok"})
		for _, n := range []string{"events.log", "events.log.old", "meta.json"} {
			if b, _ := os.ReadFile(filepath.Join(dir, n)); string(b) != "x\n" {
				t.Errorf("%s: %s written: %q", c.name, n, b)
			}
		}
		if _, err := os.Lstat(filepath.Join(dir, "SHA256SUMS")); err == nil {
			t.Errorf("%s: SHA256SUMS written", c.name)
		}
		if !strings.Contains(e.stderr.String(), "changed while opened") {
			t.Errorf("%s: stderr %q", c.name, e.stderr)
		}
	}
}
