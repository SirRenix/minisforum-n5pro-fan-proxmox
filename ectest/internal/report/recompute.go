// Package report recomputes every verdict of a bundle from samples.csv,
// events.log and meta.json with the rules of internal/spec, flags any
// difference to the stored verdict.json, and renders a self-contained HTML
// report (inline CSS and SVG, no script, no URL).
package report

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/logger"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/runner"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// Meta is the part of meta.json the report needs.
type Meta struct {
	Command        string            `json:"command"`
	Channel        int               `json:"channel"`
	Mode           string            `json:"mode"`
	ToolVersion    string            `json:"tool_version"`
	SpecVersion    string            `json:"spec_version"`
	BootType       string            `json:"boot_type"`
	Aborted        string            `json:"aborted"`
	Identity       identity.Identity `json:"identity"`
	Gates          []spec.Gate       `json:"gates"`
	SpecDeviations []string          `json:"spec_deviations"`
	RestoreValues  map[string]string `json:"restore_values"`
	RestoreFailed  []string          `json:"restore_failed"`
	RestoreWarn    []string          `json:"restore_warnings"`
	Provenance     map[string]any    `json:"provenance"`
	Label          string            `json:"label"`
	Incomplete     []string          `json:"incomplete"`
	Started        string            `json:"started"`
	Finished       string            `json:"finished"`
	Log            map[string]any    `json:"log"`
	// Provisional: the meta.json written right after the bundle was created;
	// the run ended before it replaced it (killed).
	Provisional bool `json:"provisional"`
}

// StoredVerdict is verdict.json.
type StoredVerdict struct {
	SpecVersion string        `json:"spec_version"`
	Channel     int           `json:"channel"`
	Command     string        `json:"command"`
	Tests       []spec.Result `json:"tests"`
}

// Mismatch is a recomputed verdict or gate result that differs from the
// stored one.
type Mismatch struct {
	ID         string
	Stored     spec.Verdict
	Recomputed spec.Verdict
	// Gate: the stored value is a gate result in meta.json (else a verdict
	// in verdict.json).
	Gate bool
}

// Source names the file of the stored value.
func (m Mismatch) Source() string {
	if m.Gate {
		return "meta.json"
	}
	return "verdict.json"
}

// notRecomputed and missing mark one side of a mismatch.
const (
	notRecomputed spec.Verdict = "(not recomputed)"
	missing       spec.Verdict = "(missing)"
)

// Result of a recomputation.
type Result struct {
	Meta       Meta
	Stored     StoredVerdict
	Tests      []spec.Result
	Mismatches []Mismatch
	Notes      []string
	// SubRuns are the T2/T3 sub-runs with their samples (charts).
	SubRuns []SubRun
	Events  []Event
	// Log is the F8b summary of a log run (counts recomputed with the
	// annotations of today), LogOpen its unexplained events.
	Log     *logger.Summary
	LogOpen []logger.Event
}

// SubRun is one T2 sub-run rebuilt from the bundle.
type SubRun struct {
	Rep     int
	Label   string
	Written int
	Regs    map[int][]spec.DCRSample
	// Auto is A per judged register (the |W-A| < 16 rule of T2 uses it).
	Auto map[int]int
	Eval spec.SubResult
}

// Event is one events.log line with its run time.
type Event struct {
	TS   float64
	Text string
	Line string
}

var eventRe = regexp.MustCompile(`^\S+ \[t_s=([0-9.]+)\] (.*)$`)

// ParseEvents splits events.log lines into run time and text.
func ParseEvents(lines []string) []Event {
	var out []Event
	for _, l := range lines {
		m := eventRe.FindStringSubmatch(l)
		if m == nil {
			out = append(out, Event{TS: -1, Text: l, Line: l})
			continue
		}
		ts, _ := strconv.ParseFloat(m[1], 64)
		out = append(out, Event{TS: ts, Text: m[2], Line: l})
	}
	return out
}

func hasEvent(ev []Event, text string) bool {
	for _, e := range ev {
		if e.Text == text {
			return true
		}
	}
	return false
}

func phaseSamples(smp []spec.Sample, phase string) []spec.Sample {
	var out []spec.Sample
	for _, s := range smp {
		if s.Phase == phase {
			out = append(out, s)
		}
	}
	return out
}

// Recompute rebuilds the verdicts of a loaded bundle.
func Recompute(l *bundle.Loaded) (*Result, error) {
	res := &Result{Events: ParseEvents(l.Events)}
	if len(l.MetaRaw) == 0 {
		res.Notes = append(res.Notes, "meta.json missing: the run ended before it finished (killed); verdicts cannot be recomputed")
	} else if err := json.Unmarshal(l.MetaRaw, &res.Meta); err != nil {
		return nil, fmt.Errorf("meta.json: %w", err)
	}
	if len(l.VerdictRaw) > 0 {
		if err := json.Unmarshal(l.VerdictRaw, &res.Stored); err != nil {
			return nil, fmt.Errorf("verdict.json: %w", err)
		}
	}
	m := res.Meta
	ch := m.Channel
	smp := l.Samples
	if m.SpecVersion != "" && m.SpecVersion != spec.SpecVersion {
		res.Notes = append(res.Notes, fmt.Sprintf("bundle written under TESTSPEC %s; recomputed with the rules of %s (differences are expected where the rules changed)", m.SpecVersion, spec.SpecVersion))
	}
	if m.Provisional {
		res.Notes = append(res.Notes, "meta.json is the provisional one written at the start: the run ended before it finished (killed); verdicts cannot be recomputed")
		return res, nil
	}
	switch m.Command {
	case "map", "handback":
		res.Tests = append(res.Tests, spec.T0(spec.T0Values(m.Identity, m.ToolVersion, m.BootType), m.BootType))
		res.Mismatches = append(res.Mismatches, checkGates(m, smp)...)
		if m.Command == "map" {
			res.Tests = append(res.Tests, recomputeT1(m, smp, res.Events))
		} else {
			t2, t3, subs := recomputeT2(m, smp, res.Events)
			res.SubRuns = subs
			res.Tests = append(res.Tests, t2, t3, spec.T4T5Result("T4", ch, smp), spec.T4T5Result("T5", ch, smp))
		}
	case "snapshot":
		for _, t := range res.Stored.Tests {
			if t.ID == "T6" {
				res.Tests = append(res.Tests, spec.Result{ID: "T6", Verdict: spec.Reported, Reasons: []string{"values only (TESTSPEC §5.4)"}})
			}
		}
	case "log":
		// F8b: the tool computes no verdict (explanations are the
		// operator's); the report shows the criteria counts
		if st, err := logger.LoadState(l.Dir); err == nil {
			until := time.Unix(st.EndEpoch, 0)
			if !st.Finished {
				until = time.Unix(st.LastSampleWall, 0)
				res.Notes = append(res.Notes, "log run not finished: counts up to the last sample")
			}
			s := logger.Summarize(l.Dir, st, time.Unix(st.StartEpoch, 0), until)
			res.Log = &s
			ann := logger.ParseAnnotations(l.Dir)
			for _, e := range logger.ParseEventLog(l.Dir) {
				if !logger.Explained(e, ann) {
					res.LogOpen = append(res.LogOpen, e)
				}
			}
		} else {
			res.Notes = append(res.Notes, "log run without state.json: "+err.Error())
		}
	case "":
	default:
		res.Notes = append(res.Notes, "unknown command "+strconv.Quote(m.Command))
	}
	res.Mismatches = append(res.Mismatches, compareTests(res.Stored.Tests, res.Tests)...)
	return res, nil
}

// compareTests compares stored and recomputed results in both directions:
// test verdicts, and for T2/T3 every run verdict and every sub-run verdict.
func compareTests(stored, recomputed []spec.Result) []Mismatch {
	var out []Mismatch
	st := map[string]spec.Result{}
	for _, t := range stored {
		st[t.ID] = t
	}
	rc := map[string]bool{}
	for _, t := range recomputed {
		rc[t.ID] = true
		s, ok := st[t.ID]
		if !ok {
			out = append(out, Mismatch{ID: t.ID, Stored: missing, Recomputed: t.Verdict})
			continue
		}
		if s.Verdict != t.Verdict {
			out = append(out, Mismatch{ID: t.ID, Stored: s.Verdict, Recomputed: t.Verdict})
		}
		out = append(out, compareRuns(t.ID, s.Runs, t.Runs)...)
	}
	for _, t := range stored {
		if !rc[t.ID] {
			out = append(out, Mismatch{ID: t.ID, Stored: t.Verdict, Recomputed: notRecomputed})
		}
	}
	return out
}

func compareRuns(id string, stored, recomputed []spec.RunResult) []Mismatch {
	var out []Mismatch
	sr := map[int]spec.RunResult{}
	for _, r := range stored {
		sr[r.Run] = r
	}
	seen := map[int]bool{}
	for _, r := range recomputed {
		seen[r.Run] = true
		rid := fmt.Sprintf("%s run %d", id, r.Run)
		s, ok := sr[r.Run]
		if !ok {
			out = append(out, Mismatch{ID: rid, Stored: missing, Recomputed: r.Verdict})
			continue
		}
		if s.Verdict != r.Verdict {
			out = append(out, Mismatch{ID: rid, Stored: s.Verdict, Recomputed: r.Verdict})
		}
		for i := 0; i < len(r.Subs) || i < len(s.Subs); i++ {
			switch {
			case i >= len(s.Subs):
				out = append(out, Mismatch{ID: fmt.Sprintf("%s %s", rid, r.Subs[i].Label), Stored: missing, Recomputed: r.Subs[i].T2})
			case i >= len(r.Subs):
				out = append(out, Mismatch{ID: fmt.Sprintf("%s %s", rid, s.Subs[i].Label), Stored: s.Subs[i].T2, Recomputed: notRecomputed})
			default:
				a, b := s.Subs[i], r.Subs[i]
				if a.T2 != b.T2 {
					out = append(out, Mismatch{ID: fmt.Sprintf("%s %s T2", rid, b.Label), Stored: a.T2, Recomputed: b.T2})
				}
				if a.T3 != b.T3 {
					out = append(out, Mismatch{ID: fmt.Sprintf("%s %s T3", rid, b.Label), Stored: a.T3, Recomputed: b.T3})
				}
			}
		}
	}
	for _, r := range stored {
		if !seen[r.Run] {
			out = append(out, Mismatch{ID: fmt.Sprintf("%s run %d", id, r.Run), Stored: r.Verdict, Recomputed: notRecomputed})
		}
	}
	return out
}

// checkGates recomputes G3 and G4 from the gate window and compares them
// with meta.json; a difference is a mismatch like a verdict.
func checkGates(m Meta, smp []spec.Sample) []Mismatch {
	var out []Mismatch
	gate := phaseSamples(smp, spec.PhaseGate)
	for _, g := range m.Gates {
		if (g.ID != "G3" && g.ID != "G4") || (g.Result != "PASS" && g.Result != "FAIL") {
			continue
		}
		// a gate that failed because the window was not sampled or not
		// completed (abort) is FAIL whatever the values say
		if g.Result == "FAIL" && (strings.HasPrefix(g.Detail, "not sampled") || strings.HasPrefix(g.Detail, "window not completed")) {
			if g.ID == "G4" || len(gate) == 0 {
				continue
			}
		}
		if len(gate) == 0 {
			out = append(out, Mismatch{ID: g.ID, Stored: spec.Verdict(g.Result), Recomputed: "(no gate samples)", Gate: true})
			continue
		}
		got := "FAIL"
		switch g.ID {
		case "G3":
			got, _ = runner.G3(gate[0])
		case "G4":
			if spec.EvalG4(gate).Pass {
				got = "PASS"
			}
		}
		if got != g.Result {
			out = append(out, Mismatch{ID: g.ID, Stored: spec.Verdict(g.Result), Recomputed: spec.Verdict(got), Gate: true})
		}
	}
	return out
}

func chReg(ch int) int {
	n, ok := n5driver.ChannelDCR(ch)
	if !ok {
		return 0
	}
	return n
}

func recomputeT1(m Meta, smp []spec.Sample, ev []Event) spec.Result {
	ch := m.Channel
	if !hasEvent(ev, runner.EventT1Complete) {
		switch {
		case m.Aborted != "":
			return spec.NotImplemented("T1", ch, "aborted: "+m.Aborted)
		default:
			return spec.NotImplemented("T1", ch, "not run (no complete T1 in the bundle)")
		}
	}
	rest := map[int][]int{}
	restIn := map[int][]float64{}
	for _, s := range phaseSamples(smp, spec.PhaseT1Rest) {
		for i, f := range s.Fan {
			rest[i+1] = append(rest[i+1], f)
		}
		runner.AddInputs(restIn, ch, s)
	}
	var steps []spec.T1Step
	for _, d := range spec.T1Steps {
		st := spec.T1Step{Duty: d, Fans: map[int][]int{}, Inputs: map[int][]float64{}}
		// the step's samples are timed from its write event (TESTSPEC 0.4
		// §2.1), as in the runner
		tws, ok := writeTS(ev, ch, d)
		for _, s := range phaseSamples(smp, spec.PhaseT1Step(d)) {
			runner.AddT1Sample(&st, ch, s, tws)
		}
		if !ok {
			st.T = nil // no write event: every sample is judged
		}
		steps = append(steps, st)
	}
	return spec.EvalT1(ch, runner.TachOf(ch), rest, restIn, steps, []int{1, 2, 3})
}

// writeTS is the run time of the first "write pwmC=D" event.
func writeTS(ev []Event, ch, duty int) (float64, bool) {
	w := runner.EventWrite(ch, duty)
	for _, e := range ev {
		if e.TS >= 0 && isWrite(e.Text, w) {
			return e.TS, true
		}
	}
	return 0, false
}

// isWrite matches a write event text (the tag in brackets may follow).
func isWrite(text, w string) bool {
	return text == w || strings.HasPrefix(text, w+" [")
}

var writeEventRe = regexp.MustCompile(`^write pwm[1-4]=[0-9]+( \[.*\])?$`)

var autoEventRe = regexp.MustCompile(`^T2 r([0-9]+) ([HM]) auto$`)

func recomputeT2(m Meta, smp []spec.Sample, ev []Event) (spec.Result, spec.Result, []SubRun) {
	ch := m.Channel
	if ch == spec.PCIeChannel {
		return spec.NotApplicableResult("T2", ch, spec.ReasonNoPCIeAuto), spec.NotApplicableResult("T3", ch, spec.ReasonNoPCIeAuto), nil
	}
	regs := spec.JudgedRegs(ch)
	pre := map[int][]spec.DCRSample{}
	var preIn []float64
	for _, s := range phaseSamples(smp, spec.PhaseT2Pre) {
		for _, n := range regs {
			if v := runner.RegDCR(s, n); v >= 0 {
				pre[n] = append(pre[n], spec.DCRSample{T: s.TS, DCR: v})
			}
		}
		preIn = append(preIn, spec.TableInput(ch, s))
	}
	if len(pre[regs[0]]) == 0 {
		reason := "not run (no T2 data in the bundle)"
		if m.Aborted != "" {
			reason = "aborted: " + m.Aborted
		}
		return spec.NotImplemented("T2", ch, reason), spec.NotImplemented("T3", ch, reason), nil
	}
	autoA, _ := runner.AutoValues(pre, regs)
	a := autoA[regs[0]]
	mv := spec.MValue(a)
	preCross := runner.PreCrossing(ch, preIn)
	var runs [][]spec.SubResult
	var subs []SubRun
	var holdDev []string
	cur := 0
	lastWrite, haveWrite := 0.0, false
	for _, e := range ev {
		if e.TS >= 0 && writeEventRe.MatchString(e.Text) {
			lastWrite, haveWrite = e.TS, true
			continue
		}
		mm := autoEventRe.FindStringSubmatch(e.Text)
		if mm == nil || e.TS < 0 {
			continue
		}
		rep, _ := strconv.Atoi(mm[1])
		label := mm[2]
		written := 255
		if label == "M" {
			written = mv
		}
		hold := phaseSamples(smp, spec.PhaseT2Hold(rep, label))
		for _, s := range hold {
			if s.Written >= 0 {
				written = s.Written
				break
			}
		}
		var in []float64
		for _, s := range hold {
			in = append(in, spec.TableInput(ch, s))
			if haveWrite {
				if d := runner.HoldDeviation(ch, rep, label, written, s, lastWrite); d != "" {
					holdDev = append(holdDev, d)
				}
			}
		}
		series := map[int][]spec.DCRSample{}
		for _, s := range phaseSamples(smp, spec.PhaseT2Auto(rep, label)) {
			in = append(in, spec.TableInput(ch, s))
			for _, n := range regs {
				if v := runner.RegDCR(s, n); v >= 0 {
					// the same arithmetic as the runner: both t_s on their
					// stored ms values
					series[n] = append(series[n], spec.DCRSample{T: spec.SinceMS(s.TS, e.TS), DCR: v})
				}
			}
		}
		hr := runner.HandbackRuns(label, written, regs, series, autoA)
		runner.MarkCrossing(hr, ch, preCross, in)
		sr := spec.EvalHandbackRegs(hr)
		if rep != cur {
			runs = append(runs, nil)
			cur = rep
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], sr)
		subs = append(subs, SubRun{Rep: rep, Label: label, Written: written, Regs: series, Auto: autoA, Eval: sr})
	}
	t2, t3 := runner.FinishHandback(ch, runs, m.Aborted, a, mv, regs, autoA, holdDev)
	return t2, t3, subs
}

// SortedKeys is a helper for stable output.
func SortedKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
