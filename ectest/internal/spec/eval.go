// Package spec implements the verdict rules of the N5 Pro EC test
// specification (n5pro-ec/docs/TESTSPEC.md, version SpecVersion): T1
// (mapping), T2 (hand-back), T3 (settling) and the aggregation over runs,
// the G4 gate, the settle time (§2.1), the table-input crossing rules (§5.1,
// §5.2), the reasons for T4/T5 (not evaluated by this tool yet, §5.5) and
// N/A for PCIe. Verdicts are computed from the recorded numbers only
// (TESTSPEC §1 rule 4); the runner and the report use the same functions.
package spec

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// SpecVersion is the TESTSPEC version this code implements.
const SpecVersion = "0.5"

// Verdict of a test or sub-run.
type Verdict string

const (
	Pass         Verdict = "PASS"
	Fail         Verdict = "FAIL"
	Inconclusive Verdict = "INCONCLUSIVE"
	// Reported is used for T6, which has no verdict (TESTSPEC §5:
	// reported values only).
	Reported Verdict = "REPORTED"
	// NotApplicable is a test that does not apply to the channel
	// (TESTSPEC §1 rule 2: PCIe T2-T5, no EC automatic on DCR5).
	NotApplicable Verdict = "N/A"
)

// ReasonNoPCIeAuto is the N/A reason of T2-T5 on pwm4 (TESTSPEC §6).
const ReasonNoPCIeAuto = "no EC automatic on DCR5 (firmware 0.14)"

// PCIeChannel is the pwm channel without EC automatic (DCR5).
const PCIeChannel = 4

// HDDInputStepC is the only table step of the HDD tables C/D: EC 0x06
// (ambient) < 34 C -> 1200 rpm, >= 34 C -> 1600 rpm (TESTSPEC §6).
const HDDInputStepC = 34.0

// Spec numbers (TESTSPEC §4, §5).
const (
	LeaveCounts    = 16  // T2 PASS: DCR differs from the written value by >= 16
	HoldCounts     = 2   // T2 FAIL: DCR within +-2 of the written value
	LeaveWithinS   = 60  // T2 PASS: ... within 60 s after the auto command
	ObserveS       = 900 // T2/T3 pwm3: T3 limit; the log adds SettleHoldS (1020 s)
	ObserveFastS   = 180 // T2/T3 pwm1/pwm2: T3 limit; the log adds SettleHoldS (300 s, 0.5 #34)
	PreS           = 900 // T2/T3 pwm3: window that determines A
	PreFastS       = 300 // T2/T3 pwm1/pwm2: window that determines A (0.5 #34)
	SettleBandC    = 2   // T3: DCR constant +-2 ...
	SettleHoldS    = 120 // ... for 120 s
	T1OwnStepRPM   = 200 // T1: own tach changes >= 200 rpm per step
	T1OtherRPM     = 50  // T1: tach noise limit; other tachs outside +-50 rpm are reported (0.5 #30)
	T1OtherDCR     = 16  // T1: another channel's DCR this far from its rest value follows (0.5 #33)
	RunsRequired   = 3   // T2-T5 are run 3x
	MinDuty        = 100 // duty below 100 is never written (§4)
	MaxM           = 200 // T2: M = min(A + 48, 200)
	MOffset        = 48
	LimitTctlC     = 85.0
	LimitNVMeC     = 70.0
	LimitHDDC      = 50.0
	T1StepHoldS    = 60
	SampleS        = 5
	GateLoadDiv    = 10  // G3: load average (1 min) < online CPUs / 10 (0.5 #31)
	GateTempDeltaC = 1.0 // G4: per input trend < 1 C and span <= 1 C (0.5 #29); Tctl is a safety limit only (0.4 #20)
	GateTrendS     = 60  // G4: the trend compares the means of the first and the last 60 s
	GateWindowS    = 300
	// DueToleranceS: a sample read within +-0.5 s of its due time is judged
	// at its due time (TESTSPEC 0.4 §2, #11).
	DueToleranceS = 0.5
	// Settle time after a write (TESTSPEC §2.1, 0.4 #21, 0.5 #32): a DCR read
	// earlier than this is recorded, not judged. pwm1, pwm2, pwm4 1 s
	// (measured <= 0.22 s), pwm3 10 s.
	SettleFastS = 1.0
	SettleHDDS  = 10.0
	// CrossingC: a table input crosses a step boundary S when it lies >= 2 C
	// beyond S on the other side (TESTSPEC 0.4 §5.1 #24, §5.2 #17).
	CrossingC = 2.0
)

// SettleS is the settle time of channel ch after a write in seconds
// (TESTSPEC §2.1): pwm3 10 s, the others 1 s.
func SettleS(ch int) float64 {
	if ch == 3 {
		return SettleHDDS
	}
	return SettleFastS
}

// T3LimitS is the T3 limit of channel ch in seconds (TESTSPEC §5.2): 180 s
// on pwm1/pwm2, 900 s on pwm3. The log after the auto command is this plus
// SettleHoldS, so a steady state that starts at the limit is seen in full.
func T3LimitS(ch int) int {
	if ch == 1 || ch == 2 {
		return ObserveFastS
	}
	return ObserveS
}

// PreWindowS is the window that determines A on channel ch in seconds
// (TESTSPEC §5.2): 300 s on pwm1/pwm2, 900 s on pwm3.
func PreWindowS(ch int) int {
	if ch == 1 || ch == 2 {
		return PreFastS
	}
	return PreS
}

// GateLoadLimit is the G3 limit for cpus online CPUs (TESTSPEC §3).
func GateLoadLimit(cpus int) float64 { return float64(cpus) / GateLoadDiv }

// ParseCPUList counts the CPUs of a kernel CPU list ("0-23", "0-3,8,10-11");
// 0 when it cannot be parsed.
func ParseCPUList(s string) int {
	n := 0
	for _, part := range strings.Split(strings.TrimSpace(s), ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			return 0
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				return 0
			}
		}
		n += b - a + 1
	}
	return n
}

// Judged reports whether a DCR read t seconds after the write of channel ch
// is judged (t >= settle time; the 1 ms covers the stored ms rounding).
func Judged(ch int, t float64) bool { return t >= SettleS(ch)-0.001 }

// TableInput is the EC table input of channel ch in sample s (TESTSPEC §6:
// pwm1 EC 0x09, pwm2 EC 0x05, pwm3 EC 0x06; pwm4 has none: NaN).
func TableInput(ch int, s Sample) float64 {
	switch ch {
	case 1:
		return s.ECCPU
	case 2:
		return s.ECBoard
	case 3:
		return s.ECAmb
	}
	return math.NaN()
}

// TableInputName names the table input of channel ch.
func TableInputName(ch int) string {
	return map[int]string{1: "EC 0x09 (CPU)", 2: "EC 0x05 (board)", 3: "EC 0x06 (ambient)"}[ch]
}

// StepBoundaries are the table step boundaries S of channel ch (TESTSPEC §6).
func StepBoundaries(ch int) []float64 {
	switch ch {
	case 1:
		return []float64{25, 45, 55, 65, 75, 82, 87}
	case 2:
		return []float64{60}
	case 3:
		return []float64{HDDInputStepC}
	}
	return nil
}

// Crossing reports the first value of vals that lies >= CrossingC beyond a
// step boundary S of channel ch on the other side than ref ("" = none).
// NaN values are skipped; a NaN ref decides nothing.
func Crossing(ch int, ref float64, vals []float64) string {
	if math.IsNaN(ref) {
		return ""
	}
	for _, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		for _, s := range StepBoundaries(ch) {
			if (ref < s && v >= s+CrossingC) || (ref >= s && v <= s-CrossingC) {
				return fmt.Sprintf("%s %.1f C -> %.1f C across the step at %.0f C", TableInputName(ch), ref, v, s)
			}
		}
	}
	return ""
}

// JudgedRegs are the duty registers judged for pwm channel ch (TESTSPEC
// §2): DCR1, DCR2, DCR3 and DCR4 (HDD group, both judged), DCR5. The first
// one is the primary register (A, M and the T1 own tach refer to it).
func JudgedRegs(ch int) []int {
	switch ch {
	case 1, 2:
		return []int{ch}
	case 3:
		return []int{3, 4}
	case 4:
		return []int{5}
	}
	return nil
}

// OtherRegs are the duty registers of the channels other than ch.
func OtherRegs(ch int) []int {
	own := map[int]bool{}
	for _, n := range JudgedRegs(ch) {
		own[n] = true
	}
	var out []int
	for n := 1; n <= 5; n++ {
		if !own[n] {
			out = append(out, n)
		}
	}
	return out
}

// regChannel is the pwm channel of duty register n (TESTSPEC §2).
func regChannel(n int) int {
	switch n {
	case 1, 2:
		return n
	case 3, 4:
		return 3
	}
	return PCIeChannel
}

// T1Steps are the duties of the mapping test, in order.
var T1Steps = []int{255, 217, 179, 140}

// Criterion is one named check with the numbers it was computed from.
// Met is nil when the data does not allow a decision.
type Criterion struct {
	Name   string         `json:"name"`
	Met    *bool          `json:"met"`
	Detail string         `json:"detail"`
	Values map[string]any `json:"values,omitempty"`
}

func boolp(b bool) *bool { return &b }

// Result is the verdict of one test.
type Result struct {
	ID       string      `json:"id"`
	Channel  int         `json:"channel,omitempty"` // pwm channel, 0 for channel-independent tests
	Verdict  Verdict     `json:"verdict"`
	Reasons  []string    `json:"reasons,omitempty"`
	Criteria []Criterion `json:"criteria,omitempty"`
	Runs     []RunResult `json:"runs,omitempty"`
	Values   any         `json:"values,omitempty"`
}

// RunResult is one repetition of T2/T3 with its sub-runs.
type RunResult struct {
	Run     int         `json:"run"`
	Verdict Verdict     `json:"verdict"`
	Reason  string      `json:"reason,omitempty"`
	Subs    []SubResult `json:"sub_runs"`
}

// DCRSample is one duty-register reading of the channel under test;
// T is seconds since the auto command (TimingKnown) or since the first
// sample (replay without a hand-back timestamp).
type DCRSample struct {
	T   float64 `json:"t_s"`
	DCR int     `json:"dcr"`
}

// HandbackRun is the data of one T2 sub-run: the written value, then the
// DCR after the auto command.
type HandbackRun struct {
	Label       string      `json:"label"`              // "H", "M" or "replay"
	Reg         string      `json:"register,omitempty"` // "DCR3", ... ("" = the channel's register)
	Written     int         `json:"written"`
	Samples     []DCRSample `json:"samples"`
	TimingKnown bool        `json:"timing_known"` // T is relative to the auto command
	// Auto is the channel's auto value A read before the sub-runs
	// (AutoKnown false: not measured, e.g. replay). A written value closer
	// than LeaveCounts to A cannot show "left the written value" when the EC
	// returns to A, so such a sub-run is INCONCLUSIVE for T2.
	Auto      int  `json:"auto"`
	AutoKnown bool `json:"auto_known"`
	// LimitS is the T3 limit of the channel in seconds (T3LimitS; 0: ObserveS,
	// e.g. replay, where the channel's timing is not the point).
	LimitS int `json:"t3_limit_s,omitempty"`
	// Crossed is set when the channel's table input crossed a step between
	// the write and the end of the log, or in the window that determined A
	// (TESTSPEC 0.4 §5.2 #17): T2 and T3 of the sub-run are INCONCLUSIVE.
	Crossed string `json:"table_input_crossed,omitempty"`
}

// SubResult is the evaluation of one HandbackRun for T2 and T3.
type SubResult struct {
	Label   string `json:"label"`
	Reg     string `json:"register,omitempty"`
	Written int    `json:"written"`
	// T2
	Left       bool     `json:"left_written_value"`    // some sample differs by >= LeaveCounts
	LeftAtS    *float64 `json:"left_at_s"`             // first such sample (nil: never or timing unknown)
	MaxDev     int      `json:"max_deviation"`         // largest |DCR - written|
	HeldS      float64  `json:"held_within_2_s"`       // observed time with every sample within +-2
	ObservedS  float64  `json:"observed_s"`            // span of the samples
	T2         Verdict  `json:"t2"`                    // sub-run verdict
	T2Reason   string   `json:"t2_reason"`             //
	Settled    bool     `json:"settled"`               // T3
	SettleAtS  *float64 `json:"settle_at_s"`           // start of the first steady window
	FinalValue *int     `json:"final_value,omitempty"` // value of the steady window
	T3         Verdict  `json:"t3"`
	T3Reason   string   `json:"t3_reason"`
	// More than one judged register (pwm3: DCR3 and DCR4, TESTSPEC §5.2):
	// the fields above are DCR3's numbers with the combined verdicts,
	// Others holds DCR4's evaluation. RegsAgree is false when the verdicts
	// of the registers differ (nil: one register); RegNotes lists every
	// difference, including final values (reported, not judged).
	Others    []SubResult `json:"other_registers,omitempty"`
	RegsAgree *bool       `json:"registers_agree,omitempty"`
	RegNotes  []string    `json:"register_notes,omitempty"`
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// EvalHandback evaluates one sub-run for T2 and T3.
func EvalHandback(r HandbackRun) SubResult {
	s := SubResult{Label: r.Label, Reg: r.Reg, Written: r.Written, T2: Inconclusive, T3: Inconclusive}
	limit := r.LimitS
	if limit == 0 {
		limit = ObserveS
	}
	if len(r.Samples) == 0 {
		s.T2Reason, s.T3Reason = "no DCR samples", "no DCR samples"
		return s
	}
	smp := append([]DCRSample(nil), r.Samples...)
	sort.SliceStable(smp, func(i, j int) bool { return smp[i].T < smp[j].T })
	first, last := smp[0].T, smp[len(smp)-1].T
	s.ObservedS = last - first
	if r.TimingKnown {
		s.ObservedS = last
	}
	allWithin2 := true
	for _, x := range smp {
		d := absInt(x.DCR - r.Written)
		if d > s.MaxDev {
			s.MaxDev = d
		}
		if d >= LeaveCounts && !s.Left {
			s.Left = true
			if r.TimingKnown {
				t := x.T
				s.LeftAtS = &t
			}
		}
		if d > HoldCounts {
			allWithin2 = false
		}
	}
	if allWithin2 {
		s.HeldS = s.ObservedS
	}
	switch {
	case s.Left && s.LeftAtS != nil && *s.LeftAtS <= LeaveWithinS:
		s.T2, s.T2Reason = Pass, fmt.Sprintf("left the written value %d by >= %d counts after %.0f s", r.Written, LeaveCounts, *s.LeftAtS)
	case s.Left && s.LeftAtS != nil:
		s.T2Reason = fmt.Sprintf("left the written value only after %.0f s (> %d s)", *s.LeftAtS, LeaveWithinS)
	case s.Left:
		s.T2Reason = fmt.Sprintf("left the written value %d (max deviation %d counts), time since the auto command not recorded", r.Written, s.MaxDev)
	// FAIL needs the whole log of the sub-run (TESTSPEC §5.2: the T3 limit
	// plus SettleHoldS), not only T3's limit
	case allWithin2 && r.TimingKnown && s.ObservedS >= float64(limit+SettleHoldS):
		s.T2, s.T2Reason = Fail, fmt.Sprintf("DCR stayed within +-%d of %d for %.0f s", HoldCounts, r.Written, s.ObservedS)
	case allWithin2:
		s.T2Reason = fmt.Sprintf("DCR within +-%d of the written value, but observed for %.0f s only (need %d s after the auto command)", HoldCounts, s.ObservedS, limit+SettleHoldS)
	default:
		s.T2Reason = fmt.Sprintf("DCR moved by %d counts (between %d and %d)", s.MaxDev, HoldCounts+1, LeaveCounts-1)
	}
	if r.AutoKnown && absInt(r.Written-r.Auto) < LeaveCounts {
		s.T2 = Inconclusive
		s.T2Reason = fmt.Sprintf("written value %d too close to the auto value A=%d (< %d counts): leaving it cannot be told from returning to A (%s)", r.Written, r.Auto, LeaveCounts, s.T2Reason)
	}
	// T3: first window of SettleHoldS seconds with every sample within
	// +-SettleBandC of the window's first sample.
	for i := range smp {
		start := smp[i].T
		end := start + SettleHoldS
		if end > last {
			break
		}
		ok := true
		for j := i; j < len(smp) && smp[j].T <= end; j++ {
			if absInt(smp[j].DCR-smp[i].DCR) > SettleBandC {
				ok = false
				break
			}
		}
		if ok {
			s.Settled = true
			t, v := start, smp[i].DCR
			s.SettleAtS, s.FinalValue = &t, &v
			break
		}
	}
	switch {
	case s.Settled && r.TimingKnown && *s.SettleAtS <= float64(limit):
		s.T3, s.T3Reason = Pass, fmt.Sprintf("steady at %d (+-%d for %d s) from %.0f s after the auto command", *s.FinalValue, SettleBandC, SettleHoldS, *s.SettleAtS)
	case s.Settled && r.TimingKnown:
		s.T3, s.T3Reason = Fail, fmt.Sprintf("steady state only from %.0f s (> %d s)", *s.SettleAtS, limit)
	case s.Settled:
		s.T3Reason = fmt.Sprintf("steady at %d over the observed samples, time since the auto command not recorded", *s.FinalValue)
	case r.TimingKnown && last >= float64(limit+SettleHoldS):
		s.T3, s.T3Reason = Fail, fmt.Sprintf("no steady state within %d s", limit)
	default:
		s.T3Reason = fmt.Sprintf("no steady window in %.0f s of data", s.ObservedS)
	}
	if r.Crossed != "" {
		why := "table input crossed a step (" + r.Crossed + ")"
		s.T2, s.T2Reason = Inconclusive, why+"; "+s.T2Reason
		s.T3, s.T3Reason = Inconclusive, why+"; "+s.T3Reason
	}
	return s
}

// EvalHandbackRegs evaluates one sub-run over every judged register of the
// channel (TESTSPEC §5.2, pwm3: DCR3 and DCR4, each with its own A). runs[0]
// is the primary register; the result carries its numbers, the others are
// in Others. Per test: the same verdict in every register is the verdict;
// different verdicts give INCONCLUSIVE ("DCR3/DCR4 disagree"). Different T3
// final values are noted, not judged (each register regulates on its own
// tach, TESTSPEC §6).
func EvalHandbackRegs(runs []HandbackRun) SubResult {
	if len(runs) == 0 {
		return SubResult{T2: Inconclusive, T3: Inconclusive, T2Reason: "no register", T3Reason: "no register"}
	}
	all := make([]SubResult, len(runs))
	for i, r := range runs {
		all[i] = EvalHandback(r)
	}
	s := all[0]
	if len(all) == 1 {
		return s
	}
	s.Others = all[1:]
	name := func(x SubResult, i int) string {
		if x.Reg != "" {
			return x.Reg
		}
		return fmt.Sprintf("register %d", i+1)
	}
	var names []string
	for i, x := range all {
		names = append(names, name(x, i))
	}
	pair := strings.Join(names, "/")
	agree := true
	merge := func(test string, get func(SubResult) (Verdict, string)) (Verdict, string) {
		var vs []Verdict
		var parts []string
		for i, x := range all {
			v, why := get(x)
			vs = append(vs, v)
			parts = append(parts, fmt.Sprintf("%s %s (%s)", name(x, i), v, why))
		}
		v := combine(vs)
		same := true
		for _, x := range vs {
			same = same && x == vs[0]
		}
		if !same {
			agree = false
			var short []string
			for i, x := range vs {
				short = append(short, fmt.Sprintf("%s %s", names[i], x))
			}
			s.RegNotes = append(s.RegNotes, test+": "+strings.Join(short, ", "))
			return Inconclusive, pair + " disagree: " + strings.Join(parts, "; ")
		}
		return v, strings.Join(parts, "; ")
	}
	s.T2, s.T2Reason = merge("T2", func(x SubResult) (Verdict, string) { return x.T2, x.T2Reason })
	s.T3, s.T3Reason = merge("T3", func(x SubResult) (Verdict, string) { return x.T3, x.T3Reason })
	for _, o := range all[1:] {
		s.Left = s.Left && o.Left
	}
	// final values: reported, not judged
	var finals []string
	differ := false
	for i, x := range all {
		if x.FinalValue == nil {
			finals = append(finals, names[i]+" -")
			continue
		}
		finals = append(finals, fmt.Sprintf("%s %d", names[i], *x.FinalValue))
		if all[0].FinalValue != nil && *x.FinalValue != *all[0].FinalValue {
			differ = true
		}
	}
	if differ {
		s.RegNotes = append(s.RegNotes, "T3 final values differ (reported, not judged): "+strings.Join(finals, ", "))
	}
	s.RegsAgree = boolp(agree)
	return s
}

// combine is the verdict over parts that must all agree (TESTSPEC §5:
// PASS only if all pass, FAIL only if all fail, else INCONCLUSIVE).
func combine(vs []Verdict) Verdict {
	if len(vs) == 0 {
		return Inconclusive
	}
	all := func(v Verdict) bool {
		for _, x := range vs {
			if x != v {
				return false
			}
		}
		return true
	}
	switch {
	case all(Pass):
		return Pass
	case all(Fail):
		return Fail
	}
	return Inconclusive
}

// AggregateHandback builds the T2 and T3 results for one channel from the
// repetitions (each with the sub-runs H and M). want is the number of
// sub-run labels a complete repetition has ("H", "M").
func AggregateHandback(ch int, runs [][]SubResult, want []string) (t2, t3 Result) {
	t2 = Result{ID: "T2", Channel: ch}
	t3 = Result{ID: "T3", Channel: ch}
	var v2, v3 []Verdict
	leftMet, leftSeen := 0, 0
	for i, subs := range runs {
		labels := map[string]bool{}
		var s2, s3 []Verdict
		for _, s := range subs {
			labels[s.Label] = true
			s2 = append(s2, s.T2)
			s3 = append(s3, s.T3)
			leftSeen++
			if s.Left {
				leftMet++
			}
		}
		var missing []string
		for _, l := range want {
			if !labels[l] {
				missing = append(missing, l)
			}
		}
		r2 := RunResult{Run: i + 1, Verdict: combine(s2), Subs: subs}
		r3 := RunResult{Run: i + 1, Verdict: combine(s3), Subs: subs}
		if len(missing) > 0 {
			reason := "sub-run " + strings.Join(missing, ", ") + " missing (one direction only)"
			r2.Verdict, r2.Reason = Inconclusive, reason
			r3.Verdict, r3.Reason = Inconclusive, reason
		} else if r2.Verdict == Inconclusive {
			r2.Reason = "mixed or undecided sub-runs"
		}
		t2.Runs = append(t2.Runs, r2)
		t3.Runs = append(t3.Runs, r3)
		v2 = append(v2, r2.Verdict)
		v3 = append(v3, r3.Verdict)
	}
	t2.Verdict, t3.Verdict = combine(v2), combine(v3)
	if len(runs) < RunsRequired {
		t2.Verdict, t3.Verdict = Inconclusive, Inconclusive
		reason := fmt.Sprintf("n=%d of %d runs", len(runs), RunsRequired)
		t2.Reasons = append(t2.Reasons, reason)
		t3.Reasons = append(t3.Reasons, reason)
	}
	for _, r := range t2.Runs {
		if r.Reason != "" {
			t2.Reasons = append(t2.Reasons, fmt.Sprintf("run %d: %s", r.Run, r.Reason))
		}
		for _, s := range r.Subs {
			t2.Reasons = append(t2.Reasons, fmt.Sprintf("run %d %s: %s", r.Run, s.Label, s.T2Reason))
			t3.Reasons = append(t3.Reasons, fmt.Sprintf("run %d %s: %s", r.Run, s.Label, s.T3Reason))
		}
	}
	if leftSeen > 0 {
		t2.Criteria = append(t2.Criteria, Criterion{
			Name:   "left written value (|DCR - written| >= 16)",
			Met:    boolp(leftMet == leftSeen),
			Detail: fmt.Sprintf("%d of %d sub-runs", leftMet, leftSeen),
		})
	}
	// pwm3: DCR3 and DCR4 judged together (TESTSPEC §5.2); a disagreement
	// is reported in both tests
	regsCrit := func(test string) (Criterion, bool) {
		seen, agree := 0, 0
		var notes []string
		for _, r := range t2.Runs {
			for _, s := range r.Subs {
				if s.RegsAgree == nil {
					continue
				}
				seen++
				ok := true
				for _, n := range s.RegNotes {
					// "T2: ..." / "T3: ..." are disagreements of that test;
					// "T3 final values ..." is reported with T3, not judged
					if strings.HasPrefix(n, test+": ") {
						ok = false
					}
					if strings.HasPrefix(n, test) {
						notes = append(notes, fmt.Sprintf("run %d %s: %s", r.Run, s.Label, n))
					}
				}
				if ok {
					agree++
				}
			}
		}
		c := Criterion{Name: "registers agree (DCR3 and DCR4: same " + test + " verdict per sub-run)", Met: boolp(agree == seen),
			Detail: fmt.Sprintf("%d of %d sub-runs agree", agree, seen)}
		if len(notes) > 0 {
			c.Detail += "; " + strings.Join(notes, "; ")
		}
		return c, seen > 0
	}
	if c, ok := regsCrit("T2"); ok {
		t2.Criteria = append(t2.Criteria, c)
	}
	if c, ok := regsCrit("T3"); ok {
		t3.Criteria = append(t3.Criteria, c)
	}
	return t2, t3
}

// MValue is the second T2 value: min(A + 48, 200), never below MinDuty.
func MValue(a int) int {
	m := a + MOffset
	if m > MaxM {
		m = MaxM
	}
	if m < MinDuty {
		m = MinDuty
	}
	return m
}

// T1Step is the data of one mapping step.
type T1Step struct {
	Duty int           `json:"duty"`
	DCR  []int         `json:"dcr"`            // channel DCR per sample (pwm3: DCR3; -1 / empty: unavailable)
	DCR4 []int         `json:"dcr4,omitempty"` // pwm3 only: DCR4 per sample, judged like DCR (TESTSPEC §5.1)
	Fans map[int][]int `json:"fans"`           // fanN -> rpm per sample (-1 unreadable)
	// Others are the duty registers of the other channels per sample
	// (register -> value, -1 unreadable; TESTSPEC §5.1, 0.5 #33).
	Others map[int][]int `json:"other_dcr,omitempty"`
	OwnRPM float64       `json:"own_rpm"` // mean of the last 3 own-tach samples (filled by EvalT1)
	// T is the time of each sample after the write of the step in seconds
	// (due time, TESTSPEC 0.4 §2); empty = every sample is judged.
	T []float64 `json:"t_s,omitempty"`
	// Inputs are the table inputs of the other channels with an EC automatic
	// per sample (pwm channel -> value, NaN unknown; TESTSPEC 0.4 §5.1 #24).
	Inputs map[int][]float64 `json:"-"`
	// NotJudged lists the samples recorded before the settle time (filled by
	// EvalT1; TESTSPEC 0.4 §2.1, §8).
	NotJudged []string `json:"not_judged,omitempty"`
}

// judged reports whether sample i of the step is judged (TESTSPEC 0.4
// §2.1): due at or after the write plus the settle time of channel ch.
func (s T1Step) judged(ch, i int) bool {
	if i >= len(s.T) {
		return len(s.T) == 0
	}
	return Judged(ch, s.T[i])
}

// EvalT1 evaluates the mapping test of channel ch. own is the channel's tach
// (0 = none, PCIe); rest is the per-tach rpm series at rest before the
// first write (noise reference), restIn the table inputs of the other
// channels at rest (pwm channel -> values; nil: not recorded), restDCR the
// duty registers of the other channels at rest (register -> values); fans
// lists the tachs (1..3). For pwm3 the DCR criterion covers DCR3 (T1Step.DCR)
// and DCR4 (T1Step.DCR4). A DCR sample before the settle time of the channel
// is recorded, not judged (TESTSPEC §2.1).
func EvalT1(ch, own int, rest map[int][]int, restIn map[int][]float64, restDCR map[int][]int, steps []T1Step, fans []int) Result {
	res := Result{ID: "T1", Channel: ch}
	var reasons []string
	// DCR equals the written value in every judged sample of every step
	// (TESTSPEC 0.4 §5.1); a judged sample without DCR (-1) or a step
	// without judged samples leaves the criterion undecided unless another
	// sample already differs.
	type regSeries struct {
		name string
		get  func(T1Step) []int
	}
	regs := []regSeries{{"DCR", func(s T1Step) []int { return s.DCR }}}
	if ch == 3 {
		regs = []regSeries{{"DCR3", func(s T1Step) []int { return s.DCR }}, {"DCR4", func(s T1Step) []int { return s.DCR4 }}}
	}
	for i := range steps {
		steps[i].NotJudged = nil
		for k := range steps[i].T {
			if steps[i].judged(ch, k) {
				continue
			}
			var vals []string
			for _, rg := range regs {
				if v := rg.get(steps[i]); k < len(v) && v[k] >= 0 {
					vals = append(vals, fmt.Sprintf("%s %d", rg.name, v[k]))
				} else {
					vals = append(vals, rg.name+" -")
				}
			}
			steps[i].NotJudged = append(steps[i].NotJudged, fmt.Sprintf("+%.1f s: %s", steps[i].T[k], strings.Join(vals, ", ")))
		}
	}
	dcrKnown := true
	dcrOK := true
	var bad, missing []string
	for _, rg := range regs {
		for _, st := range steps {
			series := rg.get(st)
			nMiss, nJudged := 0, 0
			differs := false
			for i, v := range series {
				if !st.judged(ch, i) {
					continue
				}
				nJudged++
				switch {
				case v < 0:
					nMiss++
				case v != st.Duty && !differs:
					differs = true
					dcrOK = false
					bad = append(bad, fmt.Sprintf("step %d: %s %d", st.Duty, rg.name, v))
				}
			}
			if nJudged == 0 {
				dcrKnown = false
				missing = append(missing, fmt.Sprintf("step %d: no judged %s sample", st.Duty, rg.name))
				continue
			}
			if nMiss > 0 {
				dcrKnown = false
				missing = append(missing, fmt.Sprintf("step %d: %d of %d judged samples without %s", st.Duty, nMiss, nJudged, rg.name))
			}
		}
	}
	c := Criterion{Name: fmt.Sprintf("DCR equals the written value in every judged sample of every step (from +%.0f s after the write)", SettleS(ch))}
	if ch == 3 {
		c.Name = fmt.Sprintf("DCR3 and DCR4 equal the written value in every judged sample of every step (from +%.0f s after the write)", SettleS(ch))
	}
	switch {
	case !dcrOK:
		c.Met = boolp(false)
		c.Detail = strings.Join(append(bad, missing...), "; ")
	case dcrKnown:
		c.Met = boolp(true)
	default:
		c.Detail = "DCR not readable: " + strings.Join(missing, "; ")
	}
	res.Criteria = append(res.Criteria, c)
	// noise at rest
	noisy := []string{}
	for _, f := range fans {
		if lo, hi, ok := spread(rest[f]); ok && hi-lo > T1OtherRPM {
			noisy = append(noisy, fmt.Sprintf("fan%d %d rpm", f, hi-lo))
		}
	}
	nc := Criterion{Name: "tach noise at rest <= 50 rpm", Met: boolp(len(noisy) == 0), Detail: strings.Join(noisy, "; ")}
	res.Criteria = append(res.Criteria, nc)
	// another channel follows when its duty register leaves its rest value
	// by >= T1OtherDCR in a judged sample (TESTSPEC §5.1, 0.5 #33); a smaller
	// difference is reported: the EC automatic regulates on rpm and may
	// correct a coupled fan
	moved, small, otherMissing := []string{}, []string{}, []string{}
	movedCh := map[int]bool{}
	for _, n := range OtherRegs(ch) {
		m, ok := mean(restDCR[n])
		if !ok {
			otherMissing = append(otherMissing, fmt.Sprintf("DCR%d: no rest reference", n))
			continue
		}
		base := int(math.Round(m))
		for _, st := range steps {
			nJudged, maxDev, devVal := 0, 0, 0
			for i, v := range st.Others[n] {
				if !st.judged(ch, i) {
					continue
				}
				nJudged++
				if v < 0 {
					otherMissing = append(otherMissing, fmt.Sprintf("step %d: DCR%d sample %d", st.Duty, n, i+1))
					continue
				}
				if d := absInt(v - base); d > maxDev {
					maxDev, devVal = d, v
				}
			}
			switch {
			case nJudged == 0:
				otherMissing = append(otherMissing, fmt.Sprintf("step %d: no judged DCR%d sample", st.Duty, n))
			case maxDev >= T1OtherDCR:
				moved = append(moved, fmt.Sprintf("DCR%d %d at step %d (rest %d)", n, devVal, st.Duty, base))
				movedCh[regChannel(n)] = true
			case maxDev > 0:
				small = append(small, fmt.Sprintf("DCR%d %d at step %d (rest %d)", n, devVal, st.Duty, base))
			}
		}
	}
	oc := Criterion{Name: fmt.Sprintf("duty registers of the other channels stay within %d counts of their rest value", T1OtherDCR), Detail: strings.Join(moved, "; ")}
	switch {
	case len(moved) > 0:
		oc.Met = boolp(false)
	case len(otherMissing) == 0:
		oc.Met = boolp(true)
		if len(small) > 0 {
			oc.Detail = "reported: " + strings.Join(small, "; ")
		}
	default:
		oc.Detail = "not readable: " + strings.Join(otherMissing, "; ")
	}
	res.Criteria = append(res.Criteria, oc)
	// other tachs +-50 around their rest mean: reported, not judged (0.5 #30)
	follows := []string{}
	for _, f := range fans {
		if f == own {
			continue
		}
		base, ok := mean(rest[f])
		if !ok {
			continue
		}
		for _, st := range steps {
			for _, v := range st.Fans[f] {
				if v >= 0 && math.Abs(float64(v)-base) > T1OtherRPM {
					follows = append(follows, fmt.Sprintf("fan%d %d rpm at step %d (rest %.0f)", f, v, st.Duty, base))
					break
				}
			}
		}
	}
	res.Criteria = append(res.Criteria, Criterion{Name: "other tachs within +-50 rpm (reported, not judged)", Met: boolp(len(follows) == 0), Detail: strings.Join(follows, "; ")})
	// table input of every other channel with an EC automatic against its
	// rest reference (TESTSPEC 0.4 §5.1 #24)
	var crossed, crossedFollow []string
	inputsSeen := false
	for oc := 1; oc <= 3; oc++ {
		if oc == ch {
			continue
		}
		ref, ok := meanF(restIn[oc])
		var vals []float64
		for _, st := range steps {
			vals = append(vals, st.Inputs[oc]...)
		}
		if !ok || len(vals) == 0 {
			continue
		}
		inputsSeen = true
		if x := Crossing(oc, ref, vals); x != "" {
			c := fmt.Sprintf("table input of pwm%d crossed a step (%s)", oc, x)
			crossed = append(crossed, c)
			if movedCh[oc] {
				crossedFollow = append(crossedFollow, c)
			}
		}
	}
	xc := Criterion{Name: "table inputs of the other channels stay on their side of every step (+-2 C)", Detail: strings.Join(crossed, "; ")}
	if inputsSeen {
		xc.Met = boolp(len(crossed) == 0)
	} else {
		xc.Detail = "table inputs not recorded"
	}
	res.Criteria = append(res.Criteria, xc)
	// own tach >= 200 rpm per step, decreasing duty -> decreasing rpm
	ownOK := true
	if own > 0 {
		var short []string
		// a step whose last 3 own-tach samples are all unreadable has no
		// mean; treating it as 0 rpm would fake a drop of >= 200 rpm
		known := make([]bool, len(steps))
		for i := range steps {
			s := steps[i].Fans[own]
			if len(s) > 3 {
				s = s[len(s)-3:]
			}
			steps[i].OwnRPM, known[i] = mean(s)
			if !known[i] {
				ownOK = false
				short = append(short, fmt.Sprintf("step %d: own tach not readable", steps[i].Duty))
			}
			if i == 0 || !known[i] || !known[i-1] {
				continue
			}
			d := steps[i-1].OwnRPM - steps[i].OwnRPM
			if d < T1OwnStepRPM {
				ownOK = false
				short = append(short, fmt.Sprintf("%d->%d: %.0f rpm", steps[i-1].Duty, steps[i].Duty, d))
			}
		}
		res.Criteria = append(res.Criteria, Criterion{Name: "own tach changes >= 200 rpm per step (downwards)", Met: boolp(ownOK), Detail: strings.Join(short, "; ")})
	}
	if ch == 3 {
		// DCR3 vs DCR4 sample by sample: reported (the verdict comes from
		// each register against the written value above)
		var diff []string
		compared := 0
		for _, st := range steps {
			for i := 0; i < len(st.DCR) && i < len(st.DCR4); i++ {
				if !st.judged(ch, i) {
					continue // the EC ramps DCR3, then DCR4 (TESTSPEC 0.4 §2.1)
				}
				a, b := st.DCR[i], st.DCR4[i]
				if a < 0 || b < 0 {
					continue
				}
				compared++
				if a != b {
					diff = append(diff, fmt.Sprintf("step %d sample %d: DCR3 %d, DCR4 %d", st.Duty, i+1, a, b))
				}
			}
		}
		rc := Criterion{Name: "DCR3 and DCR4 equal in every judged sample (reported)", Detail: strings.Join(diff, "; ")}
		if compared > 0 {
			rc.Met = boolp(len(diff) == 0)
		}
		res.Criteria = append(res.Criteria, rc)
	}
	// verdict in the order of TESTSPEC 0.4 §5.1
	switch {
	case !dcrOK:
		res.Verdict = Fail
		if ch == 3 {
			reasons = append(reasons, "DCR3 or DCR4 differs from the written value")
		} else {
			reasons = append(reasons, "DCR differs from the written value")
		}
	case len(moved) > 0 && len(crossed) == 0:
		res.Verdict = Fail
		reasons = append(reasons, "another channel's duty register follows the written channel: "+strings.Join(moved, "; "))
	case len(crossedFollow) > 0:
		// TESTSPEC §5.1 #24: the other channel's EC automatic may have
		// followed its own input, not the written channel
		res.Verdict = Inconclusive
		reasons = append(reasons, strings.Join(crossedFollow, "; ")+"; another channel's duty register moved")
	case !dcrKnown || len(otherMissing) > 0:
		res.Verdict = Inconclusive
		reasons = append(reasons, "DCR not readable: the verdict needs the duty registers (driver 0.2.1+sr1, debug_regs=1)")
	case len(noisy) > 0:
		res.Verdict = Inconclusive
		reasons = append(reasons, "tach noise > 50 rpm at rest")
	case !ownOK:
		res.Verdict = Inconclusive
		reasons = append(reasons, "own tach moved less than 200 rpm in a step, or was not readable at the end of a step")
	case len(crossed) > 0:
		// TESTSPEC 0.4 §5.1 #24, operator decision 2026-10-17: a crossing
		// makes T1 INCONCLUSIVE even with every other tach within +-50 rpm
		res.Verdict = Inconclusive
		reasons = append(reasons, strings.Join(crossed, "; "))
	default:
		res.Verdict = Pass
		switch {
		case ch == 3:
			reasons = append(reasons, "DCR3 and DCR4 equal to the written value at every step, own tach moved >= 200 rpm per step, other channels' duty registers stayed")
		case own > 0:
			reasons = append(reasons, "DCR equal to the written value at every step, own tach moved >= 200 rpm per step, other channels' duty registers stayed")
		default:
			reasons = append(reasons, "DCR equal to the written value at every step, other channels' duty registers stayed (channel without tach)")
		}
	}
	if len(follows) > 0 {
		reasons = append(reasons, "reported, not judged: tach outside +-50 rpm (airflow coupling unless a duty register moved): "+strings.Join(follows, "; "))
	}
	res.Reasons = reasons
	res.Values = steps
	return res
}

func spread(v []int) (lo, hi int, ok bool) {
	for _, x := range v {
		if x < 0 {
			continue
		}
		if !ok || x < lo {
			lo = x
		}
		if !ok || x > hi {
			hi = x
		}
		ok = true
	}
	return
}

func mean(v []int) (float64, bool) {
	sum, n := 0, 0
	for _, x := range v {
		if x >= 0 {
			sum += x
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return float64(sum) / float64(n), true
}

func meanF(v []float64) (float64, bool) {
	sum, n := 0.0, 0
	for _, x := range v {
		if !math.IsNaN(x) {
			sum += x
			n++
		}
	}
	if n == 0 {
		return math.NaN(), false
	}
	return sum / float64(n), true
}

// NotImplemented is the result of a test this tool cannot run yet.
func NotImplemented(id string, ch int, reason string) Result {
	return Result{ID: id, Channel: ch, Verdict: Inconclusive, Reasons: []string{reason}}
}

// NotApplicableResult is a test that does not apply to channel ch.
func NotApplicableResult(id string, ch int, reason string) Result {
	return Result{ID: id, Channel: ch, Verdict: NotApplicable, Reasons: []string{reason}}
}

// Reasons of T4/T5, which this tool does not evaluate yet (TESTSPEC 0.4
// §5.5 wants `report` to; listed in spec_deviations).
const (
	ReasonT4T5Manual   = "not implemented, run manually per §5.5"
	ReasonHDDBelowStep = "HDD input EC 0x06 below 34 C"
)

// T4T5Result is the result of T4 or T5 for channel ch from the samples of
// this run (TESTSPEC §5, §6): N/A on pwm4 (no EC automatic); on pwm3
// INCONCLUSIVE "HDD input EC 0x06 below 34 C" when every recorded EC 0x06
// value lies below the table step (the input cannot have crossed it);
// otherwise INCONCLUSIVE "not implemented, run manually per §5.5".
func T4T5Result(id string, ch int, samples []Sample) Result {
	if ch == PCIeChannel {
		return NotApplicableResult(id, ch, ReasonNoPCIeAuto)
	}
	if ch == 3 {
		hi, n := math.Inf(-1), 0
		for _, s := range samples {
			if !math.IsNaN(s.ECAmb) {
				hi, n = math.Max(hi, s.ECAmb), n+1
			}
		}
		if n > 0 && hi < HDDInputStepC {
			r := NotImplemented(id, ch, fmt.Sprintf("%s (max %.1f C in %d samples of this run; table step at %.0f C, a crossing by 2 C needs >= %.0f C; TESTSPEC §6)",
				ReasonHDDBelowStep, hi, n, HDDInputStepC, HDDInputStepC+2))
			r.Values = map[string]any{"ec_amb_max": hi, "samples": n}
			return r
		}
		if n == 0 {
			return NotImplemented(id, ch, ReasonT4T5Manual+" (EC 0x06 not recorded in this run)")
		}
	}
	return NotImplemented(id, ch, ReasonT4T5Manual)
}

// G4Result is the evaluation of the G4 window.
type G4Result struct {
	Pass  bool
	Parts []string
	// Limit says which abort-limit sensors (tctl, nvme, hdd) had a value
	// in the window.
	Limit map[string]bool
}

// EvalG4 evaluates the G4 gate window (TESTSPEC §3, 0.5 #29): per input (EC
// system 0x04, EC ambient 0x06, HDD max) the mean of the last GateTrendS
// seconds differs from the mean of the first by < GateTempDeltaC and max -
// min is <= GateTempDeltaC (these sensors report whole degrees, so one step
// without a trend is allowed), and every abort-limit sensor (Tctl, NVMe,
// HDD) has a value in the window. Tctl is not a stability criterion (safety
// limit only, §4).
func EvalG4(rows []Sample) G4Result {
	g := G4Result{Pass: true, Limit: map[string]bool{}}
	// TESTSPEC §4 limits are only enforced when their sensor has a value:
	// a missing one blocks the writing tests instead of silently skipping
	// its limit (drivetemp not loaded, no NVMe hwmon, ...).
	for _, ls := range []struct {
		name string
		get  func(Sample) float64
	}{{"tctl", func(s Sample) float64 { return s.Tctl }}, {"nvme", func(s Sample) float64 { return s.NVMeMax }}, {"hdd", func(s Sample) float64 { return s.HDDMax }}} {
		seen := false
		for _, s := range rows {
			seen = seen || !math.IsNaN(ls.get(s))
		}
		g.Limit[ls.name] = seen
		if !seen {
			g.Parts = append(g.Parts, "limit sensor "+ls.name+" not available")
			g.Pass = false
		}
	}
	for _, c := range []struct {
		name string
		max  float64
		get  func(Sample) float64
	}{
		{"system (ec 0x04)", GateTempDeltaC, func(s Sample) float64 { return s.ECSys }},
		{"ambient (ec 0x06)", GateTempDeltaC, func(s Sample) float64 { return s.ECAmb }},
		{"hdd", GateTempDeltaC, func(s Sample) float64 { return s.HDDMax }},
	} {
		lo, hi, n := math.Inf(1), math.Inf(-1), 0
		var first, last []float64
		t0, t1 := rows[0].TS, rows[len(rows)-1].TS
		for _, s := range rows {
			v := c.get(s)
			if math.IsNaN(v) {
				continue
			}
			lo, hi, n = math.Min(lo, v), math.Max(hi, v), n+1
			if s.TS <= t0+GateTrendS {
				first = append(first, v)
			}
			if s.TS >= t1-GateTrendS {
				last = append(last, v)
			}
		}
		if n == 0 {
			if c.name != "hdd" { // hdd is reported above
				g.Parts = append(g.Parts, c.name+" not available")
			}
			g.Pass = false
			continue
		}
		a, okA := meanF(first)
		b, okB := meanF(last)
		if !okA || !okB {
			g.Parts = append(g.Parts, c.name+" has no value in the first or last 60 s")
			g.Pass = false
			continue
		}
		span, trend := hi-lo, b-a
		ok := span <= c.max+1e-9 && math.Abs(trend) < c.max
		if !ok {
			g.Pass = false
		}
		verdict := "ok"
		if !ok {
			verdict = "not stable"
		}
		g.Parts = append(g.Parts, fmt.Sprintf("%s span %.1f C, trend %+.2f C: %s", c.name, span, trend, verdict))
	}
	return g
}
