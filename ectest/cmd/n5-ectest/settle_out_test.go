package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
)

// lagged makes the fake EC take writes over late on the run's virtual clock
// (MW1 2026-10-17: Z1 PCIe ~0.2 s, Z2 HDD ramp DCR3 ~1.5 s, DCR4 ~4.6 s; DCR4
// here 6 s, beyond one sample interval).
func lagged(e *env) {
	e.h.Lag[3], e.h.Lag[4], e.h.Lag[5] = 1500*time.Millisecond, 6*time.Second, 200*time.Millisecond
	e.h.Clock = e.clock.Now
	e.clock.hook = func(int, context.Context) bool { e.h.Advance(); return true }
}

func restoreWarnings(t *testing.T, dir string) []any {
	t.Helper()
	w, _ := readMeta(t, dir)["restore_warnings"].([]any)
	return w
}

// map pwm3 with the HDD ramp: T1 judges only after the settle time (the
// +5 s sample is listed as not judged), and the restore read-back
// (pwm3=140) compares after the settle time, not right after the write.
func TestMapPWM3SettlesHDDRamp(t *testing.T) {
	e := newEnv(t, fake.Reference())
	lagged(e)
	if rc := e.hw("map", "pwm3", "--boot", "cold", "--restore", "pwm3=140"); rc != exitOK {
		t.Fatalf("rc %d\n%s\n%s", rc, e.stdout, e.stderr)
	}
	dir := e.bundle()
	if v := readVerdict(t, dir); v["T1"].Verdict != spec.Pass {
		t.Fatalf("T1 %s %v", v["T1"].Verdict, v["T1"].Reasons)
	}
	if w := restoreWarnings(t, dir); len(w) != 0 {
		t.Fatalf("restore read back too early: %v", w)
	}
	ev, err := os.ReadFile(filepath.Join(dir, bundle.Events))
	if err != nil {
		t.Fatal(err)
	}
	vb, err := os.ReadFile(filepath.Join(dir, bundle.Verdict))
	if err != nil || !strings.Contains(string(vb), `"not_judged"`) || !strings.Contains(string(vb), "+5.0 s: DCR3 217, DCR4 255") {
		t.Fatalf("verdict.json lacks the samples not judged: %v\n%s", err, vb)
	}
	for _, want := range []string{"restore pwm3 after T1 ok"} {
		if !strings.Contains(string(ev), want) {
			t.Errorf("events lack %q", want)
		}
	}
	if e.h.DCR(3) != 140 || e.h.DCR(4) != 140 {
		t.Fatalf("DCR3/4 %d/%d after the restore", e.h.DCR(3), e.h.DCR(4))
	}
}

// map pwm4 (Z1): the restore to 255 is read back after >= 1 s, not at the
// moment of the write.
func TestMapPWM4SettlesPCIe(t *testing.T) {
	e := newEnv(t, fake.Reference())
	lagged(e)
	rc := e.hw("map", "pwm4", "--boot", "cold")
	dir := e.bundle()
	if w := restoreWarnings(t, dir); len(w) != 0 {
		t.Fatalf("rc %d, restore read back too early: %v", rc, w)
	}
	if v := readVerdict(t, dir); v["T1"].Verdict != spec.Pass {
		t.Fatalf("T1 %s %v", v["T1"].Verdict, v["T1"].Reasons)
	}
	if e.h.DCR(5) != 255 {
		t.Fatalf("DCR5 %d after the restore", e.h.DCR(5))
	}
}

// A missing --out directory is created before the gates; the bundle lands
// in it (n5-fangov lost a bundle that way, MW1 2026-10-17).
func TestOutDirCreatedBeforeGates(t *testing.T) {
	e := newEnv(t, fake.Reference())
	e.out = filepath.Join(t.TempDir(), "not", "there")
	if rc := e.hw("map", "pwm1", "--boot", "cold"); rc != exitOK {
		t.Fatalf("rc %d\n%s\n%s", rc, e.stdout, e.stderr)
	}
	if v := readVerdict(t, e.bundle()); v["T1"].Verdict != spec.Pass {
		t.Fatalf("T1 %s %v", v["T1"].Verdict, v["T1"].Reasons)
	}
	ents, _ := os.ReadDir(e.out)
	for _, x := range ents {
		if strings.HasPrefix(x.Name(), ".n5-ectest-probe") {
			t.Fatalf("probe file left: %s", x.Name())
		}
	}
}

// An --out that cannot be a directory is refused before anything else:
// no gate, no confirmation, no write.
func TestOutDirUnusableRefused(t *testing.T) {
	e := newEnv(t, fake.Reference())
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.out = filepath.Join(f, "sub")
	if rc := e.hw("map", "pwm3", "--boot", "cold"); rc != exitFail {
		t.Fatalf("rc %d", rc)
	}
	if len(e.h.AllWrites) != 0 || strings.Contains(e.stdout.String(), "gate") || strings.Contains(e.stdout.String(), confirmText) {
		t.Fatalf("ran past the --out check: writes %v\n%s", e.h.AllWrites, e.stdout)
	}
	if !strings.Contains(e.stderr.String(), "--out") || !strings.Contains(e.stderr.String(), "nothing written") {
		t.Fatalf("stderr %s", e.stderr)
	}
	// snapshot: same check
	e.stderr.Reset()
	if rc := cmdSnapshot([]string{"x", "--out", filepath.Join(f, "sub")}); rc != exitFail || !strings.Contains(e.stderr.String(), "--out") {
		t.Fatalf("snapshot rc %d: %s", rc, e.stderr)
	}
}
