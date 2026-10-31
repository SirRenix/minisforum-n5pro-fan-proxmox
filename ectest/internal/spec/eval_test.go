package spec

import (
	"math"
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

func TestEvalHandback(t *testing.T) {
	series := func(written int, vals map[float64]int, upto float64) []DCRSample {
		var out []DCRSample
		cur := written
		for ts := 5.0; ts <= upto; ts += 5 {
			if v, ok := vals[ts]; ok {
				cur = v
			}
			out = append(out, DCRSample{T: ts, DCR: cur})
		}
		return out
	}
	// leaves after 10 s, steady from then on: T2 PASS, T3 PASS
	s := EvalHandback(HandbackRun{Label: "H", Written: 255, Samples: series(255, map[float64]int{10: 0x57}, 900), TimingKnown: true})
	if s.T2 != Pass || s.T3 != Pass || *s.LeftAtS != 10 || *s.FinalValue != 0x57 {
		t.Fatalf("leave: %+v", s)
	}
	// stays within +-2 for the whole 1020-s log: T2 FAIL
	s = EvalHandback(HandbackRun{Label: "M", Written: 140, Samples: series(140, map[float64]int{300: 141}, ObserveS+SettleHoldS), TimingKnown: true})
	if s.T2 != Fail || s.Left {
		t.Fatalf("hold: %+v", s)
	}
	// within +-2, but the log ends at 900 s (abort between 900 and 1020 s): not FAIL
	s = EvalHandback(HandbackRun{Label: "M", Written: 140, Samples: series(140, nil, ObserveS), TimingKnown: true})
	if s.T2 != Inconclusive {
		t.Fatalf("hold 900 s only: %+v", s)
	}
	// leaves only after 90 s: INCONCLUSIVE
	s = EvalHandback(HandbackRun{Label: "H", Written: 255, Samples: series(255, map[float64]int{90: 200}, 900), TimingKnown: true})
	if s.T2 != Inconclusive || !s.Left || *s.LeftAtS != 90 {
		t.Fatalf("late: %+v", s)
	}
	// moves 10 counts: neither
	s = EvalHandback(HandbackRun{Label: "H", Written: 255, Samples: series(255, map[float64]int{20: 245}, 900), TimingKnown: true})
	if s.T2 != Inconclusive {
		t.Fatalf("small move: %+v", s)
	}
	// within +-2 but observed 5 min only: not FAIL
	s = EvalHandback(HandbackRun{Label: "H", Written: 140, Samples: series(140, nil, 300), TimingKnown: true})
	if s.T2 != Inconclusive {
		t.Fatalf("short: %+v", s)
	}
	// keeps oscillating: T3 FAIL
	osc := []DCRSample{}
	for ts := 5.0; ts <= 1100; ts += 5 {
		v := 100
		if int(ts/5)%2 == 0 {
			v = 120
		}
		osc = append(osc, DCRSample{T: ts, DCR: v})
	}
	if s = EvalHandback(HandbackRun{Label: "H", Written: 255, Samples: osc, TimingKnown: true}); s.T3 != Fail || s.T2 != Pass {
		t.Fatalf("oscillating: %+v", s)
	}
	if s = EvalHandback(HandbackRun{Label: "H", Written: 255}); s.T2 != Inconclusive || s.T3 != Inconclusive {
		t.Fatalf("empty: %+v", s)
	}
	_ = f
}

// T3 over the run's real observation length (Observe + SettleHoldS): a
// steady state from 800 s passes, one from 910 s fails, none at all fails.
func TestEvalHandbackT3Window(t *testing.T) {
	obs := float64(ObserveS + SettleHoldS)
	mk := func(steadyFrom float64) []DCRSample {
		var out []DCRSample
		for ts := 5.0; ts <= obs; ts += 5 {
			v := 100
			if ts < steadyFrom && int(ts/5)%2 == 0 {
				v = 130
			}
			out = append(out, DCRSample{T: ts, DCR: v})
		}
		return out
	}
	if s := EvalHandback(HandbackRun{Label: "H", Written: 255, Samples: mk(800), TimingKnown: true}); s.T3 != Pass || *s.SettleAtS > 800 {
		t.Fatalf("steady from 800 s: %s %s", s.T3, s.T3Reason)
	}
	if s := EvalHandback(HandbackRun{Label: "H", Written: 255, Samples: mk(910), TimingKnown: true}); s.T3 != Fail {
		t.Fatalf("steady from 910 s: %s %s", s.T3, s.T3Reason)
	}
	if s := EvalHandback(HandbackRun{Label: "H", Written: 255, Samples: mk(obs + 10), TimingKnown: true}); s.T3 != Fail {
		t.Fatalf("never steady: %s %s", s.T3, s.T3Reason)
	}
	// with only 900 s of data no FAIL is possible (the old run length)
	short := mk(obs + 10)
	short = short[:ObserveS/5]
	if s := EvalHandback(HandbackRun{Label: "H", Written: 255, Samples: short, TimingKnown: true}); s.T3 != Inconclusive {
		t.Fatalf("900 s of data: %s %s", s.T3, s.T3Reason)
	}
}

// A written value within 16 counts of A cannot show "left the written
// value": the sub-run is INCONCLUSIVE with that reason, never FAIL.
func TestEvalHandbackTooCloseToAuto(t *testing.T) {
	var smp []DCRSample
	for ts := 5.0; ts <= ObserveS+SettleHoldS; ts += 5 {
		smp = append(smp, DCRSample{T: ts, DCR: 200})
	}
	s := EvalHandback(HandbackRun{Label: "M", Written: 200, Samples: smp, TimingKnown: true, Auto: 200, AutoKnown: true})
	if s.T2 != Inconclusive || !strings.Contains(s.T2Reason, "too close to the auto value A=200") {
		t.Fatalf("M = A: %s %s", s.T2, s.T2Reason)
	}
	s = EvalHandback(HandbackRun{Label: "M", Written: 200, Samples: smp, TimingKnown: true, Auto: 150, AutoKnown: true})
	if s.T2 != Fail {
		t.Fatalf("M far from A: %s %s", s.T2, s.T2Reason)
	}
}

func TestAggregateHandback(t *testing.T) {
	p := SubResult{Label: "H", T2: Pass, T3: Pass, Left: true}
	pm := p
	pm.Label = "M"
	fl := SubResult{Label: "H", T2: Fail, T3: Pass}
	flm := fl
	flm.Label = "M"
	t2, t3 := AggregateHandback(3, [][]SubResult{{p, pm}, {p, pm}, {p, pm}}, []string{"H", "M"})
	if t2.Verdict != Pass || t3.Verdict != Pass {
		t.Fatalf("3x pass: %s %s", t2.Verdict, t3.Verdict)
	}
	t2, _ = AggregateHandback(3, [][]SubResult{{fl, flm}, {fl, flm}, {fl, flm}}, []string{"H", "M"})
	if t2.Verdict != Fail {
		t.Fatalf("3x fail: %s", t2.Verdict)
	}
	t2, _ = AggregateHandback(3, [][]SubResult{{p, pm}, {fl, flm}, {p, pm}}, []string{"H", "M"})
	if t2.Verdict != Inconclusive {
		t.Fatalf("mixed runs: %s", t2.Verdict)
	}
	t2, _ = AggregateHandback(3, [][]SubResult{{p, flm}, {p, flm}, {p, flm}}, []string{"H", "M"})
	if t2.Verdict != Inconclusive {
		t.Fatalf("mixed sub-runs: %s", t2.Verdict)
	}
	t2, _ = AggregateHandback(3, [][]SubResult{{p, pm}, {p, pm}}, []string{"H", "M"})
	if t2.Verdict != Inconclusive || !strings.Contains(strings.Join(t2.Reasons, ";"), "n=2 of 3") {
		t.Fatalf("n=2: %+v", t2)
	}
	for a, want := range map[int]int{0x55: 133, 0x57: 135, 180: 200, 30: 100} {
		if got := MValue(a); got != want {
			t.Errorf("MValue(%d) = %d, want %d", a, got, want)
		}
	}
}

func TestEvalT1(t *testing.T) {
	rest := map[int][]int{1: {2000, 2010}, 2: {2160, 2170}, 3: {1640, 1650}}
	mk := func(dcrOff int, follow bool) []T1Step {
		var out []T1Step
		rpm := map[int]int{255: 3540, 217: 3160, 179: 2725, 140: 2250}
		for _, d := range T1Steps {
			st := T1Step{Duty: d, DCR: []int{d + dcrOff, d + dcrOff}, DCR4: []int{d + dcrOff, d + dcrOff}, Fans: map[int][]int{1: {2005, 2003}, 2: {2165, 2166}, 3: {rpm[d], rpm[d]}}}
			if follow {
				st.Fans[1] = []int{rpm[d] - 1500, rpm[d] - 1500}
			}
			out = append(out, st)
		}
		return out
	}
	if r := EvalT1(3, 3, rest, nil, mk(0, false), []int{1, 2, 3}); r.Verdict != Pass {
		t.Fatalf("pass: %+v", r)
	}
	if r := EvalT1(3, 3, rest, nil, mk(5, false), []int{1, 2, 3}); r.Verdict != Fail {
		t.Fatalf("dcr off: %+v", r)
	}
	if r := EvalT1(3, 3, rest, nil, mk(0, true), []int{1, 2, 3}); r.Verdict != Fail {
		t.Fatalf("other tach follows: %+v", r)
	}
	noisy := map[int][]int{1: {2000, 2100}, 2: {2160}, 3: {1640}}
	if r := EvalT1(3, 3, noisy, nil, mk(0, false), []int{1, 2, 3}); r.Verdict != Inconclusive {
		t.Fatalf("noise: %+v", r)
	}
	nodcr := mk(0, false)
	for i := range nodcr {
		nodcr[i].DCR, nodcr[i].DCR4 = nil, nil
	}
	if r := EvalT1(3, 3, rest, nil, nodcr, []int{1, 2, 3}); r.Verdict != Inconclusive {
		t.Fatalf("no dcr: %+v", r)
	}
	// one sample of one step without DCR: every sample is required
	gap := mk(0, false)
	gap[2].DCR = []int{gap[2].Duty, -1}
	if r := EvalT1(3, 3, rest, nil, gap, []int{1, 2, 3}); r.Verdict != Inconclusive || r.Criteria[0].Met != nil {
		t.Fatalf("dcr gap: %+v", r)
	}
	// a differing sample still decides FAIL even with a gap elsewhere
	gap[1].DCR = []int{gap[1].Duty + 3, gap[1].Duty}
	if r := EvalT1(3, 3, rest, nil, gap, []int{1, 2, 3}); r.Verdict != Fail {
		t.Fatalf("dcr gap + mismatch: %+v", r)
	}
	// own tach unreadable at the end of the last step: no fake drop, not PASS
	blind := mk(0, false)
	blind[3].Fans[3] = []int{-1, -1}
	if r := EvalT1(3, 3, rest, nil, blind, []int{1, 2, 3}); r.Verdict != Inconclusive {
		t.Fatalf("own tach unreadable: %+v", r)
	}
	// PCIe: no own tach, DCR equal, others quiet
	if r := EvalT1(4, 0, rest, nil, func() []T1Step {
		s := mk(0, false)
		for i := range s {
			s[i].Fans[3] = []int{1645, 1645}
		}
		return s
	}(), []int{1, 2, 3}); r.Verdict != Pass {
		t.Fatalf("pcie: %+v", r)
	}
}

// G4 (TESTSPEC 0.4 §3, #20): EC system, EC ambient and HDD span < 1 C,
// every limit sensor present; Tctl is no stability criterion.
func TestEvalG4(t *testing.T) {
	c := func(v float64) func(int) float64 { return func(int) float64 { return v } }
	alt := func(a, b float64) func(int) float64 {
		return func(i int) float64 {
			if i%2 == 0 {
				return a
			}
			return b
		}
	}
	rows := func(tctl, sys, amb, hdd, nvme func(int) float64) []Sample {
		var out []Sample
		for i := 0; i <= GateWindowS/SampleS; i++ {
			out = append(out, Sample{Tctl: tctl(i), ECSys: sys(i), ECAmb: amb(i), HDDMax: hdd(i), NVMeMax: nvme(i)})
		}
		return out
	}
	cases := []struct {
		name                    string
		tctl, sys, amb, hdd, nv func(int) float64
		pass                    bool
		detail                  string
	}{
		{"tctl 13.5 C (window 1, 2026-10-17)", alt(35, 48.5), c(33), c(22), c(38), c(45), true, "system (ec 0x04) 0.0 C (< 1 C)"},
		{"system 1.0 C", c(40), alt(33, 34), c(22), c(38), c(45), false, "system (ec 0x04) 1.0 C (>= 1 C)"},
		{"system 0.9 C", c(40), alt(33, 33.9), c(22), c(38), c(45), true, "system (ec 0x04) 0.9 C (< 1 C)"},
		{"ambient 1.0 C", c(40), c(33), alt(22, 23), c(38), c(45), false, "ambient (ec 0x06) 1.0 C (>= 1 C)"},
		{"ambient missing", c(40), c(33), c(math.NaN()), c(38), c(45), false, "ambient (ec 0x06) not available"},
		{"hdd 1.0 C", c(40), c(33), c(22), alt(38, 39), c(45), false, "hdd 1.0 C (>= 1 C)"},
		{"nvme missing", c(40), c(33), c(22), c(38), c(math.NaN()), false, "limit sensor nvme not available"},
		{"tctl missing", c(math.NaN()), c(33), c(22), c(38), c(45), false, "limit sensor tctl not available"},
	}
	for _, tc := range cases {
		g := EvalG4(rows(tc.tctl, tc.sys, tc.amb, tc.hdd, tc.nv))
		if g.Pass != tc.pass || !strings.Contains(strings.Join(g.Parts, "; "), tc.detail) {
			t.Errorf("%s: pass %v %v", tc.name, g.Pass, g.Parts)
		}
		if strings.Contains(strings.Join(g.Parts, "; "), "tctl)") {
			t.Errorf("%s: Tctl judged as stability: %v", tc.name, g.Parts)
		}
	}
	if g := EvalG4(rows(c(40), c(33), c(22), c(38), c(math.NaN()))); g.Limit["nvme"] || !g.Limit["tctl"] || !g.Limit["hdd"] {
		t.Errorf("limit sensors %v", g.Limit)
	}
}

// Settle time (TESTSPEC 0.4 §2.1, #21): samples before +10 s (pwm1-3) or
// +1 s (pwm4) after the write are listed, not judged.
func TestEvalT1SettleTime(t *testing.T) {
	rest := map[int][]int{1: {2000, 2010}, 2: {2160, 2170}, 3: {1640, 1650}}
	rpm := map[int]int{255: 3540, 217: 3160, 179: 2725, 140: 2250}
	ts := []float64{5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60}
	mk := func(first int) []T1Step {
		var out []T1Step
		for _, d := range T1Steps {
			st := T1Step{Duty: d, T: ts, Fans: map[int][]int{1: {}, 2: {}, 3: {}}}
			for i := range ts {
				v := d
				if i == 0 {
					v = first // the EC ramp: the old value at +5 s
				}
				st.DCR, st.DCR4 = append(st.DCR, v), append(st.DCR4, v)
				st.Fans[1], st.Fans[2], st.Fans[3] = append(st.Fans[1], 2005), append(st.Fans[2], 2165), append(st.Fans[3], rpm[d])
			}
			out = append(out, st)
		}
		return out
	}
	r := EvalT1(3, 3, rest, nil, mk(0x64), []int{1, 2, 3})
	if r.Verdict != Pass {
		t.Fatalf("pwm3, +5 s off: %s %v", r.Verdict, r.Reasons)
	}
	steps := r.Values.([]T1Step)
	if len(steps[1].NotJudged) != 1 || steps[1].NotJudged[0] != "+5.0 s: DCR3 100, DCR4 100" {
		t.Fatalf("not judged %v", steps[1].NotJudged)
	}
	// +10 s is judged
	st := mk(0x64)
	st[2].DCR4[1] = 0x64
	if r := EvalT1(3, 3, rest, nil, st, []int{1, 2, 3}); r.Verdict != Fail {
		t.Fatalf("pwm3, +10 s off: %s", r.Verdict)
	}
	// pwm4: 1 s, so +5 s is judged
	pc := mk(0x64)
	for i := range pc {
		pc[i].Fans[3] = []int{1645, 1645, 1645, 1645, 1645, 1645, 1645, 1645, 1645, 1645, 1645, 1645}
		pc[i].DCR4 = nil
	}
	if r := EvalT1(4, 0, rest, nil, pc, []int{1, 2, 3}); r.Verdict != Fail {
		t.Fatalf("pwm4, +5 s off: %s %v", r.Verdict, r.Reasons)
	}
}

// T1 (TESTSPEC 0.4 §5.1, #24): another tach that follows while its table
// input crossed a step is INCONCLUSIVE, without a crossing FAIL.
func TestEvalT1TableInputCrossing(t *testing.T) {
	rest := map[int][]int{1: {2000, 2010}, 2: {2160, 2170}, 3: {1640, 1650}}
	restIn := map[int][]float64{1: {37, 38}, 2: {27, 27}}
	rpm := map[int]int{255: 3540, 217: 3160, 179: 2725, 140: 2250}
	mk := func(cpuIn float64) []T1Step {
		var out []T1Step
		for _, d := range T1Steps {
			out = append(out, T1Step{Duty: d, DCR: []int{d, d}, DCR4: []int{d, d},
				Fans:   map[int][]int{1: {2305, 2310}, 2: {2165, 2166}, 3: {rpm[d], rpm[d]}},
				Inputs: map[int][]float64{1: {cpuIn, cpuIn}, 2: {27, 27}}})
		}
		return out
	}
	r := EvalT1(3, 3, rest, restIn, mk(61), []int{1, 2, 3})
	if r.Verdict != Inconclusive || !strings.Contains(r.Reasons[0], "table input of pwm1 crossed a step") {
		t.Fatalf("crossed: %s %v", r.Verdict, r.Reasons)
	}
	// 46 C is not >= 45 + 2: no crossing, fan1 follows -> FAIL
	if r := EvalT1(3, 3, rest, restIn, mk(46), []int{1, 2, 3}); r.Verdict != Fail {
		t.Fatalf("not crossed: %s %v", r.Verdict, r.Reasons)
	}
	if x := Crossing(1, 60, []float64{53.5}); x != "" {
		t.Fatalf("53.5 from 60: %q", x)
	}
	if x := Crossing(1, 60, []float64{53}); !strings.Contains(x, "step at 55 C") {
		t.Fatalf("53 from 60: %q", x)
	}
	if x := Crossing(3, 22, []float64{35.9}); x != "" || Crossing(3, 22, []float64{36}) == "" || Crossing(4, 22, []float64{90}) != "" {
		t.Fatal("HDD step 34 C / PCIe")
	}
}

// T1 (TESTSPEC 0.4 §5.1, #24 case B, operator decision 2026-10-17): a table
// input of another channel that crossed a step makes T1 INCONCLUSIVE also
// when every other tach stays within +-50 rpm; the rules before it in §5.1
// keep their order.
func TestEvalT1CrossingWithoutFollowing(t *testing.T) {
	rest := map[int][]int{1: {2000, 2010}, 2: {2160, 2170}, 3: {1640, 1650}}
	restIn := map[int][]float64{1: {37, 38}, 2: {27, 27}}
	rpm := map[int]int{255: 3540, 217: 3160, 179: 2725, 140: 2250}
	mk := func(cpuIn float64, fan2 int, own map[int]int) []T1Step {
		var out []T1Step
		for _, d := range T1Steps {
			out = append(out, T1Step{Duty: d, DCR: []int{d, d}, DCR4: []int{d, d},
				Fans:   map[int][]int{1: {2005, 2008}, 2: {fan2, fan2}, 3: {own[d], own[d]}},
				Inputs: map[int][]float64{1: {cpuIn, cpuIn}, 2: {27, 27}}})
		}
		return out
	}
	// every other tach within +-50 rpm, EC 0x09 37.5 -> 61 C (steps 45, 55)
	r := EvalT1(3, 3, rest, restIn, mk(61, 2165, rpm), []int{1, 2, 3})
	if r.Verdict != Inconclusive || len(r.Reasons) != 1 || !strings.HasPrefix(r.Reasons[0], "table input of pwm1 crossed a step") || strings.Contains(r.Reasons[0], "another tach") {
		t.Fatalf("crossed, tachs quiet: %s %v", r.Verdict, r.Reasons)
	}
	// the same run without the crossing passes
	if r := EvalT1(3, 3, rest, restIn, mk(46, 2165, rpm), []int{1, 2, 3}); r.Verdict != Pass {
		t.Fatalf("not crossed: %s %v", r.Verdict, r.Reasons)
	}
	// fan2 outside +-50 while only pwm1's input crossed: no FAIL (a crossing
	// rules it out), not the "that channel crossed" rule either -> the
	// crossing rule at the end
	if r := EvalT1(3, 3, rest, restIn, mk(61, 2400, rpm), []int{1, 2, 3}); r.Verdict != Inconclusive || !strings.HasPrefix(r.Reasons[0], "table input of pwm1 crossed a step") {
		t.Fatalf("fan2 follows, pwm1 crossed: %s %v", r.Verdict, r.Reasons)
	}
	// own tach < 200 rpm comes before the crossing rule (§5.1 order)
	flat := map[int]int{255: 3540, 217: 3500, 179: 3460, 140: 3420}
	if r := EvalT1(3, 3, rest, restIn, mk(61, 2165, flat), []int{1, 2, 3}); r.Verdict != Inconclusive || !strings.HasPrefix(r.Reasons[0], "own tach") {
		t.Fatalf("own tach and crossing: %s %v", r.Verdict, r.Reasons)
	}
	// a DCR that differs stays FAIL with a crossing
	bad := mk(61, 2165, rpm)
	bad[1].DCR = []int{217, 200}
	if r := EvalT1(3, 3, rest, restIn, bad, []int{1, 2, 3}); r.Verdict != Fail {
		t.Fatalf("DCR differs and crossing: %s %v", r.Verdict, r.Reasons)
	}
}

// T2/T3 (TESTSPEC 0.4 §5.2, #17): a sub-run whose table input crossed a
// step is INCONCLUSIVE in both tests.
func TestEvalHandbackCrossed(t *testing.T) {
	var smp []DCRSample
	for ts := 5.0; ts <= ObserveS+SettleHoldS; ts += 5 {
		smp = append(smp, DCRSample{T: ts, DCR: 0x55})
	}
	r := HandbackRun{Label: "H", Written: 255, Samples: smp, TimingKnown: true}
	if s := EvalHandback(r); s.T2 != Pass || s.T3 != Pass {
		t.Fatalf("baseline %s %s", s.T2, s.T3)
	}
	r.Crossed = "EC 0x09 (CPU) 37.0 C -> 61.0 C across the step at 45 C"
	if s := EvalHandback(r); s.T2 != Inconclusive || s.T3 != Inconclusive || !strings.HasPrefix(s.T2Reason, "table input crossed a step") {
		t.Fatalf("crossed %s %s %q", s.T2, s.T3, s.T2Reason)
	}
}

// T1 on pwm3 judges DCR3 and DCR4 (TESTSPEC §5.1).
func TestEvalT1DCR4(t *testing.T) {
	rest := map[int][]int{1: {2000, 2010}, 2: {2160, 2170}, 3: {1640, 1650}}
	rpm := map[int]int{255: 3540, 217: 3160, 179: 2725, 140: 2250}
	mk := func() []T1Step {
		var out []T1Step
		for _, d := range T1Steps {
			out = append(out, T1Step{Duty: d, DCR: []int{d, d}, DCR4: []int{d, d}, Fans: map[int][]int{1: {2005, 2003}, 2: {2165, 2166}, 3: {rpm[d], rpm[d]}}})
		}
		return out
	}
	r := EvalT1(3, 3, rest, nil, mk(), []int{1, 2, 3})
	last := r.Criteria[len(r.Criteria)-1]
	if r.Verdict != Pass || last.Met == nil || !*last.Met {
		t.Fatalf("both equal: %+v", r)
	}
	// DCR4 differs in one sample: FAIL, the difference is listed
	st := mk()
	st[2].DCR4 = []int{179, 182}
	r = EvalT1(3, 3, rest, nil, st, []int{1, 2, 3})
	last = r.Criteria[len(r.Criteria)-1]
	if r.Verdict != Fail || !strings.Contains(r.Criteria[0].Detail, "step 179: DCR4 182") || last.Met == nil || *last.Met || !strings.Contains(last.Detail, "DCR3 179, DCR4 182") {
		t.Fatalf("DCR4 differs: %+v", r)
	}
	if !strings.Contains(r.Reasons[0], "DCR3 or DCR4") {
		t.Fatalf("reason %v", r.Reasons)
	}
	// DCR4 not readable: not PASS
	st = mk()
	for i := range st {
		st[i].DCR4 = nil
	}
	r = EvalT1(3, 3, rest, nil, st, []int{1, 2, 3})
	if r.Verdict != Inconclusive || r.Criteria[0].Met != nil || !strings.Contains(r.Criteria[0].Detail, "no judged DCR4 sample") {
		t.Fatalf("DCR4 missing: %+v", r)
	}
	// other channels ignore DCR4
	st = mk()
	for i := range st {
		st[i].DCR4 = nil
	}
	if r = EvalT1(1, 1, rest, nil, func() []T1Step {
		s := st
		for i := range s {
			s[i].Fans[1], s[i].Fans[3] = []int{rpm[s[i].Duty], rpm[s[i].Duty]}, []int{1645, 1645}
		}
		return s
	}(), []int{1, 2, 3}); r.Verdict != Pass {
		t.Fatalf("pwm1 without DCR4: %+v", r)
	}
}

// T2/T3 on pwm3: DCR3 and DCR4 evaluated per register; a disagreement is
// INCONCLUSIVE and reported, final values are reported, not judged.
func TestEvalHandbackRegs(t *testing.T) {
	series := func(written int, vals map[float64]int) []DCRSample {
		var out []DCRSample
		cur := written
		for ts := 5.0; ts <= ObserveS+SettleHoldS; ts += 5 {
			if v, ok := vals[ts]; ok {
				cur = v
			}
			out = append(out, DCRSample{T: ts, DCR: cur})
		}
		return out
	}
	run := func(reg string, smp []DCRSample) HandbackRun {
		return HandbackRun{Label: "H", Reg: reg, Written: 255, Samples: smp, TimingKnown: true, Auto: 0x57, AutoKnown: true}
	}
	leave := series(255, map[float64]int{10: 0x57})
	stay := series(255, nil)
	s := EvalHandbackRegs([]HandbackRun{run("DCR3", leave), run("DCR4", leave)})
	if s.T2 != Pass || s.T3 != Pass || s.RegsAgree == nil || !*s.RegsAgree || len(s.RegNotes) != 0 || len(s.Others) != 1 {
		t.Fatalf("agree: %+v", s)
	}
	s = EvalHandbackRegs([]HandbackRun{run("DCR3", leave), run("DCR4", stay)})
	notes := strings.Join(s.RegNotes, "; ")
	if s.T2 != Inconclusive || !strings.HasPrefix(s.T2Reason, "DCR3/DCR4 disagree") || s.RegsAgree == nil || *s.RegsAgree || s.Left {
		t.Fatalf("disagree: %+v", s)
	}
	if !strings.Contains(notes, "T2: DCR3 PASS, DCR4 FAIL") || !strings.Contains(notes, "T3 final values differ (reported, not judged): DCR3 87, DCR4 255") {
		t.Fatalf("notes: %s", notes)
	}
	if s.T3 != Pass {
		t.Fatalf("T3 both steady: %s %s", s.T3, s.T3Reason)
	}
	s = EvalHandbackRegs([]HandbackRun{run("DCR3", stay), run("DCR4", stay)})
	if s.T2 != Fail {
		t.Fatalf("both stay: %s %s", s.T2, s.T2Reason)
	}
	// aggregation: the disagreement is a T2 criterion, not a T3 one
	d := EvalHandbackRegs([]HandbackRun{run("DCR3", leave), run("DCR4", stay)})
	dm := d
	dm.Label = "M"
	t2, t3 := AggregateHandback(3, [][]SubResult{{d, dm}, {d, dm}, {d, dm}}, []string{"H", "M"})
	c2 := t2.Criteria[len(t2.Criteria)-1]
	c3 := t3.Criteria[len(t3.Criteria)-1]
	if t2.Verdict != Inconclusive || c2.Met == nil || *c2.Met || !strings.Contains(c2.Name, "same T2 verdict") {
		t.Fatalf("aggregate T2: %s %+v", t2.Verdict, t2.Criteria)
	}
	if t3.Verdict != Pass || c3.Met == nil || !*c3.Met || !strings.Contains(c3.Detail, "T3 final values differ") {
		t.Fatalf("aggregate T3: %s %+v", t3.Verdict, t3.Criteria)
	}
}

// T4/T5 (TESTSPEC §5, §5.5): not implemented in the tool; reasons per
// channel, N/A on pwm4.
func TestT4T5Result(t *testing.T) {
	amb := func(vs ...float64) []Sample {
		var out []Sample
		for _, v := range vs {
			out = append(out, Sample{ECAmb: v})
		}
		return out
	}
	r := T4T5Result("T4", 3, amb(22, 22.5, math.NaN()))
	if r.Verdict != Inconclusive || !strings.HasPrefix(r.Reasons[0], ReasonHDDBelowStep+" (max 22.5 C") {
		t.Fatalf("hdd below: %+v", r)
	}
	if r = T4T5Result("T5", 3, amb(22, 34)); r.Verdict != Inconclusive || r.Reasons[0] != ReasonT4T5Manual {
		t.Fatalf("hdd at the step: %+v", r)
	}
	if r = T4T5Result("T5", 3, amb(math.NaN())); r.Verdict != Inconclusive || !strings.HasPrefix(r.Reasons[0], ReasonT4T5Manual+" (EC 0x06 not recorded") {
		t.Fatalf("hdd no input: %+v", r)
	}
	for _, ch := range []int{1, 2} {
		if r = T4T5Result("T4", ch, amb(22)); r.Verdict != Inconclusive || r.Reasons[0] != ReasonT4T5Manual {
			t.Fatalf("pwm%d: %+v", ch, r)
		}
	}
	if r = T4T5Result("T4", 4, nil); r.Verdict != NotApplicable || r.Reasons[0] != "no EC automatic on DCR5 (firmware 0.14)" {
		t.Fatalf("pwm4: %+v", r)
	}
}
