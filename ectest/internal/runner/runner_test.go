package runner_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/runner"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sim"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
)

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// The full procedure against the simulator on a virtual clock.
func TestRunnerSimulated(t *testing.T) {
	s := sim.New(t0)
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	before := 0
	r.BeforeWrite = func() error {
		before++
		if s.Writes != 0 || len(s.Log) != 0 {
			t.Error("BeforeWrite after a write")
		}
		return nil
	}
	var streamed []string
	r.OnEvent = func(l string) { streamed = append(streamed, l) }
	ctx := context.Background()
	g3, g4 := r.Gates(ctx)
	if g3.Result != "PASS" || g4.Result != "PASS" {
		t.Fatalf("gates %+v %+v", g3, g4)
	}
	t1 := r.RunT1(ctx)
	if t1.Verdict != spec.Pass {
		t.Fatalf("T1 %+v", t1)
	}
	t2, t3 := r.RunHandback(ctx)
	if t2.Verdict != spec.Pass || t3.Verdict != spec.Pass || len(t2.Runs) != 3 {
		t.Fatalf("T2 %s T3 %s runs %d: %v", t2.Verdict, t3.Verdict, len(t2.Runs), t2.Reasons)
	}
	r.Close()
	if v, _ := s.DCR(); v.Reg(3) != 140 {
		t.Fatalf("pwm3 not restored to its stop duty: %s", v)
	}
	if before != 1 {
		t.Fatalf("BeforeWrite called %d times", before)
	}
	for _, x := range r.Samples {
		if x.Written >= 0 && x.Written < spec.MinDuty {
			t.Fatalf("duty %d written", x.Written)
		}
	}
	ev := strings.Join(r.Events, "\n")
	for _, want := range []string{"[t_s=300.000] T1 pwm3: rest reference", runner.EventT1Complete, runner.EventT2Auto(1, "H"), runner.EventT2Auto(3, "M"), runner.EventT2Complete, "restore pwm3 after T2 ok"} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q", want)
		}
	}
	if len(streamed) != len(r.Events) {
		t.Fatalf("streamed %d of %d events", len(streamed), len(r.Events))
	}
	// PCIe has no EC automatic (TESTSPEC §6): T2/T3 N/A, nothing written
	s2 := sim.New(t0)
	r2 := runner.NewRunner(s2, runner.SpecConfig(4, 0, []int{1, 2, 3}))
	t2p, t3p := r2.RunHandback(ctx)
	r2.Close()
	if t2p.Verdict != spec.NotApplicable || t3p.Verdict != spec.NotApplicable || t2p.Reasons[0] != spec.ReasonNoPCIeAuto || len(s2.Log) != 0 || len(r2.Samples) != 0 {
		t.Fatalf("pcie T2 %s T3 %s %v, writes %v, samples %d", t2p.Verdict, t3p.Verdict, t2p.Reasons, s2.Log, len(r2.Samples))
	}
}

// Without the register dump nothing is written and the verdicts say why.
func TestRunnerNoDCRWritesNothing(t *testing.T) {
	s := sim.New(t0)
	s.NoDCR = true
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	t1 := r.RunT1(context.Background())
	t2, t3 := r.RunHandback(context.Background())
	r.Close()
	if len(s.Log) != 0 || t1.Verdict != spec.Inconclusive || t2.Verdict != spec.Inconclusive || t3.Verdict != spec.Inconclusive {
		t.Fatalf("writes %v, verdicts %s %s %s", s.Log, t1.Verdict, t2.Verdict, t3.Verdict)
	}
	if !strings.Contains(t1.Reasons[0], "nothing was written") {
		t.Fatalf("reason %q", t1.Reasons)
	}
}

// A failing BeforeWrite (guardian state file not written) stops the run
// before anything is written.
func TestRunnerBeforeWriteFails(t *testing.T) {
	s := sim.New(t0)
	r := runner.NewRunner(s, runner.SpecConfig(1, 1, []int{1, 2, 3}))
	r.BeforeWrite = func() error { return errors.New("disk full") }
	res := r.RunT1(context.Background())
	r.Close()
	if len(s.Log) != 0 || res.Verdict != spec.Inconclusive || !strings.Contains(r.Abort, "disk full") {
		t.Fatalf("writes %v, %s, abort %q", s.Log, res.Verdict, r.Abort)
	}
}

// Limits of §4 abort the run and Close restores the channel.
func TestRunnerAbortsOnLimit(t *testing.T) {
	s := sim.New(t0)
	r := runner.NewRunner(s, runner.SpecConfig(1, 1, []int{1, 2, 3}))
	s.Tctl = 86
	t1 := r.RunT1(context.Background())
	if t1.Verdict != spec.Inconclusive || !strings.Contains(r.Abort, "Tctl") {
		t.Fatalf("abort: %q %+v", r.Abort, t1)
	}
	// limit hit in the middle of a step: the HDD limit holds pwm3 at 255
	// (TESTSPEC 0.4 §4, #13), not its stop duty 140
	s = sim.New(t0)
	s.OnSleep = func(n int) {
		if n == 20 {
			s.SetTemps(func(x *sim.Sim) { x.HDD = 51 })
		}
	}
	r = runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	if res := r.RunT1(context.Background()); res.Verdict != spec.Inconclusive || !strings.Contains(r.Abort, "HDD 51.0 C") {
		t.Fatalf("HDD limit: %q %+v", r.Abort, res)
	}
	r.Close()
	if v, _ := s.DCR(); v.Reg(3) != 255 || v.Reg(4) != 255 || len(r.Unrestored()) != 0 || r.HotChannel() != 3 {
		t.Fatalf("not held at 255: %s %v", v, r.Unrestored())
	}
	// a limit that is not a temperature (a lost sensor) restores pwm3 to 140
	s = sim.New(t0)
	s.OnSleep = func(n int) {
		if n == 20 {
			s.SetTemps(func(x *sim.Sim) { x.HDD = math.NaN() })
		}
	}
	r = runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	r.LimitSensors = map[string]bool{"tctl": true, "nvme": true, "hdd": true}
	if res := r.RunT1(context.Background()); res.Verdict != spec.Inconclusive || !strings.Contains(r.Abort, "limit sensor hdd") {
		t.Fatalf("hdd lost: %q %+v", r.Abort, res)
	}
	r.Close()
	if v, _ := s.DCR(); v.Reg(3) != 140 || len(r.RestoreOverrides) != 0 {
		t.Fatalf("lost sensor: %s %v", v, r.RestoreOverrides)
	}
	// cancellation: restore still runs
	s = sim.New(t0)
	r = runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	ctx, cancel := context.WithCancel(context.Background())
	if err := r.ManualForTest(3); err != nil {
		t.Fatal(err)
	}
	cancel()
	if r.RunT1(ctx).Verdict != spec.Inconclusive || !strings.Contains(r.Abort, "cancelled") {
		t.Fatalf("cancel: %q", r.Abort)
	}
	r.Close()
	if v, _ := s.DCR(); v.Reg(3) != 140 {
		t.Fatalf("not restored: %s", v)
	}
	if err := r.WriteForTest(3, 99); err == nil {
		t.Fatal("duty 99 written")
	}
}

// TESTSPEC 0.4 §4 (#13): after an abort on a temperature limit the channel
// of that sensor goes to 255 and stays there, touched by the run or not;
// the other touched channels get their restore value; the reason is in
// RestoreOverrides and BeforeHot runs before the write. A run that never
// wrote writes nothing (reading of n5-fangov validate 0.5.0-rc4).
func TestTemperatureAbortHolds255(t *testing.T) {
	s := sim.New(t0)
	s.OnSleep = func(n int) {
		if n == 20 {
			s.SetTemps(func(x *sim.Sim) { x.NVMe = spec.LimitNVMeC + 1 })
		}
	}
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	var hot []int
	r.BeforeHot = func(ch int) error {
		hot = append(hot, ch)
		if d, _ := s.DCR(); d.Reg(2) == 255 {
			t.Error("BeforeHot after the write")
		}
		return errors.New("state file not writable")
	}
	if res := r.RunT1(context.Background()); res.Verdict != spec.Inconclusive || !strings.Contains(r.Abort, "NVMe 71.0 C") {
		t.Fatalf("NVMe limit: %q %+v", r.Abort, res)
	}
	r.Close()
	r.Close() // idempotent: no second 255 write
	d, _ := s.DCR()
	if d.Reg(2) != 255 || d.Reg(3) != 140 || len(r.Unrestored()) != 0 {
		t.Fatalf("after the NVMe abort DCR2 %d (want 255), DCR3 %d (want the stop 140), unrestored %v", d.Reg(2), d.Reg(3), r.Unrestored())
	}
	if !strings.Contains(r.RestoreOverrides[2], "255 (temperature abort: NVMe 71.0 C") || len(r.RestoreOverrides) != 1 || fmt.Sprint(hot) != "[2]" {
		t.Fatalf("overrides %v, BeforeHot %v", r.RestoreOverrides, hot)
	}
	ev := strings.Join(r.Events, "\n")
	for _, want := range []string{"restore pwm2=255 ok: 255 (temperature abort", "255 is written anyway", "restore pwm3 ok"} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q", want)
		}
	}
	if n := strings.Count(strings.Join(s.Log, " "), "pwm2=255"); n != 1 {
		t.Fatalf("pwm2=255 written %d times: %v", n, s.Log)
	}
	// Tctl over the limit in the gate window: nothing was written, nothing
	// is written now
	s = sim.New(t0)
	s.Tctl = spec.LimitTctlC
	r = runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	r.Gates(context.Background())
	r.Close()
	if len(s.Log) != 0 || len(r.RestoreOverrides) != 0 || r.HotChannel() != 0 {
		t.Fatalf("a run without writes wrote %v", s.Log)
	}
	// a register that does not reach 255 after the settle time is a restore
	// warning, the channel counts as set
	s = sim.New(t0)
	s.WriteLag[1] = time.Hour
	s.OnSleep = func(n int) {
		if n == 20 {
			s.SetTemps(func(x *sim.Sim) { x.Tctl = 90 })
		}
	}
	r = runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	r.RunT1(context.Background())
	r.Close()
	if len(r.RestoreWarnings) != 1 || !strings.Contains(r.RestoreWarnings[0], "pwm1") || len(r.Unrestored()) != 0 || r.RestoreOverrides[1] == "" {
		t.Fatalf("warnings %v unrestored %v", r.RestoreWarnings, r.Unrestored())
	}
}

// T3 FAIL is reachable in a run: a channel that never settles fails all
// three repetitions.
func TestRunnerT3FailReachable(t *testing.T) {
	s := sim.New(t0)
	s.Oscillate[1] = true
	r := runner.NewRunner(s, runner.SpecConfig(1, 1, []int{1, 2, 3}))
	t2, t3 := r.RunHandback(context.Background())
	r.Close()
	if t3.Verdict != spec.Fail {
		t.Fatalf("T3 %s: %v", t3.Verdict, t3.Reasons)
	}
	if t2.Verdict == spec.Fail {
		t.Fatalf("T2 %s: %v", t2.Verdict, t2.Reasons)
	}
}

// G4 blocks when a limit sensor has no value; a sensor lost later aborts.
func TestRunnerLimitSensors(t *testing.T) {
	s := sim.New(t0)
	s.HDD = math.NaN()
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	_, g4 := r.Gates(context.Background())
	if g4.Result != "FAIL" || !strings.Contains(g4.Detail, "limit sensor hdd not available") {
		t.Fatalf("G4 %+v", g4)
	}
	s = sim.New(t0)
	r = runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	if _, g4 := r.Gates(context.Background()); g4.Result != "PASS" {
		t.Fatalf("G4 %+v", g4)
	}
	s.NVMe = math.NaN()
	if res := r.RunT1(context.Background()); res.Verdict != spec.Inconclusive || !strings.Contains(r.Abort, "limit sensor nvme unreadable") {
		t.Fatalf("lost sensor: %q %+v", r.Abort, res)
	}
	r.Close()
	if len(r.Unrestored()) != 0 {
		t.Fatalf("unrestored %v", r.Unrestored())
	}
}

// The guard (G1 during the run) aborts as soon as another writer appears.
func TestRunnerGuardAborts(t *testing.T) {
	s := sim.New(t0)
	cfg := runner.SpecConfig(3, 3, []int{1, 2, 3})
	n := 0
	cfg.Guard = func() string {
		n++
		if n > 20 {
			return "process fancontrol (pid 4242)"
		}
		return ""
	}
	r := runner.NewRunner(s, cfg)
	if res := r.RunT1(context.Background()); res.Verdict != spec.Inconclusive || !strings.Contains(r.Abort, "fancontrol") {
		t.Fatalf("guard: %q %+v", r.Abort, res)
	}
	r.Close()
	if v, _ := s.DCR(); v.Reg(3) != 140 {
		t.Fatalf("not restored: %s", v)
	}
}

// A failed restore stays visible; a written value with a differing
// register is a warning, not a failure.
func TestRunnerRestoreOutcome(t *testing.T) {
	s := sim.New(t0)
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	if err := r.ManualForTest(3); err != nil {
		t.Fatal(err)
	}
	s.RestoreErr[3] = errors.New("EIO")
	r.Close()
	r.Close()
	if u := r.Unrestored(); len(u) != 1 || !strings.Contains(u[0], "pwm3: EIO") {
		t.Fatalf("unrestored %v", u)
	}
	s.RestoreErr[3] = fmt.Errorf("%w: DCR3 reads 87", runner.ErrRestoreUnverified)
	r.Close()
	if len(r.Unrestored()) != 0 || len(r.RestoreWarnings) != 1 || !strings.Contains(strings.Join(r.Events, "\n"), "restore pwm3 WRITTEN, REGISTER DIFFERS") {
		t.Fatalf("unverified: %v %v %v", r.Unrestored(), r.RestoreWarnings, r.Events)
	}
}

// The first sample of the gate window is checked against the limits.
func TestGatesFirstSampleLimit(t *testing.T) {
	s := sim.New(t0)
	s.Tctl = spec.LimitTctlC + 1
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	g3, g4 := r.Gates(context.Background())
	if g3.Result != "PASS" || g4.Result != "FAIL" || !strings.Contains(r.Abort, "Tctl") || len(r.Samples) != 1 {
		t.Fatalf("g3 %+v g4 %+v abort %q samples %d", g3, g4, r.Abort, len(r.Samples))
	}
	r2 := runner.NewRunner(sim.New(t0), runner.SpecConfig(3, 3, []int{1, 2, 3}))
	r2.Stop("boot-state sample over a limit")
	g3, g4 = r2.Gates(context.Background())
	if g3.Result != "FAIL" || g4.Result != "FAIL" || len(r2.Samples) != 0 {
		t.Fatalf("pre-aborted: %+v %+v %d", g3, g4, len(r2.Samples))
	}
}

// wobbleEnv is the simulator with Tctl/EC system values cycled per read.
type wobbleEnv struct {
	*sim.Sim
	n         int
	tctl, sys []float64
}

func (w *wobbleEnv) Read() runner.Readings {
	rd := w.Sim.Read()
	if len(w.tctl) > 0 {
		rd.Tctl = w.tctl[w.n%len(w.tctl)]
	}
	if len(w.sys) > 0 {
		rd.ECSys = w.sys[w.n%len(w.sys)]
	}
	w.n++
	return rd
}

// The runner's gate window (TESTSPEC 0.4 G4, #20): Tctl noise of a
// productive host passes, EC system/ambient and HDD must not move.
func TestGatesTctlSafetyOnly(t *testing.T) {
	r := runner.NewRunner(&wobbleEnv{Sim: sim.New(t0), tctl: []float64{35, 48.5}}, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	if _, g4 := r.Gates(context.Background()); g4.Result != "PASS" || strings.Contains(g4.Detail, "tctl)") {
		t.Fatalf("Tctl 13.5 C: %+v", g4)
	}
	r = runner.NewRunner(&wobbleEnv{Sim: sim.New(t0), sys: []float64{33, 34.5}}, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	if _, g4 := r.Gates(context.Background()); g4.Result != "FAIL" || !strings.Contains(g4.Detail, "system (ec 0x04) 1.5 C") {
		t.Fatalf("system 1.5 C: %+v", g4)
	}
}

// pwm3 with DCR4 stuck after the auto command: T2 INCONCLUSIVE (DCR3/DCR4
// disagree), A per register recorded.
func TestRunnerDCR4Disagree(t *testing.T) {
	s := sim.New(t0)
	s.KeepDCR4OnAuto = true
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	t2, t3 := r.RunHandback(context.Background())
	r.Close()
	if t2.Verdict != spec.Inconclusive || !strings.Contains(strings.Join(t2.Reasons, "\n"), "DCR3/DCR4 disagree") {
		t.Fatalf("T2 %s: %v", t2.Verdict, t2.Reasons)
	}
	if v, _ := t2.Values.(map[string]any); v["A_DCR3"] != 0x57 || v["A_DCR4"] != 0x8c || v["M"] != spec.MValue(0x57) {
		t.Fatalf("values %v", t2.Values)
	}
	if t3.Verdict != spec.Pass {
		t.Fatalf("T3 %s: %v", t3.Verdict, t3.Reasons)
	}
}

func TestParseRestoreArg(t *testing.T) {
	for in, want := range map[string]string{"pwm3=auto": "3 auto", "pwm4=255": "4 255", "pwm1=100": "1 100"} {
		ch, v, err := runner.ParseRestoreArg(in)
		if err != nil || fmt.Sprintf("%d %s", ch, v) != want {
			t.Errorf("%s: %d %s %v", in, ch, v, err)
		}
	}
	for _, bad := range []string{"pwm3=99", "pwm3=0", "pwm5=auto", "3=auto", "pwm3", "pwm3=256", "pwm3=off", "pwm4=auto", "pwm4=200"} {
		if _, _, err := runner.ParseRestoreArg(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestResolveRestore(t *testing.T) {
	dir := t.TempDir()
	none := filepath.Join(dir, "none.toml")
	// no config: default of TESTSPEC 0.4 §4, no deviation
	rv := runner.ResolveRestore(nil, none, []int{1, 2, 3, 4})
	if rv.Values[1] != "auto" || rv.Values[3] != "auto" || rv.Values[4] != "255" || rv.Sources[3] != runner.SourceDefault || len(rv.Deviations) != 0 {
		t.Fatalf("default: %+v", rv)
	}
	// config as n5-fangov reads it
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("[[channel]]\nname = \"cpu\"\npwm = 1\nstop = \"auto\"\n\n[[channel]]\nname = \"hdd\"\npwm = 3\nstop = \"auto\"\n\n[[channel]]\nname = \"pcie\"\npwm = 4\nstop = \"60\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rv = runner.ResolveRestore(map[int]string{2: "180"}, cfg, []int{1, 2, 3, 4})
	if rv.Values[1] != "auto" || rv.Values[2] != "180" || rv.Sources[2] != runner.SourceArg || rv.Values[3] != "140" {
		t.Fatalf("config: %+v", rv)
	}
	// pwm4 is 255 whatever the config says (decision 3, 2026-10-16)
	if rv.Values[4] != "255" || rv.Sources[4] != runner.SourcePCIe {
		t.Fatalf("pwm4 with stop 60: %+v", rv)
	}
	w := strings.Join(rv.Warnings, "|")
	if strings.Contains(w, "pwm4") || !strings.Contains(w, "pwm3 stop \"auto\" is replaced by 140") {
		t.Fatalf("warnings %v", rv.Warnings)
	}
	// --restore is part of TESTSPEC 0.4 §4; a fixed pwm4 stop duty other
	// than 255 stays a deviation
	if len(rv.Deviations) != 1 || rv.Deviations[0] != runner.DeviationPCIeRestore("60") {
		t.Fatalf("deviations %v", rv.Deviations)
	}
	// M2: a config without a pwm = 4 channel gave n5-fangov's built-in
	// "auto", which left DCR5 at the last duty; now 255, which is what the
	// hand-back ("auto") writes from driver 0.2.1+sr1~rc2: no deviation
	cfg2 := filepath.Join(dir, "cpu-only.toml")
	if err := os.WriteFile(cfg2, []byte("[[channel]]\nname = \"cpu\"\npwm = 1\nstop = \"auto\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rv = runner.ResolveRestore(nil, cfg2, []int{4})
	if rv.Values[4] != "255" || rv.Sources[4] != runner.SourcePCIe || len(rv.Deviations) != 0 {
		t.Fatalf("config without pwm4: %+v", rv)
	}
	// a configured 255 is no deviation
	if err := os.WriteFile(cfg2, []byte("[[channel]]\nname = \"pcie\"\npwm = 4\nstop = \"255\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rv = runner.ResolveRestore(nil, cfg2, []int{4}); rv.Values[4] != "255" || len(rv.Deviations) != 0 {
		t.Fatalf("config pwm4 255: %+v", rv)
	}
	// --restore pwm4=255 is the only accepted override
	if rv = runner.ResolveRestore(map[int]string{4: "255"}, cfg2, []int{4}); rv.Values[4] != "255" || rv.Sources[4] != runner.SourceArg {
		t.Fatalf("--restore pwm4=255: %+v", rv)
	}
	// unreadable config: n5-fangov's built-in values
	if err := os.WriteFile(cfg, []byte("[[channel]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rv = runner.ResolveRestore(nil, cfg, []int{3, 4})
	if rv.Values[3] != "140" || rv.Values[4] != "255" || rv.Sources[3] != runner.SourceBuiltin || rv.Sources[4] != runner.SourcePCIe || len(rv.Warnings) != 1 {
		t.Fatalf("broken config: %+v", rv)
	}
}

func TestG2(t *testing.T) {
	proc := t.TempDir()
	mk := func(pid, comm, cmdline string) {
		d := filepath.Join(proc, pid)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline), 0o644)
	}
	mk("1", "systemd", "/sbin/init\x00")
	defer func(f func() (string, error)) { runner.ZpoolStatus = f }(runner.ZpoolStatus)
	runner.ZpoolStatus = func() (string, error) { return "", exec.ErrNotFound }
	if g := runner.G2(proc); g.Result != "PASS" || !strings.Contains(g.Detail, "no zpool") {
		t.Fatalf("idle: %+v", g)
	}
	runner.ZpoolStatus = func() (string, error) { return "  scan: scrub in progress since ...", nil }
	if g := runner.G2(proc); g.Result != "FAIL" || !strings.Contains(g.Detail, "scrub in progress") {
		t.Fatalf("scrub: %+v", g)
	}
	runner.ZpoolStatus = func() (string, error) { return "", errors.New("exit status 1") }
	if g := runner.G2(proc); g.Result != "FAIL" {
		t.Fatalf("zpool failing: %+v", g)
	}
	runner.ZpoolStatus = func() (string, error) { return "all pools are healthy", nil }
	mk("77", "task UPID:n5ho", "task UPID:n5host:0000ABCD:0001:6700:vzdump:101:root@pam:\x00")
	if g := runner.G2(proc); g.Result != "FAIL" || !strings.Contains(g.Detail, "PVE task vzdump (pid 77)") {
		t.Fatalf("pve task: %+v", g)
	}
	if got := runner.BackupProcess("perl", "perl\x00-T\x00/usr/bin/vzdump\x00101\x00"); got != "process vzdump" {
		t.Fatalf("interpreter: %q", got)
	}
	if got := runner.BackupProcess("proxmox-backup-", "/usr/sbin/proxmox-backup-proxy\x00"); got != "" {
		t.Fatalf("PBS daemon: %q", got)
	}
}

// The cadence is fixed (sample k at k*5 s after the auto command), so the
// time the reads take does not add up: with 0.4 s per read a register that
// leaves at 59.5 s is seen in the 12th sample at 60 s (T2 PASS), not at
// 64.4 s (INCONCLUSIVE, as with sleep-then-read).
func TestFixedCadence(t *testing.T) {
	s := sim.New(t0)
	s.ReadLatency = 400 * time.Millisecond
	s.AutoDelay[1] = 59500 * time.Millisecond
	cfg := runner.SpecConfig(1, 1, []int{1, 2, 3})
	cfg.Repeat = 1
	r := runner.NewRunner(s, cfg)
	t2, _ := r.RunHandback(context.Background())
	r.Close()
	if len(t2.Runs) != 1 || len(t2.Runs[0].Subs) != 2 {
		t.Fatalf("runs %+v", t2.Runs)
	}
	for _, sub := range t2.Runs[0].Subs {
		if sub.T2 != spec.Pass || sub.LeftAtS == nil || *sub.LeftAtS != 60 {
			t.Fatalf("sub-run %s: T2 %s, left at %v: %s", sub.Label, sub.T2, sub.LeftAtS, sub.T2Reason)
		}
		if sub.ObservedS != float64(spec.ObserveS+spec.SettleHoldS) {
			t.Fatalf("sub-run %s observed %.3f s", sub.Label, sub.ObservedS)
		}
	}
	// every sample of a sub-run sits on its 5-s slot (to the ms)
	var autoTS float64
	for _, e := range r.Events {
		if strings.Contains(e, runner.EventT2Auto(1, "H")) {
			fmt.Sscanf(e[strings.Index(e, "[t_s=")+5:], "%f", &autoTS)
		}
	}
	k := 0
	for _, x := range r.Samples {
		if x.Phase != spec.PhaseT2Auto(1, "H") {
			continue
		}
		k++
		if got := spec.SinceMS(x.TS, autoTS); got != float64(5*k) {
			t.Fatalf("sample %d at %.3f s after the auto command", k, got)
		}
	}
}

// A sample that overruns its slot skips the slots it missed instead of
// sampling in a burst, and the hold still ends at its full length.
func TestOverrunSkipsSlots(t *testing.T) {
	s := sim.New(t0)
	cfg := runner.SpecConfig(1, 1, []int{1, 2, 3})
	r := runner.NewRunner(s, cfg)
	n := 0
	s.OnSleep = func(int) {
		n++
		if n == 3 {
			s.ReadLatency = 12 * time.Second // one slow read
		} else {
			s.ReadLatency = 0
		}
	}
	_, g4 := r.Gates(context.Background())
	if g4.Result == "" {
		t.Fatal("no G4")
	}
	prev := -1.0
	for _, x := range r.Samples {
		if x.Phase != spec.PhaseGate {
			continue
		}
		if prev >= 0 && x.TS-prev < 4.999 {
			t.Fatalf("burst: samples at %.3f and %.3f", prev, x.TS)
		}
		prev = x.TS
	}
	if last := r.Samples[len(r.Samples)-1].TS; last < spec.GateWindowS {
		t.Fatalf("gate window ended at %.3f s", last)
	}
}
