// Package runner executes the writing procedures of TESTSPEC (T1 mapping,
// T2/T3 hand-back) and the G3/G4 gate window against an Env: the real N5 Pro
// through hwmon sysfs (cmd/n5-ectest) or the simulator of --dry-run
// (internal/sim). It records every sample and event, enforces the limits of
// TESTSPEC §4 at every sample, never writes a duty below 100 and restores
// every channel it touched (Close) on every exit path of its caller.
package runner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// Readings are the environment values of one sample; NaN = not available,
// Fan[n] = -1 unreadable.
type Readings struct {
	Fan     map[int]int // fan1..fan3
	Tctl    float64
	NVMeMax float64
	HDDMax  float64
	ECCPU   float64
	ECSys   float64
	ECBoard float64
	ECAmb   float64
	Load1   float64
}

// Env is what a run needs from the machine.
type Env interface {
	Now() time.Time
	// Sleep waits d; false when ctx is done.
	Sleep(ctx context.Context, d time.Duration) bool
	EnterManual(ch int) error
	WriteDuty(ch, duty int) error
	// Auto sends the channel's EC automatic command (pwmN_enable = 2).
	Auto(ch int) error
	// Restore puts the channel into its restore value (TESTSPEC §4).
	Restore(ch int) error
	// DCR reads the duty registers; an error means "not available".
	DCR() (n5driver.DCR, error)
	Read() Readings
}

// Config of one run.
type Config struct {
	Channel int   // pwm channel under test
	OwnTach int   // its tach (0 = none)
	Tachs   []int // all tachs (1..3)
	Hold    time.Duration
	Sample  time.Duration
	Observe time.Duration // T3 limit; the log adds spec.SettleHoldS
	// Pre is the window that determines A (0: Observe).
	Pre    time.Duration
	Window time.Duration // G4 window
	// OnlineCPUs is the number of online CPUs (G3); 0: not readable.
	OnlineCPUs int
	Repeat     int // T2/T3 repetitions
	// Guard, when set, is asked at every sample whether another pwm writer
	// became active (G1 for the whole run); a non-empty answer aborts.
	Guard func() string
}

// PreWindow is the window that determines A.
func (c Config) PreWindow() time.Duration {
	if c.Pre > 0 {
		return c.Pre
	}
	return c.Observe
}

// HandbackObserve is how long a T2 sub-run logs after the auto command:
// Observe plus SettleHoldS, so a steady state that starts at Observe can
// still be seen for its full 120 s (TESTSPEC §5.2).
func (c Config) HandbackObserve() time.Duration {
	return c.Observe + spec.SettleHoldS*time.Second
}

// ErrRestoreUnverified is wrapped by Env.Restore when the restore value was
// written but the read-back of the EC register differs. Close counts the
// channel as restored and reports the difference separately.
var ErrRestoreUnverified = errors.New("restore value written, EC register differs")

// lostAfter is the number of consecutive samples without a value after
// which a limit sensor that was available at the gate aborts the run.
const lostAfter = 3

// SpecConfig returns the TESTSPEC timing (version spec.SpecVersion) for
// channel ch.
func SpecConfig(ch, ownTach int, tachs []int) Config {
	return Config{Channel: ch, OwnTach: ownTach, Tachs: tachs,
		Hold: spec.T1StepHoldS * time.Second, Sample: spec.SampleS * time.Second,
		Observe: time.Duration(spec.T3LimitS(ch)) * time.Second, Pre: time.Duration(spec.PreWindowS(ch)) * time.Second,
		Window: spec.GateWindowS * time.Second, Repeat: spec.RunsRequired}
}

// TachOf is the tach of pwm channel ch (0 = none: PCIe).
func TachOf(ch int) int {
	if ch >= 1 && ch <= 3 {
		return ch
	}
	return 0
}

// Runner executes the writing tests against an Env.
type Runner struct {
	Env     Env
	Cfg     Config
	Samples []spec.Sample
	Events  []string
	Abort   string // why the run stopped early ("" = it did not)
	// LimitSensors says which abort-limit sensors (tctl, nvme, hdd) had a
	// value in the gate window; Gates fills it.
	LimitSensors map[string]bool
	// RestoreWarnings lists channels whose restore value was written but
	// whose EC register differs afterwards (ErrRestoreUnverified).
	RestoreWarnings []string
	// OnSample and OnEvent stream every sample and event (bundle files are
	// written while the run goes on, so a killed worker leaves its data).
	OnSample func(spec.Sample)
	OnEvent  func(string)
	// BeforeWrite is called before the first write of the run (the guardian
	// state file is written there).
	BeforeWrite func() error
	// Tag is appended to every write, auto and restore event: tool, version
	// and driver version, so EC and driver statements stay separable.
	Tag string
	// RestoreOverrides are restore values that differ from the channel's
	// restore value, with the reason (TESTSPEC 0.4 §4, #13: after an abort
	// on a temperature limit the channel of that sensor is set to 255 and
	// stays there).
	RestoreOverrides map[int]string
	// BeforeHot is called before the channel of a tripped temperature limit
	// is set to 255 (the guardian state file records 255 there); an error is
	// logged and 255 is written anyway.
	BeforeHot func(ch int) error

	start      time.Time
	touched    map[int]bool
	restoreErr map[int]string
	nanRun     map[string]int
	dcrOK      bool
	dcrErr     error
	wrote      bool
	hot        int    // channel of the sensor that tripped a temperature limit (0 = none)
	hotWhy     string // that limit
}

// NewRunner starts the run clock.
func NewRunner(env Env, cfg Config) *Runner {
	r := &Runner{Env: env, Cfg: cfg, start: env.Now(), touched: map[int]bool{}, restoreErr: map[int]string{}, nanRun: map[string]int{}}
	_, r.dcrErr = env.DCR()
	r.dcrOK = r.dcrErr == nil
	return r
}

// Start is the run clock origin (t_s = 0).
func (r *Runner) Start() time.Time { return r.start }

// DCRAvailable reports whether the duty registers can be read.
func (r *Runner) DCRAvailable() (bool, error) { return r.dcrOK, r.dcrErr }

// FormatEvent renders one events.log line: UTC time, seconds since the run
// started, text.
func FormatEvent(t time.Time, ts float64, text string) string {
	return fmt.Sprintf("%s [t_s=%.3f] %s", t.UTC().Format(time.RFC3339), ts, text)
}

func (r *Runner) event(format string, args ...any) { r.eventAt(format, args...) }

// eventAt records an event and returns the time stamped on it.
func (r *Runner) eventAt(format string, args ...any) time.Time {
	now := r.Env.Now()
	line := FormatEvent(now, now.Sub(r.start).Seconds(), fmt.Sprintf(format, args...))
	r.Events = append(r.Events, line)
	if r.OnEvent != nil {
		r.OnEvent(line)
	}
	return now
}

// Event records an event from the caller (gate results, operator input).
func (r *Runner) Event(format string, args ...any) { r.event(format, args...) }

// Sample records one row and checks the safety limits of TESTSPEC §4.
// The returned string is the abort reason ("" = within limits).
func (r *Runner) Sample(phase string, written int) (spec.Sample, string) {
	return r.sampleAt(phase, written, time.Time{})
}

// sampleAt is Sample for a sample due at due (zero: no due time). A read
// within spec.DueToleranceS of its due time gets the due time as t_s
// (TESTSPEC 0.4 §2, #11); the real read time stays in t_iso. A later read
// keeps its real time and is logged.
func (r *Runner) sampleAt(phase string, written int, due time.Time) (spec.Sample, string) {
	// the time stamp belongs to the duty registers (the judged value): they
	// are read first, the slower hwmon reads follow
	now := r.Env.Now()
	ts := now.Sub(r.start).Seconds()
	if !due.IsZero() {
		late := now.Sub(due).Seconds()
		if math.Abs(late) <= spec.DueToleranceS {
			ts = due.Sub(r.start).Seconds()
		} else {
			r.event("sample %s %.3f s off its due time t_s=%.3f: stored at its real time", phase, late, due.Sub(r.start).Seconds())
		}
	}
	s := spec.NewSample(now, ts, phase, r.Cfg.Channel, written)
	var dcr n5driver.DCR
	haveDCR := false
	if r.dcrOK {
		if d, err := r.Env.DCR(); err == nil {
			dcr, haveDCR = d, true
			for n := 1; n <= 5; n++ {
				s.DCR[n-1] = d.Reg(n)
			}
		}
	}
	rd := r.Env.Read()
	s.ECCPU, s.ECSys, s.ECBoard, s.ECAmb = rd.ECCPU, rd.ECSys, rd.ECBoard, rd.ECAmb
	s.HDDMax, s.NVMeMax, s.Tctl, s.Load1 = rd.HDDMax, rd.NVMeMax, rd.Tctl, rd.Load1
	for n := 1; n <= 3; n++ {
		if v, ok := rd.Fan[n]; ok {
			s.Fan[n-1] = v
		}
	}
	r.Samples = append(r.Samples, s)
	if r.OnSample != nil {
		r.OnSample(s)
	}
	if r.Cfg.Guard != nil {
		if why := r.Cfg.Guard(); why != "" {
			return s, "another pwm writer became active: " + why
		}
	}
	for _, ls := range []struct {
		name string
		v    float64
	}{{"tctl", rd.Tctl}, {"nvme", rd.NVMeMax}, {"hdd", rd.HDDMax}} {
		if !r.LimitSensors[ls.name] {
			continue
		}
		if !math.IsNaN(ls.v) {
			r.nanRun[ls.name] = 0
			continue
		}
		r.nanRun[ls.name]++
		if r.nanRun[ls.name] >= lostAfter {
			return s, fmt.Sprintf("limit sensor %s unreadable in %d consecutive samples (its limit cannot be enforced)", ls.name, lostAfter)
		}
	}
	// a temperature limit names the channel of its sensor (TESTSPEC 0.4 §4,
	// #13: Tctl -> pwm1, NVMe -> pwm2, HDD -> pwm3); the first one counts
	hot := func(ch int, why string) (spec.Sample, string) {
		if r.hot == 0 && r.Abort == "" {
			r.hot, r.hotWhy = ch, why
		}
		return s, why
	}
	switch {
	case !math.IsNaN(rd.Tctl) && rd.Tctl >= spec.LimitTctlC:
		return hot(1, fmt.Sprintf("CPU Tctl %.1f C >= %.0f C", rd.Tctl, spec.LimitTctlC))
	case !math.IsNaN(rd.NVMeMax) && rd.NVMeMax >= spec.LimitNVMeC:
		return hot(2, fmt.Sprintf("NVMe %.1f C >= %.0f C", rd.NVMeMax, spec.LimitNVMeC))
	case !math.IsNaN(rd.HDDMax) && rd.HDDMax >= spec.LimitHDDC:
		return hot(3, fmt.Sprintf("HDD %.1f C >= %.0f C", rd.HDDMax, spec.LimitHDDC))
	}
	for _, n := range r.Cfg.Tachs {
		v, ok := rd.Fan[n]
		if !ok || v != 0 {
			continue
		}
		// tach 0 aborts while the channel's duty is > 0; without the
		// registers the duty counts as > 0
		if haveDCR {
			if d, ok := dcr.Channel(n); ok && d == 0 {
				continue
			}
		}
		return s, fmt.Sprintf("fan%d reports 0 rpm while its duty is > 0", n)
	}
	return s, ""
}

// hold samples every Cfg.Sample for d from now; false on abort or cancel.
func (r *Runner) hold(ctx context.Context, phase string, written int, d time.Duration, each func(spec.Sample)) bool {
	return r.holdFrom(ctx, r.Env.Now(), phase, written, d, each)
}

// holdFrom samples on a fixed cadence: sample k is due at anchor +
// k*Cfg.Sample (k = 1..d/Cfg.Sample), so the read time of each sample does
// not add up (TESTSPEC 0.4 §2, #11: the 12th sample after an auto command
// is due at 60 s and judged there when read within +-0.5 s). A sample that
// overran skips the slots it missed instead of sampling in a burst (a
// skipped slot is a gap).
func (r *Runner) holdFrom(ctx context.Context, anchor time.Time, phase string, written int, d time.Duration, each func(spec.Sample)) bool {
	n := int(d / r.Cfg.Sample)
	if n < 1 {
		n = 1
	}
	for k := 1; k <= n; {
		due := anchor.Add(time.Duration(k) * r.Cfg.Sample)
		wait := due.Sub(r.Env.Now())
		if wait < 0 {
			wait = 0
		}
		if !r.Env.Sleep(ctx, wait) {
			r.stop("cancelled (signal)")
			return false
		}
		s, abort := r.sampleAt(phase, written, due)
		if each != nil {
			each(s)
		}
		if abort != "" {
			r.stop(abort)
			return false
		}
		next := int(r.Env.Now().Sub(anchor)/r.Cfg.Sample) + 1
		if next <= k {
			next = k + 1
		}
		if k < n && next > n {
			// the last slot is never skipped: the hold must reach its end
			next = n
		}
		k = next
	}
	return true
}

// Stop ends the run early with reason (first reason wins).
func (r *Runner) Stop(reason string) { r.stop(reason) }

func (r *Runner) stop(reason string) {
	if r.Abort == "" {
		r.Abort = reason
		r.event("ABORT %s", reason)
	}
}

var errLowDuty = errors.New("runner: duty below 100 is never written (TESTSPEC §4)")

// firstWrite runs BeforeWrite once, before anything is written.
func (r *Runner) firstWrite() error {
	if r.wrote {
		return nil
	}
	if r.BeforeWrite != nil {
		if err := r.BeforeWrite(); err != nil {
			return fmt.Errorf("before the first write: %w", err)
		}
	}
	r.wrote = true
	return nil
}

// write writes duty to channel ch and returns the time stamped on its
// event (the anchor of the samples that judge the write, TESTSPEC 0.4 §2).
func (r *Runner) write(ch, duty int) (time.Time, error) {
	if duty < spec.MinDuty || duty > 255 {
		return time.Time{}, fmt.Errorf("%w: %d", errLowDuty, duty)
	}
	if err := r.firstWrite(); err != nil {
		return time.Time{}, err
	}
	r.touched[ch] = true
	if err := r.Env.WriteDuty(ch, duty); err != nil {
		r.event("write pwm%d=%d FAILED: %v%s", ch, duty, err, r.tag())
		return time.Time{}, err
	}
	return r.eventAt("%s%s", EventWrite(ch, duty), r.tag()), nil
}

// EventWrite is the event text of a duty write (the report anchors the T1
// step samples on it).
func EventWrite(ch, duty int) string { return fmt.Sprintf("write pwm%d=%d", ch, duty) }

func (r *Runner) manual(ch int) error {
	if err := r.firstWrite(); err != nil {
		return err
	}
	r.touched[ch] = true
	if err := r.Env.EnterManual(ch); err != nil {
		r.event("pwm%d manual FAILED: %v%s", ch, err, r.tag())
		return err
	}
	r.event("pwm%d manual (driver sets 255)%s", ch, r.tag())
	return nil
}

// auto sends the EC automatic command and returns the time stamped on its
// event.
func (r *Runner) auto(ch int) (time.Time, error) {
	if err := r.firstWrite(); err != nil {
		return time.Time{}, err
	}
	r.touched[ch] = true
	if err := r.Env.Auto(ch); err != nil {
		r.event("pwm%d auto FAILED: %v%s", ch, err, r.tag())
		return time.Time{}, err
	}
	return r.eventAt("pwm%d auto (EC automatic command)%s", ch, r.tag()), nil
}

func (r *Runner) tag() string {
	if r.Tag == "" {
		return ""
	}
	return " [" + r.Tag + "]"
}

// HotChannel is the channel a temperature abort sets to 255 (TESTSPEC 0.4
// §4, #13), 0 for none: only in a run that wrote; a run that never wrote
// leaves every channel as it found it (n5-fangov validate 0.5.0-rc4 reads
// the rule the same way).
func (r *Runner) HotChannel() int {
	if r.hot == 0 || !r.wrote {
		return 0
	}
	return r.hot
}

// Close restores every channel the run touched to its restore value; after
// an abort on a temperature limit the channel of that sensor is set to 255
// instead, touched or not (TESTSPEC 0.4 §4, #13). It is safe to call more
// than once and runs on every exit path of the caller.
func (r *Runner) Close() {
	if h := r.HotChannel(); h > 0 && r.RestoreOverrides[h] == "" {
		r.touched[h] = true
	}
	for ch := 1; ch <= 4; ch++ {
		if !r.touched[ch] {
			continue
		}
		if r.restore(ch, "") {
			delete(r.touched, ch)
		}
	}
}

// restore puts one channel into its restore value and logs the outcome;
// false means the channel is not restored.
func (r *Runner) restore(ch int, when string) bool {
	if ch == r.HotChannel() {
		return r.restoreHot(ch, when)
	}
	err := r.Env.Restore(ch)
	switch {
	case err == nil:
		r.event("restore pwm%d%s ok%s", ch, when, r.tag())
		delete(r.restoreErr, ch)
		return true
	case errors.Is(err, ErrRestoreUnverified):
		r.event("restore pwm%d%s WRITTEN, REGISTER DIFFERS: %v%s", ch, when, err, r.tag())
		r.RestoreWarnings = append(r.RestoreWarnings, fmt.Sprintf("pwm%d: %v", ch, err))
		delete(r.restoreErr, ch)
		return true
	default:
		r.event("restore pwm%d%s FAILED: %v - set it by hand (n5-ectest restore)%s", ch, when, err, r.tag())
		r.restoreErr[ch] = err.Error()
		return false
	}
}

// restoreHot sets the channel of a tripped temperature limit to 255 (manual
// mode, duty 255) and records the reason in RestoreOverrides (TESTSPEC 0.4
// §4, #13). The read-back is compared after the settle time of the channel
// (§2.1); a register that differs then is a restore warning, as for any
// restore value.
func (r *Runner) restoreHot(ch int, when string) bool {
	why := fmt.Sprintf("255 (temperature abort: %s; TESTSPEC §4)", r.hotWhy)
	if r.BeforeHot != nil {
		if err := r.BeforeHot(ch); err != nil {
			r.event("restore pwm%d=255%s: before the write: %v (255 is written anyway)%s", ch, when, err, r.tag())
		}
	}
	t0 := r.Env.Now()
	err := r.Env.EnterManual(ch)
	if err == nil {
		err = r.Env.WriteDuty(ch, 255)
	}
	if err != nil {
		r.event("restore pwm%d=255%s after a temperature abort FAILED: %v - set it by hand (n5-ectest restore)%s", ch, when, err, r.tag())
		r.restoreErr[ch] = err.Error()
		return false
	}
	if r.RestoreOverrides == nil {
		r.RestoreOverrides = map[int]string{}
	}
	r.RestoreOverrides[ch] = why
	delete(r.restoreErr, ch)
	if r.dcrOK {
		regs := spec.JudgedRegs(ch)
		st := SettleTime(ch)
		// background context: the restore runs on the signal path too
		s := WaitDCR(context.Background(), r.Env.Now, r.Env.Sleep, r.Env.DCR, t0, regs, 255, st, st)
		if s.Err == nil && !s.Reached {
			r.event("restore pwm%d=255%s WRITTEN, REGISTER DIFFERS: %s: %s%s", ch, when, s.Text(regs), why, r.tag())
			r.RestoreWarnings = append(r.RestoreWarnings, fmt.Sprintf("pwm%d: %v: pwm%d=255 written, %s", ch, ErrRestoreUnverified, ch, s.Text(regs)))
			return true
		}
	}
	r.event("restore pwm%d=255%s ok: %s%s", ch, when, why, r.tag())
	return true
}

// Unrestored lists the channels this run touched and could not put back
// into their restore value, with the last error ("pwm3: ...").
func (r *Runner) Unrestored() []string {
	var out []string
	for ch := 1; ch <= 4; ch++ {
		if r.touched[ch] {
			out = append(out, fmt.Sprintf("pwm%d: %s", ch, orText(r.restoreErr[ch], "not restored")))
		}
	}
	return out
}

// Gates samples the G3/G4 window (no writes) and evaluates both.
func (r *Runner) Gates(ctx context.Context) (g3, g4 spec.Gate) {
	g3 = spec.Gate{ID: "G3", Condition: G3Condition}
	g4 = spec.Gate{ID: "G4", Condition: G4Condition}
	if r.Abort != "" {
		g3.Result, g3.Detail = "FAIL", "not sampled: "+r.Abort
		g4.Result, g4.Detail = "FAIL", "not sampled: "+r.Abort
		return g3, g4
	}
	var rows []spec.Sample
	first, abort := r.Sample(spec.PhaseGate, -1)
	rows = append(rows, first)
	ok := false
	if abort != "" {
		r.stop(abort)
	} else {
		ok = r.hold(ctx, spec.PhaseGate, -1, r.Cfg.Window, func(s spec.Sample) { rows = append(rows, s) })
	}
	g3.Result, g3.Detail = G3(first, r.Cfg.OnlineCPUs)
	if !ok {
		g4.Result, g4.Detail = "FAIL", "window not completed: "+r.Abort
		return g3, g4
	}
	ev := spec.EvalG4(rows)
	r.LimitSensors = ev.Limit
	g4.Detail = fmt.Sprintf("over %s: %v", r.Cfg.Window, ev.Parts)
	if ev.Pass {
		g4.Result = "PASS"
	} else {
		g4.Result = "FAIL"
	}
	return g3, g4
}

// G3Condition and G4Condition are the gate texts of TESTSPEC §3.
const (
	G3Condition = "load average (1 min) < 0.1 x online CPUs"
	G4Condition = "over 5 min: EC 0x04 (system), EC 0x06 (ambient) and HDD temperature each with mean of the last 60 s - mean of the first 60 s < 1 C and max - min <= 1 C; every abort-limit sensor (Tctl, NVMe, HDD) readable; Tctl is a safety limit only"
)

// G3 judges the load of the first gate sample against cpus online CPUs.
func G3(first spec.Sample, cpus int) (result, detail string) {
	switch {
	case math.IsNaN(first.Load1):
		return "FAIL", "load average not readable"
	case cpus <= 0:
		return "FAIL", fmt.Sprintf("load1 %.2f, online CPUs not readable", first.Load1)
	}
	limit := spec.GateLoadLimit(cpus)
	detail = fmt.Sprintf("load1 %.2f, limit %.2f (%d online CPUs)", first.Load1, limit, cpus)
	if first.Load1 < limit {
		return "PASS", detail
	}
	return "FAIL", detail
}

// RegDCR is DCRn of the sample, -1 when not read.
func RegDCR(s spec.Sample, n int) int {
	if n < 1 || n > len(s.DCR) || s.DCR[n-1] < 0 {
		return -1
	}
	return s.DCR[n-1]
}

// Event texts the report rebuilds the tests from.
const (
	EventT1Complete = "T1 complete"
	EventT2Complete = "T2 complete"
)

// EventT2Auto marks the auto command of one T2 sub-run.
func EventT2Auto(rep int, label string) string { return fmt.Sprintf("T2 r%d %s auto", rep, label) }

// RunT1 is the mapping test of Cfg.Channel.
func (r *Runner) RunT1(ctx context.Context) spec.Result {
	ch := r.Cfg.Channel
	if !r.dcrOK {
		return spec.NotImplemented("T1", ch, "not run: duty registers not readable ("+errText(r.dcrErr)+"); a verdict needs them, so nothing was written")
	}
	if r.Abort != "" {
		return spec.NotImplemented("T1", ch, "not run: "+r.Abort)
	}
	rest := map[int][]int{}
	restIn := map[int][]float64{}
	restDCR := map[int][]int{}
	r.event("T1 pwm%d: rest reference", ch)
	if !r.hold(ctx, spec.PhaseT1Rest, -1, r.Cfg.Hold, func(s spec.Sample) {
		for i, f := range s.Fan {
			rest[i+1] = append(rest[i+1], f)
		}
		AddInputs(restIn, ch, s)
		AddOtherDCR(restDCR, ch, s)
	}) {
		return spec.NotImplemented("T1", ch, "aborted: "+r.Abort)
	}
	if err := r.manual(ch); err != nil {
		r.stop("manual mode failed: " + err.Error())
		return spec.NotImplemented("T1", ch, "aborted: "+r.Abort)
	}
	var steps []spec.T1Step
	for _, duty := range spec.T1Steps {
		tw, err := r.write(ch, duty)
		if err != nil {
			r.stop("write failed: " + err.Error())
			return spec.NotImplemented("T1", ch, "aborted: "+r.Abort)
		}
		// the step's samples are due at the write + k*5 s; those before the
		// settle time of the channel are recorded, not judged (TESTSPEC 0.4
		// §2.1, §5.1)
		tws := tw.Sub(r.start).Seconds()
		st := spec.T1Step{Duty: duty, Fans: map[int][]int{}, Inputs: map[int][]float64{}}
		ok := r.holdFrom(ctx, tw, spec.PhaseT1Step(duty), duty, r.Cfg.Hold, func(s spec.Sample) {
			AddT1Sample(&st, ch, s, tws)
		})
		steps = append(steps, st)
		if !ok {
			return spec.NotImplemented("T1", ch, "aborted: "+r.Abort)
		}
	}
	r.event(EventT1Complete)
	if r.restore(ch, " after T1") {
		delete(r.touched, ch)
	}
	return spec.EvalT1(ch, r.Cfg.OwnTach, rest, restIn, restDCR, steps, r.Cfg.Tachs)
}

// AddInputs appends the table inputs of the channels other than ch with an
// EC automatic (pwm1-pwm3) of sample s to in (TESTSPEC 0.4 §5.1 #24).
func AddInputs(in map[int][]float64, ch int, s spec.Sample) {
	for oc := 1; oc <= 3; oc++ {
		if oc != ch {
			in[oc] = append(in[oc], spec.TableInput(oc, s))
		}
	}
}

// AddOtherDCR appends the duty registers of the channels other than ch of
// sample s to in (TESTSPEC §5.1, 0.5 #33).
func AddOtherDCR(in map[int][]int, ch int, s spec.Sample) {
	for _, n := range spec.OtherRegs(ch) {
		in[n] = append(in[n], RegDCR(s, n))
	}
}

// AddT1Sample adds sample s of a T1 step written at run time tws (seconds)
// to st; runner and report build the steps the same way.
func AddT1Sample(st *spec.T1Step, ch int, s spec.Sample, tws float64) {
	n, _ := n5driver.ChannelDCR(ch)
	st.DCR = append(st.DCR, RegDCR(s, n))
	if ch == 3 { // DCR4 is judged too (TESTSPEC §5.1)
		st.DCR4 = append(st.DCR4, RegDCR(s, 4))
	}
	for i, f := range s.Fan {
		st.Fans[i+1] = append(st.Fans[i+1], f)
	}
	st.T = append(st.T, spec.SinceMS(s.TS, tws))
	if st.Inputs == nil {
		st.Inputs = map[int][]float64{}
	}
	AddInputs(st.Inputs, ch, s)
	if st.Others == nil {
		st.Others = map[int][]int{}
	}
	AddOtherDCR(st.Others, ch, s)
}

// observeIn samples after an auto command stamped at t0 and returns the DCR
// series of every judged register of the channel, relative to t0 (t in
// seconds, from the stored ms values of sample and event, as the report
// rebuilds it); in collects the channel's table input of every sample.
func (r *Runner) observeIn(ctx context.Context, t0 time.Time, phase string, d time.Duration, in *[]float64) (map[int][]spec.DCRSample, bool) {
	ts0 := t0.Sub(r.start).Seconds()
	out := map[int][]spec.DCRSample{}
	regs := spec.JudgedRegs(r.Cfg.Channel)
	ok := r.holdFrom(ctx, t0, phase, -1, d, func(s spec.Sample) {
		for _, n := range regs {
			if v := RegDCR(s, n); v >= 0 {
				out[n] = append(out[n], spec.DCRSample{T: spec.SinceMS(s.TS, ts0), DCR: v})
			}
		}
		*in = append(*in, spec.TableInput(r.Cfg.Channel, s))
	})
	return out, ok
}

// firstKnown is the first value of v that is not NaN (NaN: none).
func firstKnown(v []float64) float64 {
	for _, x := range v {
		if !math.IsNaN(x) {
			return x
		}
	}
	return math.NaN()
}

// PreCrossing is the table-input crossing of channel ch in the window that
// determines A (reference: its first sample; "" = none; TESTSPEC 0.4 §5.2).
func PreCrossing(ch int, in []float64) string {
	return spec.Crossing(ch, firstKnown(in), in)
}

// MarkCrossing sets the crossing of a sub-run on every register: the one of
// the A window (pre), else the one between the write and the end of the log
// (in: the hold and log samples, reference the first one; TESTSPEC 0.4 §5.2
// #17).
func MarkCrossing(hr []spec.HandbackRun, ch int, pre string, in []float64) {
	x := ""
	if pre != "" {
		x = "in the window that determined A: " + pre
	} else if c := spec.Crossing(ch, firstKnown(in), in); c != "" {
		x = c
	}
	for i := range hr {
		hr[i].Crossed = x
	}
}

// HoldDeviation lists a hold sample of a T2 sub-run (written at run time
// tws) that is due after the settle time and whose judged register differs
// from the written value ("" = none; TESTSPEC 0.4 §5.2 step 3).
func HoldDeviation(ch, rep int, label string, written int, s spec.Sample, tws float64) string {
	t := spec.SinceMS(s.TS, tws)
	if !spec.Judged(ch, t) {
		return ""
	}
	var bad []string
	for _, n := range spec.JudgedRegs(ch) {
		if v := RegDCR(s, n); v >= 0 && v != written {
			bad = append(bad, fmt.Sprintf("DCR%d %d", n, v))
		}
	}
	if len(bad) == 0 {
		return ""
	}
	return fmt.Sprintf("run %d %s hold +%.1f s: written %d, %s", rep, label, t, written, strings.Join(bad, ", "))
}

// RunHandback is T2 with T3 from the same data: per repetition the
// sub-runs H (255) and M (min(A+48, 200)), each 60 s held, then the auto
// command and Cfg.HandbackObserve() of samples. pwm3 is evaluated on DCR3
// and DCR4. pwm4 has no EC automatic: T2/T3 are N/A and nothing is written
// (TESTSPEC §5.2, §6).
func (r *Runner) RunHandback(ctx context.Context) (spec.Result, spec.Result) {
	ch := r.Cfg.Channel
	if ch == spec.PCIeChannel {
		return spec.NotApplicableResult("T2", ch, spec.ReasonNoPCIeAuto), spec.NotApplicableResult("T3", ch, spec.ReasonNoPCIeAuto)
	}
	if !r.dcrOK {
		reason := "not run: duty registers not readable (" + errText(r.dcrErr) + "); a verdict needs them, so nothing was written"
		return spec.NotImplemented("T2", ch, reason), spec.NotImplemented("T3", ch, reason)
	}
	if r.Abort != "" {
		return spec.NotImplemented("T2", ch, "not run: "+r.Abort), spec.NotImplemented("T3", ch, "not run: "+r.Abort)
	}
	r.event("T2 pwm%d: auto value A", ch)
	ta, err := r.auto(ch)
	if err != nil {
		r.stop("auto command failed: " + err.Error())
		return spec.NotImplemented("T2", ch, "aborted: "+r.Abort), spec.NotImplemented("T3", ch, "aborted: "+r.Abort)
	}
	regs := spec.JudgedRegs(ch)
	var preIn []float64
	pre, ok := r.observeIn(ctx, ta, spec.PhaseT2Pre, r.Cfg.PreWindow(), &preIn)
	if !ok || len(pre[regs[0]]) == 0 {
		return spec.NotImplemented("T2", ch, "aborted: "+orText(r.Abort, "no DCR sample")), spec.NotImplemented("T3", ch, "aborted: "+orText(r.Abort, "no DCR sample"))
	}
	autoA, aText := AutoValues(pre, regs)
	a := autoA[regs[0]]
	m := spec.MValue(a)
	r.event("T2 pwm%d: %s, H=255, M=%d", ch, strings.Join(aText, ", "), m)
	preCross := PreCrossing(ch, preIn)
	if preCross != "" {
		r.event("T2 pwm%d: table input crossed a step in the A window: %s (every sub-run INCONCLUSIVE, TESTSPEC §5.2)", ch, preCross)
	}
	var runs [][]spec.SubResult
	var holdDev []string
	for rep := 1; rep <= r.Cfg.Repeat; rep++ {
		var subs []spec.SubResult
		for _, sub := range []struct {
			label string
			duty  int
		}{{"H", 255}, {"M", m}} {
			if err := r.manual(ch); err != nil {
				r.stop("manual mode failed: " + err.Error())
				break
			}
			tw, err := r.write(ch, sub.duty)
			if err != nil {
				r.stop("write failed: " + err.Error())
				break
			}
			// hold: due at the write + k*5 s; a DCR after the settle time
			// that differs from W is listed, not judged (TESTSPEC 0.4 §5.2)
			var subIn []float64
			tws := tw.Sub(r.start).Seconds()
			if !r.holdFrom(ctx, tw, spec.PhaseT2Hold(rep, sub.label), sub.duty, r.Cfg.Hold, func(s spec.Sample) {
				subIn = append(subIn, spec.TableInput(ch, s))
				if d := HoldDeviation(ch, rep, sub.label, sub.duty, s, tws); d != "" {
					holdDev = append(holdDev, d)
				}
			}) {
				break
			}
			if _, err := r.auto(ch); err != nil {
				r.stop("auto command failed: " + err.Error())
				break
			}
			// t = 0 of the sub-run is the time stamped on this event: the
			// report rebuilds the series from it
			t0 := r.eventAt("%s", EventT2Auto(rep, sub.label))
			smp, ok := r.observeIn(ctx, t0, spec.PhaseT2Auto(rep, sub.label), r.Cfg.HandbackObserve(), &subIn)
			hr := HandbackRuns(ch, sub.label, sub.duty, regs, smp, autoA)
			MarkCrossing(hr, ch, preCross, subIn)
			subs = append(subs, spec.EvalHandbackRegs(hr))
			if !ok {
				break
			}
		}
		if len(subs) > 0 {
			runs = append(runs, subs)
		}
		if r.Abort != "" {
			break
		}
	}
	if r.Abort == "" {
		r.event(EventT2Complete)
	}
	t2, t3 := FinishHandback(ch, runs, r.Abort, a, m, regs, autoA, holdDev)
	if r.restore(ch, " after T2") {
		delete(r.touched, ch)
	}
	return t2, t3
}

// AutoValues returns A per judged register (last sample of the pre window)
// and its text; a register without samples has no A.
func AutoValues(pre map[int][]spec.DCRSample, regs []int) (map[int]int, []string) {
	autoA := map[int]int{}
	var aText []string
	for _, n := range regs {
		if p := pre[n]; len(p) > 0 {
			autoA[n] = p[len(p)-1].DCR
			aText = append(aText, fmt.Sprintf("DCR%d A=%d", n, autoA[n]))
		} else {
			aText = append(aText, fmt.Sprintf("DCR%d A=-", n))
		}
	}
	return autoA, aText
}

// HandbackRuns builds the per-register data of one sub-run of channel ch.
func HandbackRuns(ch int, label string, duty int, regs []int, smp map[int][]spec.DCRSample, autoA map[int]int) []spec.HandbackRun {
	var hr []spec.HandbackRun
	for _, n := range regs {
		av, known := autoA[n]
		reg := ""
		if len(regs) > 1 {
			reg = fmt.Sprintf("DCR%d", n)
		}
		hr = append(hr, spec.HandbackRun{Label: label, Reg: reg, Written: duty, Samples: smp[n], TimingKnown: true, Auto: av, AutoKnown: known, LimitS: spec.T3LimitS(ch)})
	}
	return hr
}

// FinishHandback aggregates the sub-runs into T2/T3 and applies an abort.
// holdDev are the hold samples after the settle time that differ from the
// written value (listed, not judged; TESTSPEC 0.4 §5.2).
func FinishHandback(ch int, runs [][]spec.SubResult, abort string, a, m int, regs []int, autoA map[int]int, holdDev []string) (spec.Result, spec.Result) {
	t2, t3 := spec.AggregateHandback(ch, runs, []string{"H", "M"})
	if abort != "" {
		t2.Verdict, t3.Verdict = spec.Inconclusive, spec.Inconclusive
		t2.Reasons = append([]string{"aborted: " + abort}, t2.Reasons...)
		t3.Reasons = append([]string{"aborted: " + abort}, t3.Reasons...)
	}
	if holdDev == nil {
		holdDev = []string{}
	}
	vals := map[string]any{"A": a, "M": m, "hold_deviations": holdDev, "settle_s": spec.SettleS(ch)}
	if len(regs) > 1 {
		for _, n := range regs {
			if v, ok := autoA[n]; ok {
				vals[fmt.Sprintf("A_DCR%d", n)] = v
			}
		}
	}
	t2.Values = vals
	return t2, t3
}

func errText(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

func orText(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
