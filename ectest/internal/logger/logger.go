package logger

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/hwread"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// ErrFinished: the run directory holds a finished run (a unit started
// again after the end, e.g. at boot, exits cleanly on it).
var ErrFinished = errors.New("log run already finished")

// Config of a run.
type Config struct {
	RunID    string
	Dir      string // run directory (bundle)
	Duration time.Duration
	Interval time.Duration
	MailTo   string
}

// JLine is one journal line.
type JLine struct {
	Time   time.Time
	Boot   string
	Msg    string
	Cursor string
}

// Deps are the outside world of a run (tests replace them).
type Deps struct {
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) bool
	// Fangov returns ActiveState and NRestarts of n5-fangov.
	Fangov func() (state string, nrestarts int, err error)
	// Journal returns the lines after cursor ("" = since the given time):
	// unit "" = kernel log, else the unit's lines with priority <= warning.
	Journal func(unit, cursor string, since time.Time) ([]JLine, string, error)
	// Mail sends one message; an error queues it in outbox/.
	Mail func(subject, body string) error
	// FreeBytes is the free space of the file system of path.
	FreeBytes func(path string) (uint64, error)
	// Notify is sd_notify (READY=1, WATCHDOG=1, STOPPING=1).
	Notify func(state string)
	// Print is the stdout line sink (events, mails without an MTA).
	Print func(string)
	// Redact cleans every stored and mailed text.
	Redact *identity.Redactor
	// Meta is written to meta.json at the start (provenance) and extended at
	// the end.
	Meta map[string]any
	// Identity returns what names the software of the box (kernel, driver
	// version, srcversion, parameters, DKMS package); it is recorded at the
	// start, on every resume and after a reboot (K2). nil: not recorded.
	Identity func() map[string]string
}

// State is state.json, written atomically after every sample.
type State struct {
	RunID          string              `json:"run_id"`
	StartEpoch     int64               `json:"start_epoch"`
	EndEpoch       int64               `json:"end_epoch"`
	IntervalS      float64             `json:"interval_s"`
	BootIDStart    string              `json:"boot_id_start"`
	TaintStart     string              `json:"taint_start"`
	LastBoot       string              `json:"last_boot_id"`
	LastTaint      string              `json:"last_taint"`
	LastSampleWall int64               `json:"last_sample_epoch"`
	LastSampleMono float64             `json:"last_sample_mono_s"`
	Samples        int                 `json:"samples"`
	EventSeq       int                 `json:"event_seq"`
	CursorKernel   string              `json:"cursor_kernel"`
	CursorFangov   string              `json:"cursor_fangov"`
	LastCheck      int64               `json:"last_check_epoch"`
	LastDaily      int64               `json:"last_daily_epoch"`
	FangovNR       int                 `json:"fangov_nrestarts"`
	Episodes       map[string]bool     `json:"episodes"`
	ZeroRun        [3]int              `json:"fan_zero_run"`
	MissRun        int                 `json:"ec_missing_run"`
	LastSent       map[string]int64    `json:"alert_last_sent"`
	Pending        map[string][]string `json:"alert_pending"`
	K1Count        int                 `json:"k1_recorded"`
	Restarts       int                 `json:"logger_restarts"`
	Finished       bool                `json:"finished"`
	MailsDropped   int                 `json:"mails_dropped"`
	// MailSeq numbers the queued mails (unique outbox file names).
	MailSeq int `json:"mail_seq"`
	// Identity is the last recorded software identity, IdentityChanges
	// every change seen at a resume or reboot ("<time> key: old -> new").
	Identity        map[string]string `json:"identity,omitempty"`
	IdentityChanges []string          `json:"identity_changes,omitempty"`
}

// File names of a log run.
const (
	AuxCSV      = "aux.csv"
	StateFile   = "state.json"
	Annotations = "annotations.txt"
)

// AuxHeader are the logger's extra columns, keyed by t_iso (design §8).
var AuxHeader = []string{"t_iso", "boot_id", "uptime_s", "mono_s", "pwm1", "pwm2", "pwm3", "pwm4", "pwm1_enable", "pwm2_enable", "pwm3_enable", "pwm4_enable", "ec_read_errors", "fangov_state", "fangov_nrestarts"}

// Logger is one run.
type Logger struct {
	cfg   Config
	d     Deps
	st    State
	start time.Time
	// mailDown: a mail failed in this loop pass; further mails of the pass
	// go to the outbox without an attempt (a hanging MTA must not stop the
	// sampler or trip the watchdog)
	mailDown bool
}

// OutboxDir is where the mails of a run wait when no MTA works: beside the
// run directory, not in it (the queued mails carry the recipient address,
// the run directory is what gets posted).
func OutboxDir(runDir string) string { return filepath.Clean(runDir) + ".outbox" }

// identitySnapshot records the software identity: a note in events.log,
// and every change against the last one in IdentityChanges.
func (l *Logger) identitySnapshot(now time.Time, why string) {
	if l.d.Identity == nil {
		return
	}
	id := l.d.Identity()
	var keys []string
	for k := range id {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts, changes []string
	for _, k := range keys {
		parts = append(parts, k+"="+orDash(id[k]))
		if l.st.Identity != nil && l.st.Identity[k] != id[k] {
			changes = append(changes, fmt.Sprintf("%s: %s -> %s", k, orDash(l.st.Identity[k]), orDash(id[k])))
		}
	}
	l.note(now, fmt.Sprintf("identity (%s): %s", why, strings.Join(parts, ", ")))
	for _, c := range changes {
		l.st.IdentityChanges = append(l.st.IdentityChanges, now.UTC().Format(time.RFC3339)+" "+c)
		l.note(now, "identity changed ("+why+"): "+c)
	}
	l.st.Identity = id
}

// LoadState reads state.json of a run directory.
func LoadState(dir string) (State, error) {
	var s State
	b, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}

func (l *Logger) saveState() error {
	b, err := json.MarshalIndent(l.st, "", "  ")
	if err != nil {
		return err
	}
	return bundle.WriteAtomic(filepath.Join(l.cfg.Dir, StateFile), append(b, '\n'), 0o640)
}

func (l *Logger) red(s string) string {
	if l.d.Redact == nil {
		return s
	}
	return l.d.Redact.String(s)
}

func appendLine(p, line string) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	_, err = f.WriteString(line + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func appendCSV(p string, header, row []string) error {
	_, statErr := os.Stat(p)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if errors.Is(statErr, fs.ErrNotExist) {
		_ = w.Write(header)
	}
	_ = w.Write(row)
	w.Flush()
	err = w.Error()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// event records one criterion hit: events.log, the alert queue, stdout.
func (l *Logger) event(key string, at time.Time, detail string, evidence ...string) {
	l.st.EventSeq++
	detail = strings.ReplaceAll(detail, "\n", " ")
	ts := at.Sub(l.start).Seconds()
	line := l.red(fmt.Sprintf("%s [t_s=%.3f] %s #%d %s", at.UTC().Format(time.RFC3339), ts, key, l.st.EventSeq, detail))
	_ = appendLine(filepath.Join(l.cfg.Dir, bundle.Events), line)
	if l.st.Pending == nil {
		l.st.Pending = map[string][]string{}
	}
	ev := []string{fmt.Sprintf("#%d %s %s", l.st.EventSeq, at.UTC().Format(time.RFC3339), detail)}
	for _, e := range evidence {
		ev = append(ev, "    "+e)
	}
	if len(l.st.Pending[key]) < 200 {
		l.st.Pending[key] = append(l.st.Pending[key], ev...)
	}
	if l.d.Print != nil {
		l.d.Print(line)
	}
}

// note records a logger event without a criterion (start, resume, end).
func (l *Logger) note(at time.Time, text string) {
	line := l.red(fmt.Sprintf("%s [t_s=%.3f] %s", at.UTC().Format(time.RFC3339), at.Sub(l.start).Seconds(), text))
	_ = appendLine(filepath.Join(l.cfg.Dir, bundle.Events), line)
}

func readUptime() float64 {
	if f := strings.Fields(sysroot.Read(filepath.Join(sysroot.Proc(), "uptime"))); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			return v
		}
	}
	return math.NaN()
}

// Run samples until the end of the run or ctx is done. A run directory
// with a state.json is resumed (restart-safe); the gap is checked as K5.
func Run(ctx context.Context, cfg Config, d Deps) error {
	if cfg.Interval <= 0 || cfg.Duration <= 0 {
		return errors.New("log: interval and duration must be > 0")
	}
	l := &Logger{cfg: cfg, d: d}
	now := d.Now()
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return err
	}
	st, err := LoadState(cfg.Dir)
	switch {
	case err == nil:
		if st.Finished {
			// a unit still enabled after the end starts at every boot: tell
			// systemd (Type=notify) it started and stops, so it is not failed
			if d.Notify != nil {
				d.Notify("READY=1")
				d.Notify("STOPPING=1")
			}
			return fmt.Errorf("%w: run %s in %s", ErrFinished, st.RunID, cfg.Dir)
		}
		l.st = st
		l.st.Restarts++
		l.start = time.Unix(st.StartEpoch, 0)
		l.note(now, fmt.Sprintf("logger resumed (restart %d)", l.st.Restarts))
		l.identitySnapshot(now, "resume")
		if d.Meta != nil {
			// the meta.json written at the end comes from this process
			d.Meta["started"] = l.start.UTC().Format(time.RFC3339)
			d.Meta["planned_end"] = time.Unix(l.st.EndEpoch, 0).UTC().Format(time.RFC3339)
		}
	case errors.Is(err, fs.ErrNotExist):
		l.start = now
		l.st = State{RunID: cfg.RunID, StartEpoch: now.Unix(), EndEpoch: now.Add(cfg.Duration).Unix(), IntervalS: cfg.Interval.Seconds(),
			BootIDStart: sysroot.Read(filepath.Join(sysroot.Proc(), "sys", "kernel", "random", "boot_id")),
			TaintStart:  sysroot.Read(filepath.Join(sysroot.Proc(), "sys", "kernel", "tainted")),
			Episodes:    map[string]bool{}, LastSent: map[string]int64{}, Pending: map[string][]string{}}
		l.st.LastBoot, l.st.LastTaint = l.st.BootIDStart, l.st.TaintStart
		if _, n, err := d.Fangov(); err == nil {
			l.st.FangovNR = n
		}
		l.st.LastDaily = l.st.StartEpoch
		if d.Meta != nil {
			d.Meta["started"] = now.UTC().Format(time.RFC3339)
			d.Meta["planned_end"] = time.Unix(l.st.EndEpoch, 0).UTC().Format(time.RFC3339)
			b, _ := json.MarshalIndent(d.Meta, "", "  ")
			_ = bundle.WriteAtomic(filepath.Join(cfg.Dir, bundle.Meta), append([]byte(l.red(string(b))), '\n'), 0o640)
		}
		l.note(now, fmt.Sprintf("logger started: run %s, interval %s, until %s", cfg.RunID, cfg.Interval, time.Unix(l.st.EndEpoch, 0).UTC().Format(time.RFC3339)))
		l.identitySnapshot(now, "start")
		// the start mail may hang on a dead MTA: state first
		if err := l.saveState(); err != nil {
			return err
		}
		l.mail(fmt.Sprintf("START %s", cfg.RunID), fmt.Sprintf("Run %s started %s, interval %s, until %s.\nThis mail arriving proves the mail path of the alerts.\n",
			cfg.RunID, now.UTC().Format(time.RFC3339), cfg.Interval, time.Unix(l.st.EndEpoch, 0).UTC().Format(time.RFC3339)))
	default:
		return err
	}
	if l.st.Episodes == nil {
		l.st.Episodes = map[string]bool{}
	}
	if l.st.LastSent == nil {
		l.st.LastSent = map[string]int64{}
	}
	if err := l.saveState(); err != nil {
		return err
	}
	if d.Notify != nil {
		d.Notify("READY=1")
	}
	for {
		now = d.Now()
		if now.Unix() >= l.st.EndEpoch {
			return l.finish(now)
		}
		l.sample(now)
		if now.Unix()-l.st.LastCheck >= CheckIntervalS {
			l.check(now)
		}
		if now.Unix()-l.st.LastDaily >= 86400 {
			l.daily(now)
		}
		if err := l.saveState(); err != nil {
			l.event("K8", now, "state file not written: "+err.Error())
		}
		if d.Notify != nil {
			d.Notify("WATCHDOG=1")
		}
		if !d.Sleep(ctx, cfg.Interval) {
			l.note(d.Now(), "logger stopped (signal); resumes on the next start")
			_ = l.saveState()
			if d.Notify != nil {
				d.Notify("STOPPING=1")
			}
			return nil
		}
		// a new pass: one mail attempt again (the start mail belongs to the
		// first pass)
		l.mailDown = false
	}
}

// sample takes one row and runs the per-sample criteria K2, K3 (state), K4,
// K5, K7.
func (l *Logger) sample(now time.Time) {
	sys, proc := sysroot.Sys(), sysroot.Proc()
	boot := sysroot.Read(filepath.Join(proc, "sys", "kernel", "random", "boot_id"))
	mono := readUptime()
	r := hwread.Read(sys, proc)
	s := spec.NewSample(now, now.Sub(l.start).Seconds(), "log", 0, -1)
	for n := 1; n <= 3; n++ {
		s.Fan[n-1] = r.Fan[n]
	}
	s.ECCPU, s.ECSys, s.ECBoard, s.ECAmb = r.EC["cpu"], r.EC["system"], r.EC["board"], r.EC["ambient"]
	s.HDDMax, s.NVMeMax, s.Tctl, s.Load1 = r.HDDMax, r.NVMeMax, r.Tctl, r.Load1
	errs := r.Errors
	dcrWhy := ""
	if p := n5driver.DCRPath(sys); r.Hwmon != "" {
		if _, err := os.Stat(p); err == nil {
			txt, err := sysroot.ReadTimeout(p, sysroot.DefaultTimeout)
			if err == nil {
				if dcr, perr := n5driver.ParseDCR(txt); perr == nil {
					for n := 1; n <= 5; n++ {
						s.DCR[n-1] = dcr.Reg(n)
					}
				} else {
					dcrWhy = "dcr not parsable: " + perr.Error()
				}
			} else {
				errs++
				dcrWhy = "dcr read error: " + err.Error()
			}
		} else {
			// driver without debug_regs=1, or debugfs not readable here
			dcrWhy = "dcr not readable (" + err.Error() + "): dcr1-dcr5 stay empty; load the driver with debug_regs=1"
		}
	}
	fgState, fgNR, fgErr := l.d.Fangov()
	if fgErr != nil {
		fgState, fgNR = "", -1
	}
	_ = appendCSV(filepath.Join(l.cfg.Dir, bundle.Samples), spec.CSVHeader, s.Row())
	aux := []string{s.Time.UTC().Format(time.RFC3339), boot, cellF(mono, 2), cellF(mono, 3)}
	for _, m := range []map[int]int{r.PWM, r.Enable} {
		for n := 1; n <= 4; n++ {
			aux = append(aux, cellI(m[n]))
		}
	}
	errCell := strconv.Itoa(errs)
	if r.Hwmon == "" {
		errCell = ""
	}
	aux = append(aux, errCell, fgState, cellI(fgNR))
	_ = appendCSV(filepath.Join(l.cfg.Dir, AuxCSV), AuxHeader, aux)

	// K2 / K5
	if l.st.Samples > 0 || l.st.Restarts > 0 {
		switch {
		case boot != "" && l.st.LastBoot != "" && boot != l.st.LastBoot:
			l.event("K2", now, fmt.Sprintf("boot_id changed: %.8s -> %.8s", l.st.LastBoot, boot))
			// a kernel or DKMS change comes with a reboot
			l.identitySnapshot(now, "reboot")
			if l.st.LastSampleWall > 0 {
				if gap := now.Unix() - l.st.LastSampleWall; gap > GapMaxS {
					l.event("K5", now, fmt.Sprintf("gap %d s between samples across a reboot (wall clock)", gap))
				}
			}
			l.st.FangovNR = 0
		case !math.IsNaN(mono) && l.st.LastSampleMono > 0:
			if gap := mono - l.st.LastSampleMono; gap > GapMaxS {
				l.event("K5", now, fmt.Sprintf("gap %.0f s between samples (monotonic, boot %.8s)", gap, boot))
			}
		}
	}
	if boot != "" {
		l.st.LastBoot = boot
	}
	// K3: n5-fangov state and restart counter
	switch {
	case fgState == FangovNotInstalled:
	case fgErr != nil:
		l.episode("K3", "fangov_unknown", true, now, "n5-fangov state not readable: "+fgErr.Error())
	case fgState != "active":
		l.episode("K3", "fangov_inactive", true, now, fmt.Sprintf("%s ActiveState=%s", FangovUnit, orDash(fgState)))
	default:
		l.episode("K3", "fangov_inactive", false, now, "")
		l.episode("K3", "fangov_unknown", false, now, "")
	}
	if fgErr == nil && fgNR > l.st.FangovNR {
		l.event("K3", now, fmt.Sprintf("%s NRestarts %d -> %d", FangovUnit, l.st.FangovNR, fgNR))
	}
	if fgErr == nil {
		l.st.FangovNR = fgNR
	}
	// K4
	for f := 1; f <= 3; f++ {
		if r.Fan[f] == 0 {
			l.st.ZeroRun[f-1]++
			if l.st.ZeroRun[f-1] == 2 {
				l.event("K4", now, fmt.Sprintf("fan%d 0 rpm in 2 consecutive samples", f))
			}
		} else {
			l.st.ZeroRun[f-1] = 0
		}
	}
	if r.Hwmon == "" || (r.Fan[1] < 0 && math.IsNaN(r.EC["cpu"])) {
		l.st.MissRun++
		if l.st.MissRun == 2 {
			l.event("K4", now, "EC hwmon missing or without values (2 samples)")
		}
	} else {
		l.st.MissRun = 0
	}
	// one event per episode: a lasting EC error is not one line per sample
	l.episode("K4", "ec_read_errors", errs > 0, now, fmt.Sprintf("%d EC read error(s) or timeout(s) in one sample (episode: reported once until a sample without errors)", errs))
	// dcr1-dcr5 empty without a word would look like a complete record
	l.episode("K8", "dcr", dcrWhy != "", now, dcrWhy)
	for _, h := range []struct {
		name string
		v    float64
		th   float64
	}{{"Tctl", r.Tctl, ThTctl}, {"NVMe", r.NVMeMax, ThNVMe}, {"HDD", r.HDDMax, ThHDD}, {"EC_CPU", r.EC["cpu"], ThECCPU}} {
		if math.IsNaN(h.v) {
			continue
		}
		l.episode("K4", "hot_"+h.name, h.v >= h.th, now, fmt.Sprintf("%s=%.1f >= %.0f", h.name, h.v, h.th))
	}
	// K7
	taint := sysroot.Read(filepath.Join(proc, "sys", "kernel", "tainted"))
	if taint != "" && taint != l.st.LastTaint {
		l.event("K7", now, fmt.Sprintf("kernel taint %s -> %s (start %s)", orDash(l.st.LastTaint), taint, orDash(l.st.TaintStart)))
		l.st.LastTaint = taint
	}
	l.st.Samples++
	l.st.LastSampleWall = now.Unix()
	if !math.IsNaN(mono) {
		l.st.LastSampleMono = mono
	}
}

// episode reports a condition once when it starts; it re-arms when it ends.
func (l *Logger) episode(key, name string, on bool, now time.Time, detail string) {
	if on && !l.st.Episodes[name] {
		l.event(key, now, detail)
	}
	if on {
		l.st.Episodes[name] = true
	} else {
		delete(l.st.Episodes, name)
	}
}

// check runs every CheckIntervalS: K1, K3 journal, K6, alerts, outbox.
func (l *Logger) check(now time.Time) {
	since := time.Unix(l.st.StartEpoch, 0)
	if l.st.LastCheck > 0 {
		since = time.Unix(l.st.LastCheck, 0)
	}
	boot := strings.ReplaceAll(sysroot.Read(filepath.Join(sysroot.Proc(), "sys", "kernel", "random", "boot_id")), "-", "")
	lines, cur, err := l.d.Journal("", l.st.CursorKernel, since)
	if err != nil {
		l.episode("K8", "journal_kernel", true, now, "kernel log not readable: "+err.Error())
	} else {
		l.episode("K8", "journal_kernel", false, now, "")
	}
	if cur != "" {
		l.st.CursorKernel = cur
	}
	dropped := 0
	for _, jl := range lines {
		msg := strings.TrimPrefix(jl.Msg, "kernel: ")
		if !K1Hit(msg, jl.Boot != "" && boot != "" && jl.Boot != boot) {
			continue
		}
		if l.st.K1Count >= K1MaxPerRun {
			dropped++
			continue
		}
		l.st.K1Count++
		short := msg
		if len(short) > 200 {
			short = short[:200]
		}
		l.event("K1", jl.Time, short, jl.Time.UTC().Format(time.RFC3339)+" "+msg)
	}
	if dropped > 0 {
		l.event("K1", now, fmt.Sprintf("K1 flood: %d further hit(s) not recorded one by one (cap %d per run)", dropped, K1MaxPerRun))
	}
	flines, fcur, ferr := l.d.Journal(FangovUnit, l.st.CursorFangov, since)
	if ferr == nil {
		if fcur != "" {
			l.st.CursorFangov = fcur
		}
		if len(flines) > 0 {
			var ev []string
			for i, x := range flines {
				if i >= len(flines)-40 {
					ev = append(ev, x.Time.UTC().Format(time.RFC3339)+" "+x.Msg)
				}
			}
			l.event("K3", now, fmt.Sprintf("%d journal line(s) >= warning of %s", len(flines), FangovUnit), ev...)
		}
	}
	// K6
	var k6 []string
	if sz := dirSize(l.cfg.Dir); sz > DataMaxMB*1024*1024 {
		k6 = append(k6, fmt.Sprintf("data directory %d MB > %d MB", sz/1024/1024, DataMaxMB))
	}
	if free, err := l.d.FreeBytes(l.cfg.Dir); err == nil && free < RootMinFreeGB*1024*1024*1024 {
		k6 = append(k6, fmt.Sprintf("free space of the log target %d MB < %d GB", free/1024/1024, RootMinFreeGB))
	}
	l.episode("K6", "disk", len(k6) > 0, now, strings.Join(k6, "; "))
	l.st.LastCheck = now.Unix()
	// state before any mail: a watchdog kill during a hanging mail must not
	// run the whole check again right after the restart
	if err := l.saveState(); err != nil {
		l.event("K8", now, "state file not written: "+err.Error())
	}
	l.flushOutbox()
	l.alerts(now)
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// alerts sends one mail per key with pending events, at most one per key
// per AlertCooldownS.
func (l *Logger) alerts(now time.Time) {
	for _, k := range Keys {
		p := l.st.Pending[k]
		if len(p) == 0 || now.Unix()-l.st.LastSent[k] < AlertCooldownS {
			continue
		}
		first := strings.SplitN(p[0], " ", 3)
		short := ""
		if len(first) == 3 {
			short = first[2]
		}
		if len(short) > 80 {
			short = short[:80]
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Run: %s\nCriterion: %s - %s\n\nEvents since the last mail of %s (at most 50 lines):\n\n", l.cfg.RunID, k, KeyText[k], k)
		from := 0
		if len(p) > 50 {
			from = len(p) - 50
		}
		b.WriteString(strings.Join(p[from:], "\n"))
		fmt.Fprintf(&b, "\n\nExplain a planned event: n5-ectest log annotate %s --event N \"text\"\n", l.cfg.RunID)
		l.mail(fmt.Sprintf("ALERT %s %s", k, short), b.String())
		l.st.LastSent[k] = now.Unix()
		delete(l.st.Pending, k)
	}
}

// Summary are the counts of a run (daily mail, end, report).
type Summary struct {
	Samples      int                       `json:"samples"`
	CoveragePct  float64                   `json:"coverage_pct"`
	Keys         map[string]map[string]int `json:"keys"` // K -> total, explained, unexplained
	OpenEpisodes []string                  `json:"open_episodes"`
	Annotations  int                       `json:"annotations"`
	DataMB       float64                   `json:"data_mb"`
	Restarts     int                       `json:"logger_restarts"`
	MailsDropped int                       `json:"mails_dropped"`
}

// Summarize counts the events of the run directory since `since`.
func Summarize(dir string, st State, since, until time.Time) Summary {
	evs := ParseEventLog(dir)
	ann := ParseAnnotations(dir)
	s := Summary{Samples: st.Samples, Keys: map[string]map[string]int{}, Annotations: len(ann), DataMB: float64(dirSize(dir)) / 1024 / 1024, Restarts: st.Restarts, MailsDropped: st.MailsDropped}
	for _, k := range Keys {
		s.Keys[k] = map[string]int{"total": 0, "explained": 0, "unexplained": 0}
	}
	for _, e := range evs {
		if e.Time.Before(since) {
			continue
		}
		c := s.Keys[e.Key]
		if c == nil {
			continue
		}
		c["total"]++
		if Explained(e, ann) {
			c["explained"]++
		} else {
			c["unexplained"]++
		}
	}
	for k := range st.Episodes {
		s.OpenEpisodes = append(s.OpenEpisodes, k)
	}
	sort.Strings(s.OpenEpisodes)
	s.CoveragePct = Coverage(dir, time.Unix(st.StartEpoch, 0), until, st.IntervalS, ann)
	return s
}

func (s Summary) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "samples: %d, coverage %.2f %%, data %.1f MB, logger restarts %d\n\n", s.Samples, s.CoveragePct, s.DataMB, s.Restarts)
	b.WriteString("criterion  total  explained  unexplained\n")
	for _, k := range Keys {
		c := s.Keys[k]
		fmt.Fprintf(&b, "%-9s  %5d  %9d  %11d\n", k, c["total"], c["explained"], c["unexplained"])
	}
	if len(s.OpenEpisodes) > 0 {
		fmt.Fprintf(&b, "\nopen episodes: %s\n", strings.Join(s.OpenEpisodes, ", "))
	}
	fmt.Fprintf(&b, "annotations: %d\n", s.Annotations)
	if s.MailsDropped > 0 {
		fmt.Fprintf(&b, "mails dropped (outbox full): %d\n", s.MailsDropped)
	}
	return b.String()
}

func (l *Logger) daily(now time.Time) {
	day := int(now.Sub(l.start).Hours()/24) + 0
	all := Summarize(l.cfg.Dir, l.st, l.start, now)
	last := Summarize(l.cfg.Dir, l.st, now.Add(-24*time.Hour), now)
	body := fmt.Sprintf("Run %s, day %d, %s\n\nWhole run so far:\n%s\nLast 24 h:\n%s", l.cfg.RunID, day, now.UTC().Format(time.RFC3339), all.text(), last.text())
	l.mail(fmt.Sprintf("DAILY %s day %d", l.cfg.RunID, day), body)
	l.st.LastDaily = now.Unix()
}

func (l *Logger) finish(now time.Time) error {
	l.check(now)
	s := Summarize(l.cfg.Dir, l.st, l.start, time.Unix(l.st.EndEpoch, 0))
	l.st.Finished = true
	l.note(now, "logger finished")
	l.mail(fmt.Sprintf("END %s", l.cfg.RunID), fmt.Sprintf("Run %s finished %s.\n\n%s\nThe raw data is in %s; report: n5-ectest report <dir>.\n", l.cfg.RunID, now.UTC().Format(time.RFC3339), s.text(), filepath.Base(l.cfg.Dir)))
	if err := l.saveState(); err != nil {
		return err
	}
	if l.d.Meta != nil {
		l.d.Meta["finished"] = now.UTC().Format(time.RFC3339)
		l.d.Meta["log"] = s
		l.d.Meta["identity_changes"] = append([]string{}, l.st.IdentityChanges...)
		b, _ := json.MarshalIndent(l.d.Meta, "", "  ")
		_ = bundle.WriteAtomic(filepath.Join(l.cfg.Dir, bundle.Meta), append([]byte(l.red(string(b))), '\n'), 0o640)
	}
	if l.d.Notify != nil {
		l.d.Notify("STOPPING=1")
	}
	return bundle.WriteSums(l.cfg.Dir)
}

func cellI(v int) string {
	if v < 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func cellF(v float64, prec int) string {
	if math.IsNaN(v) {
		return ""
	}
	return strconv.FormatFloat(v, 'f', prec, 64)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
