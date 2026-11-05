package runner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/runner"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sim"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// vclock is a virtual clock for WaitDCR.
type vclock struct{ t time.Time }

func (c *vclock) now() time.Time { return c.t }
func (c *vclock) sleep(_ context.Context, d time.Duration) bool {
	c.t = c.t.Add(d)
	return true
}

// dcrAt returns a register dump whose DCRn follow f(elapsed).
func dcrAt(c *vclock, start time.Time, f func(el time.Duration) map[int]int) func() (n5driver.DCR, error) {
	return func() (n5driver.DCR, error) {
		var d n5driver.DCR
		for n, v := range f(c.t.Sub(start)) {
			d[2+n] = byte(v)
		}
		return d, nil
	}
}

// MW1 Z1: PCIe shows the old value at the moment of the write; the restore
// read-back waits the settle time (1 s) and then sees the target.
func TestWaitDCRPCIeMinimum(t *testing.T) {
	c := &vclock{t: t0}
	reads := 0
	read := dcrAt(c, t0, func(el time.Duration) map[int]int {
		reads++
		if el < 200*time.Millisecond {
			return map[int]int{5: 0xa7}
		}
		return map[int]int{5: 0xff}
	})
	st := runner.SettleTime(4)
	s := runner.WaitDCR(context.Background(), c.now, c.sleep, read, t0, []int{5}, 0xff, st, st)
	if !s.Reached || s.After != time.Second || reads != 1 {
		t.Fatalf("pcie: %+v after %s, %d reads", s, s.After, reads)
	}
}

// MW1 Z2: the HDD group ramps DCR3, then DCR4; the read-back at the settle
// time (10 s) sees both.
func TestWaitDCRHDDRamp(t *testing.T) {
	if runner.SettleTime(3) != 10*time.Second || runner.SettleTime(1) != time.Second || runner.SettleTime(2) != time.Second || runner.SettleTime(4) != time.Second {
		t.Fatalf("settle times %s %s %s %s", runner.SettleTime(1), runner.SettleTime(2), runner.SettleTime(3), runner.SettleTime(4))
	}
	c := &vclock{t: t0}
	read := dcrAt(c, t0, func(el time.Duration) map[int]int {
		d3, d4 := 0x57, 0x57
		if el >= 1500*time.Millisecond {
			d3 = 140
		}
		if el >= 4600*time.Millisecond {
			d4 = 140
		}
		return map[int]int{3: d3, 4: d4}
	})
	st := runner.SettleTime(3)
	s := runner.WaitDCR(context.Background(), c.now, c.sleep, read, t0, []int{3, 4}, 140, st, st)
	if !s.Reached || s.After != st {
		t.Fatalf("hdd: %+v", s)
	}
	if got := s.Text([]int{3, 4}); got != "DCR3=140 DCR4=140 after 10.0 s" {
		t.Fatalf("text %q", got)
	}
	// never reached: the reading at the settle time
	c = &vclock{t: t0}
	read = dcrAt(c, t0, func(time.Duration) map[int]int { return map[int]int{3: 140, 4: 0x57} })
	s = runner.WaitDCR(context.Background(), c.now, c.sleep, read, t0, []int{3, 4}, 140, st, st)
	if s.Reached || s.After != st || s.DCR.Reg(4) != 0x57 {
		t.Fatalf("not reached: %+v", s)
	}
	// registers not readable: no further wait after the settle time
	c = &vclock{t: t0}
	s = runner.WaitDCR(context.Background(), c.now, c.sleep, func() (n5driver.DCR, error) { return n5driver.DCR{}, errors.New("no dcr") }, t0, []int{3, 4}, 140, 0, st)
	if s.Err == nil || c.t != t0 {
		t.Fatalf("unreadable: %+v, waited %s", s, c.t.Sub(t0))
	}
}

// T1 on pwm3 with the HDD ramp of Z2, DCR4 slower than one sample interval:
// the sample at +5 s shows the old value and is listed, not judged (TESTSPEC
// 0.4 §2.1); from +10 s every sample is judged; the step is exactly 60 s.
func TestRunT1SettlesHDDRamp(t *testing.T) {
	s := sim.New(t0)
	s.WriteLag[3], s.WriteLag[4] = 1500*time.Millisecond, 6*time.Second
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	res := r.RunT1(context.Background())
	r.Close()
	if res.Verdict != spec.Pass {
		t.Fatalf("T1 %s %v", res.Verdict, res.Reasons)
	}
	steps := res.Values.([]spec.T1Step)
	if len(steps) != 4 || len(steps[1].NotJudged) != 1 || !strings.HasPrefix(steps[1].NotJudged[0], "+5.0 s: DCR3 217, DCR4 255") {
		t.Fatalf("not judged: %+v", steps)
	}
	for _, st := range steps {
		if len(st.T) != 12 || st.T[0] != 5 || st.T[11] != 60 {
			t.Fatalf("step %d timing %v", st.Duty, st.T)
		}
	}
	if strings.Contains(strings.Join(r.Events, "\n"), "settle pwm3") {
		t.Fatal("pre-hold settle wait still in place")
	}
}

// T1 on pwm4 (Z1): settle time 1 s, every sample of a step is judged.
func TestRunT1SettlesPCIe(t *testing.T) {
	s := sim.New(t0)
	s.WriteLag[5] = 200 * time.Millisecond
	r := runner.NewRunner(s, runner.SpecConfig(4, 0, []int{1, 2, 3}))
	res := r.RunT1(context.Background())
	r.Close()
	if res.Verdict != spec.Pass {
		t.Fatalf("T1 %s %v", res.Verdict, res.Reasons)
	}
	for _, st := range res.Values.([]spec.T1Step) {
		if len(st.NotJudged) != 0 {
			t.Fatalf("step %d not judged %v", st.Duty, st.NotJudged)
		}
	}
}

// A register that has not reached the value at +10 s is a judged
// difference: T1 FAIL, no abort.
func TestRunT1SettleTimeout(t *testing.T) {
	s := sim.New(t0)
	s.WriteLag[4] = 20 * time.Second
	r := runner.NewRunner(s, runner.SpecConfig(3, 3, []int{1, 2, 3}))
	res := r.RunT1(context.Background())
	r.Close()
	if r.Abort != "" || res.Verdict != spec.Fail {
		t.Fatalf("abort %q T1 %s %v", r.Abort, res.Verdict, res.Reasons)
	}
}

// The hold of a T2 sub-run lists samples after the settle time that differ
// from the written value (TESTSPEC 0.4 §5.2); they do not change T2/T3.
func TestHandbackHoldDeviations(t *testing.T) {
	s := sim.New(t0)
	s.WriteLag[3], s.WriteLag[4] = 1500*time.Millisecond, 12*time.Second
	cfg := runner.SpecConfig(3, 3, []int{1, 2, 3})
	cfg.Repeat = 1
	r := runner.NewRunner(s, cfg)
	t2, _ := r.RunHandback(context.Background())
	r.Close()
	v := t2.Values.(map[string]any)
	dev := v["hold_deviations"].([]string)
	if len(dev) != 2 || !strings.HasPrefix(dev[0], "run 1 H hold +10.0 s: written 255, DCR4") || !strings.HasPrefix(dev[1], "run 1 M hold +10.0 s") {
		t.Fatalf("hold deviations %v", dev)
	}
}

// A table input that crosses a step in a sub-run makes it INCONCLUSIVE
// (TESTSPEC 0.4 §5.2, #17).
func TestHandbackTableInputCrossing(t *testing.T) {
	s := sim.New(t0)
	cfg := runner.SpecConfig(1, 1, []int{1, 2, 3})
	cfg.Repeat = 1
	n := 0
	s.OnSleep = func(int) {
		n++
		if n == 200 { // in the first sub-run, after the A window (180 samples)
			s.SetTemps(func(x *sim.Sim) { x.ECCPU = 61 })
		}
	}
	r := runner.NewRunner(s, cfg)
	t2, t3 := r.RunHandback(context.Background())
	r.Close()
	if t2.Verdict != spec.Inconclusive || t3.Verdict != spec.Inconclusive {
		t.Fatalf("T2 %s T3 %s", t2.Verdict, t3.Verdict)
	}
	if !strings.Contains(strings.Join(t2.Reasons, "\n"), "table input crossed a step (EC 0x09 (CPU) 37.0 C -> 61.0 C across the step at 45 C)") {
		t.Fatalf("reasons %v", t2.Reasons)
	}
	// in the A window: every sub-run
	s = sim.New(t0)
	n = 0
	s.OnSleep = func(int) {
		n++
		if n == 50 {
			s.SetTemps(func(x *sim.Sim) { x.ECCPU = 61 })
		}
	}
	r = runner.NewRunner(s, cfg)
	t2, _ = r.RunHandback(context.Background())
	r.Close()
	for _, run := range t2.Runs {
		for _, sub := range run.Subs {
			if sub.T2 != spec.Inconclusive || !strings.Contains(sub.T2Reason, "in the window that determined A") {
				t.Fatalf("sub-run %s: %s %q", sub.Label, sub.T2, sub.T2Reason)
			}
		}
	}
}
