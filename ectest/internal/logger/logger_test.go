package logger_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/logger"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
)

// The Go constants equal the watcher's thresholds and K1 pattern
// (tools/validation-watch/n5-validation.conf.example, design §9).
func TestConstantsMatchWatcher(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "tools", "validation-watch", "n5-validation.conf.example"))
	if err != nil {
		t.Skip("watcher config not found next to this module")
	}
	defer f.Close()
	conf := map[string]string{}
	sc := bufio.NewScanner(f)
	re := regexp.MustCompile(`^([A-Z0-9_]+)=(.*)$`)
	for sc.Scan() {
		if m := re.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			conf[m[1]] = strings.Trim(m[2], `'"`)
		}
	}
	want := map[string]string{
		"K1_PATTERN": logger.K1Pattern, "K1_IGNORE": logger.K1Ignore, "K1_IGNORE_PREVBOOT": logger.K1IgnorePrevBoot,
		"K1_MAX_PER_RUN": fmt.Sprint(logger.K1MaxPerRun), "CHECK_INTERVAL": fmt.Sprint(logger.CheckIntervalS),
		"CHECK_GAP_MAX": fmt.Sprint(logger.CheckGapMaxS), "GAP_MAX": fmt.Sprint(logger.GapMaxS),
		"TH_TCTL": fmt.Sprint(logger.ThTctl), "TH_NVME": fmt.Sprint(logger.ThNVMe), "TH_HDD": fmt.Sprint(logger.ThHDD), "TH_EC_CPU": fmt.Sprint(logger.ThECCPU),
		"DATA_MAX_MB": fmt.Sprint(logger.DataMaxMB), "ROOT_MIN_FREE_GB": fmt.Sprint(logger.RootMinFreeGB),
		"ALERT_COOLDOWN": fmt.Sprint(logger.AlertCooldownS), "ANNOT_WINDOW": fmt.Sprint(logger.AnnotWindowS), "COVER_MIN": fmt.Sprint(logger.CoverMin),
		"FANGOV_UNIT": logger.FangovUnit,
	}
	for k, v := range want {
		if conf[k] != v {
			t.Errorf("%s: watcher %q, n5-ectest %q", k, conf[k], v)
		}
	}
}

func TestK1Hit(t *testing.T) {
	for msg, want := range map[string]bool{
		"BUG: kernel NULL pointer dereference":                           true,
		"minisforum_n5_it5571 minisforum_n5_it5571: EC timeout":          true,
		"INFO: task txg_sync:1234 blocked for more than 122 seconds.":    true,
		"minisforum_n5_it5571: module verification failed: signature":    false,
		"r8169 0000:02:00.0 eth0: Link is Up":                            false,
		"watchdog: watchdog0: watchdog did not stop!":                    true,
		"WARNING: CPU: 3 PID: 812 at drivers/hwmon/foo.c:42 bar+0x1/0x2": true, // TESTSPEC 0.4 #15
	} {
		if logger.K1Hit(msg, false) != want {
			t.Errorf("%q: %v", msg, !want)
		}
	}
	if logger.K1Hit("watchdog: watchdog0: watchdog did not stop!", true) {
		t.Error("previous-boot shutdown line counted")
	}
}

// world is the fake outside of a run.
type world struct {
	mu       sync.Mutex
	t        time.Time
	n        int
	h        *fake.Host
	start    time.Time
	jour     []logger.JLine
	fg       string
	nr       int
	mails    []string
	mailErr  error
	onSample func(n int)
	cancelAt int
	cancel   context.CancelFunc
}

func (w *world) deps(meta map[string]any) logger.Deps {
	return logger.Deps{
		Now: func() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.t },
		Sleep: func(ctx context.Context, d time.Duration) bool {
			w.mu.Lock()
			w.t = w.t.Add(d)
			w.n++
			n := w.n
			up := w.t.Sub(w.start).Seconds() + 1000
			w.mu.Unlock()
			w.h.Put("proc/uptime", fmt.Sprintf("%.2f 0.00\n", up))
			if w.onSample != nil {
				w.onSample(n)
			}
			if w.cancelAt > 0 && n == w.cancelAt {
				w.cancel()
			}
			return ctx.Err() == nil
		},
		Fangov: func() (string, int, error) { return w.fg, w.nr, nil },
		Journal: func(unit, cursor string, since time.Time) ([]logger.JLine, string, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if unit != "" {
				return nil, cursor, nil
			}
			out := w.jour
			w.jour = nil
			return out, cursor + "x", nil
		},
		Mail: func(subject, body string) error {
			if w.mailErr != nil {
				return w.mailErr
			}
			w.mails = append(w.mails, subject)
			return nil
		},
		FreeBytes: func(string) (uint64, error) { return 100 << 30, nil },
		Redact:    identity.NewRedactor("", "fakehost-secret"),
		Meta:      meta,
	}
}

func newWorld(t *testing.T) *world {
	h := fake.New(t, fake.Reference())
	start := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	h.Put("proc/uptime", "1000.00 0.00\n")
	return &world{t: start, start: start, h: h, fg: "active", nr: 0}
}

func (w *world) count(key string) int {
	n := 0
	for _, m := range w.mails {
		if strings.Contains(m, "ALERT "+key+" ") {
			n++
		}
	}
	return n
}

func TestRunCriteria(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(t.TempDir(), "20261010-0000-F8NAA-1.05-log-t1")
	w.onSample = func(n int) {
		switch n {
		case 10: // fan2 stalls for 2 samples
			w.h.Put(w.h.HwmonRel()+"/fan2_input", "0\n")
		case 12:
			w.h.Put(w.h.HwmonRel()+"/fan2_input", "2236\n")
		case 20: // Tctl over the limit for 5 samples: one event
			w.h.SetTemp("tctl", "86000")
		case 25:
			w.h.SetTemp("tctl", "40000")
		case 30:
			w.mu.Lock()
			w.jour = []logger.JLine{{Time: w.t, Msg: "kernel: BUG: unable to handle page fault"}, {Time: w.t, Msg: "minisforum_n5_it5571: module verification failed"}}
			w.mu.Unlock()
		case 40:
			w.h.Put("proc/sys/kernel/tainted", "12289\n")
		case 50:
			w.fg = "failed"
		case 55:
			w.fg, w.nr = "active", 1
		case 60: // a hang of the sampler: 200 s between two samples
			w.mu.Lock()
			w.t = w.t.Add(200 * time.Second)
			w.mu.Unlock()
		case 70:
			w.h.Put(w.h.HwmonRel()+"/fan1_input", "garbage\n")
		case 71:
			w.h.Put(w.h.HwmonRel()+"/fan1_input", "2003\n")
		}
	}
	meta := map[string]any{"command": "log"}
	if err := logger.Run(context.Background(), logger.Config{RunID: "t1", Dir: dir, Duration: 26 * time.Hour, Interval: 60 * time.Second, MailTo: "root"}, w.deps(meta)); err != nil {
		t.Fatal(err)
	}
	evs := logger.ParseEventLog(dir)
	got := map[string]int{}
	for _, e := range evs {
		got[e.Key]++
	}
	want := map[string]int{"K1": 1, "K3": 2, "K4": 3, "K5": 1, "K7": 1}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s: %d events, want %d", k, got[k], n)
		}
	}
	if got["K2"] != 0 || got["K6"] != 0 {
		t.Errorf("unexpected events %v", got)
	}
	ev, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	for _, s := range []string{"fan2 0 rpm in 2 consecutive samples", "Tctl=86.0 >= 85", "BUG: unable to handle page fault", "kernel taint 12288 -> 12289", "ActiveState=failed", "NRestarts 0 -> 1", "gap 260 s between samples (monotonic", "1 EC read error"} {
		if !strings.Contains(string(ev), s) {
			t.Errorf("events lack %q:\n%s", s, ev)
		}
	}
	// mails: START, one per key (cooldown), a daily summary, END
	for _, s := range []string{"[n5-ectest] START t1", "[n5-ectest] DAILY t1 day 1", "[n5-ectest] END t1"} {
		found := false
		for _, m := range w.mails {
			found = found || strings.HasPrefix(m, s)
		}
		if !found {
			t.Errorf("no mail %q in %v", s, w.mails)
		}
	}
	// 3 K4 events, 2 K3 events: the cooldown (1 per key and hour) merges
	if w.count("K4") != 2 || w.count("K3") != 2 || w.count("K1") != 1 {
		t.Errorf("cooldown: K4 %d, K3 %d, K1 %d mails: %v", w.count("K4"), w.count("K3"), w.count("K1"), w.mails)
	}
	// files of the run
	for _, f := range []string{"samples.csv", "aux.csv", "events.log", "state.json", "meta.json", "SHA256SUMS"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if p := bundle.VerifySums(dir); len(p) != 0 {
		t.Fatalf("sums %v", p)
	}
	l, err := bundle.Load(dir)
	if err != nil || len(l.Samples) < 1500 || len(l.Samples) > 26*60 {
		t.Fatalf("samples %d %v", len(l.Samples), err)
	}
	if l.Samples[0].Phase != "log" || l.Samples[0].DCR[0] != 0x55 || l.Samples[0].Fan[2] < 0 {
		t.Fatalf("sample %+v", l.Samples[0])
	}
	st, _ := logger.LoadState(dir)
	s := logger.Summarize(dir, st, time.Unix(st.StartEpoch, 0), time.Unix(st.EndEpoch, 0))
	if s.Keys["K4"]["total"] != 3 || s.CoveragePct > 99.9 || s.CoveragePct < 99 {
		t.Fatalf("summary %+v", s)
	}
	if meta["finished"] == nil || meta["log"] == nil {
		t.Fatalf("meta not finished: %v", meta)
	}
	// after the end: no restart of a finished run
	if err := logger.Run(context.Background(), logger.Config{RunID: "t1", Dir: dir, Duration: time.Hour, Interval: time.Minute}, w.deps(nil)); err == nil {
		t.Fatal("finished run restarted")
	}
	// annotations explain events; K1 only per event
	var k1, k5 logger.Event
	for _, e := range evs {
		switch e.Key {
		case "K1":
			k1 = e
		case "K5":
			k5 = e
		}
	}
	if err := logger.AnnotateEvents(dir, w.t, []int{k1.ID}, "injected fault, \n test"); err != nil {
		t.Fatal(err)
	}
	if err := logger.AnnotateEvents(dir, w.t, []int{9999}, "x"); err == nil {
		t.Fatal("unknown event annotated")
	}
	if err := logger.AnnotateDown(dir, k5.Time.Add(-time.Minute), k5.Time, "planned"); err != nil {
		t.Fatal(err)
	}
	s = logger.Summarize(dir, st, time.Unix(st.StartEpoch, 0), time.Unix(st.EndEpoch, 0))
	if s.Keys["K1"]["explained"] != 1 || s.Keys["K5"]["explained"] != 1 || s.Keys["K4"]["unexplained"] != 3 {
		t.Fatalf("explained %+v", s.Keys)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, logger.Annotations)); strings.Contains(string(b), "\n test") {
		t.Fatal("annotation not one line")
	}
}

// A killed logger resumes from state.json; the gap is K5 (monotonic), a
// reboot K2.
func TestResumeAndReboot(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(t.TempDir(), "run")
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel, w.cancelAt = cancel, 5
	cfg := logger.Config{RunID: "r", Dir: dir, Duration: time.Hour, Interval: 30 * time.Second}
	if err := logger.Run(ctx, cfg, w.deps(nil)); err != nil {
		t.Fatal(err)
	}
	st, err := logger.LoadState(dir)
	if err != nil || st.Samples != 5 || st.Finished {
		t.Fatalf("state %+v %v", st, err)
	}
	// down for 5 minutes, same boot
	w.mu.Lock()
	w.t = w.t.Add(5 * time.Minute)
	w.mu.Unlock()
	w.h.Put("proc/uptime", fmt.Sprintf("%.2f 0\n", w.t.Sub(w.start).Seconds()+1000))
	ctx2, cancel2 := context.WithCancel(context.Background())
	w.cancel, w.cancelAt = cancel2, w.n+3
	if err := logger.Run(ctx2, cfg, w.deps(nil)); err != nil {
		t.Fatal(err)
	}
	// reboot: new boot id, uptime small
	w.h.Put("proc/sys/kernel/random/boot_id", "11111111-2222-3333-4444-555555555555\n")
	w.mu.Lock()
	w.t = w.t.Add(2 * time.Minute)
	w.start = w.t.Add(-10 * time.Second)
	w.mu.Unlock()
	w.cancelAt = 0
	if err := logger.Run(context.Background(), cfg, w.deps(nil)); err != nil {
		t.Fatal(err)
	}
	ev, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	for _, s := range []string{"logger resumed (restart 1)", "K5 #1 gap 330 s between samples (monotonic", "K2 #", "boot_id changed: 0f0e0d0c -> 11111111", "across a reboot", "logger finished"} {
		if !strings.Contains(string(ev), s) {
			t.Errorf("events lack %q:\n%s", s, ev)
		}
	}
}

// Without a working MTA mails go to the outbox beside the run and are sent later.
func TestOutboxAndCheck(t *testing.T) {
	w := newWorld(t)
	w.mailErr = errors.New("sendmail: exit 75")
	dir := filepath.Join(t.TempDir(), "run")
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel, w.cancelAt = cancel, 2
	cfg := logger.Config{RunID: "o", Dir: dir, Duration: time.Hour, Interval: 30 * time.Second, MailTo: "root"}
	if err := logger.Run(ctx, cfg, w.deps(nil)); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(logger.OutboxDir(dir))
	if len(ents) != 1 {
		t.Fatalf("outbox %v", ents)
	}
	b, _ := os.ReadFile(filepath.Join(logger.OutboxDir(dir), ents[0].Name()))
	if !strings.Contains(string(b), "Subject: [n5-ectest] START o") {
		t.Fatalf("queued: %s", b)
	}
	// outside check: fresh, then stale (mail once per hour), unit failed
	var mails []string
	m := func(s, _ string) error { mails = append(mails, s); return nil }
	st, _ := logger.LoadState(dir)
	last := time.Unix(st.LastSampleWall, 0)
	os.Chtimes(filepath.Join(dir, "samples.csv"), last, last)
	if r := logger.Check(dir, last.Add(time.Minute), m, ""); r.Stale || len(mails) != 0 {
		t.Fatalf("fresh: %+v %v", r, mails)
	}
	if r := logger.Check(dir, last.Add(15*time.Minute), m, ""); !r.Stale || len(mails) != 1 || !strings.Contains(mails[0], "ALERT K5 o: no sample for 900 s") {
		t.Fatalf("stale: %+v %v", r, mails)
	}
	logger.Check(dir, last.Add(20*time.Minute), m, "")
	if len(mails) != 1 {
		t.Fatalf("second stale mail within the hour: %v", mails)
	}
	logger.Check(dir, last.Add(21*time.Minute), m, "n5-ectest-log@o.service")
	if len(mails) != 2 || !strings.Contains(mails[1], "FAILED o: n5-ectest-log@o.service") {
		t.Fatalf("unit failed: %v", mails)
	}
	// the MTA works again: the queued mail is sent at the next check
	w.mailErr = nil
	w.cancelAt = 0
	if err := logger.Run(context.Background(), cfg, w.deps(nil)); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(logger.OutboxDir(dir)); len(ents) != 0 {
		t.Fatalf("outbox not flushed: %v", ents)
	}
	if len(w.mails) == 0 || w.mails[0] != "[n5-ectest] START o" {
		t.Fatalf("mails %v", w.mails)
	}
}

// A box without n5-fangov: K3 does not apply; n5-fangov stopped: one K3.
func TestK3WithoutFangov(t *testing.T) {
	for _, c := range []struct {
		state string
		want  int
	}{{logger.FangovNotInstalled, 0}, {"inactive", 1}} {
		w := newWorld(t)
		w.fg = c.state
		dir := filepath.Join(t.TempDir(), "run")
		if err := logger.Run(context.Background(), logger.Config{RunID: "k3", Dir: dir, Duration: time.Hour, Interval: 30 * time.Second}, w.deps(nil)); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range logger.ParseEventLog(dir) {
			if e.Key == "K3" {
				n++
			}
		}
		if n != c.want {
			t.Errorf("%s: %d K3 events, want %d", c.state, n, c.want)
		}
	}
}

func TestParseJournalJSON(t *testing.T) {
	out := `{"MESSAGE":"BUG: x","__REALTIME_TIMESTAMP":"1760054400000000","_BOOT_ID":"0f0e0d0c0b0a09080706050403020100","__CURSOR":"s=1"}
{"MESSAGE":[104,105],"__REALTIME_TIMESTAMP":"1760054401000000","__CURSOR":"s=2"}
not json
`
	lines, cur, err := logger.ParseJournalJSON(out)
	if err != nil || len(lines) != 2 || cur != "s=2" || lines[1].Msg != "hi" || lines[0].Time.Unix() != 1760054400 || lines[0].Boot == "" {
		t.Fatalf("%+v %q %v", lines, cur, err)
	}
}

// A finished run started again (unit still enabled at boot): READY=1 and
// STOPPING=1, so the Type=notify unit is not failed.
func TestFinishedRunNotifies(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(t.TempDir(), "run")
	cfg := logger.Config{RunID: "f", Dir: dir, Duration: 10 * time.Minute, Interval: 30 * time.Second}
	if err := logger.Run(context.Background(), cfg, w.deps(nil)); err != nil {
		t.Fatal(err)
	}
	var got []string
	d := w.deps(nil)
	d.Notify = func(s string) { got = append(got, s) }
	if err := logger.Run(context.Background(), cfg, d); !errors.Is(err, logger.ErrFinished) {
		t.Fatalf("err %v", err)
	}
	if strings.Join(got, ",") != "READY=1,STOPPING=1" {
		t.Fatalf("notify %v", got)
	}
}

// A dead MTA: at most one mail attempt per sampling pass, the watchdog fed
// before each attempt, every queued mail in its own file (two in the same
// second included).
func TestMailDownOneAttemptPerPass(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(t.TempDir(), "run")
	attempts := map[int]int{}
	watchdog := 0
	d := w.deps(nil)
	d.Mail = func(subject, body string) error {
		attempts[w.n]++
		return errors.New("sendmail: timed out")
	}
	d.Notify = func(s string) {
		if s == "WATCHDOG=1" {
			watchdog++
		}
	}
	// several criteria at once: alert mails for K2..K7 pile up in one check
	w.onSample = func(n int) {
		if n == 3 {
			w.h.SetTemp("tctl", "86000")
			w.h.Put("proc/sys/kernel/tainted", "12289\n")
			w.fg = "failed"
		}
	}
	cfg := logger.Config{RunID: "m", Dir: dir, Duration: 30 * time.Minute, Interval: 30 * time.Second, MailTo: "root"}
	if err := logger.Run(context.Background(), cfg, d); err != nil {
		t.Fatal(err)
	}
	for pass, n := range attempts {
		if n > 1 {
			t.Fatalf("%d mail attempts in pass %d", n, pass)
		}
	}
	ents, _ := os.ReadDir(logger.OutboxDir(dir))
	st, _ := logger.LoadState(dir)
	if len(ents) < 4 || len(ents) != st.MailSeq || watchdog < len(attempts) {
		t.Fatalf("outbox %d files, mail_seq %d, watchdog %d, attempts %v", len(ents), st.MailSeq, watchdog, attempts)
	}
	if _, err := os.Stat(filepath.Join(dir, "outbox")); err == nil {
		t.Fatal("outbox inside the run directory")
	}
	// the address is only in the outbox, never in the run directory
	b, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if strings.Contains(string(b), ".eml") {
		t.Fatalf("queued mails in SHA256SUMS:\n%s", b)
	}
}

// The outside check of a unit that fails at every start mails once per
// hour, with the count of the failures in between.
func TestUnitFailedCooldown(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(t.TempDir(), "run")
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel, w.cancelAt = cancel, 2
	if err := logger.Run(ctx, logger.Config{RunID: "u", Dir: dir, Duration: time.Hour, Interval: 30 * time.Second}, w.deps(nil)); err != nil {
		t.Fatal(err)
	}
	var mails []string
	m := func(s, b string) error { mails = append(mails, s+"\n"+b); return nil }
	at := w.t
	for i := 0; i < 10; i++ {
		logger.Check(dir, at.Add(time.Duration(i)*40*time.Second), m, "n5-ectest-log@u.service")
	}
	if len(mails) != 1 {
		t.Fatalf("10 failures in 6 min: %d mails", len(mails))
	}
	logger.Check(dir, at.Add(61*time.Minute), m, "n5-ectest-log@u.service")
	if len(mails) != 2 || !strings.Contains(mails[1], "(10 failure(s) since the last such mail") {
		t.Fatalf("after an hour: %v", mails)
	}
}

// A lasting EC read error is one K4 episode; a missing dcr is one K8 event;
// the software identity is recorded at the start and after a reboot, a
// change lands in meta.json.
func TestEpisodesAndIdentity(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(t.TempDir(), "run")
	ident := map[string]string{"kernel": "7.0.14-20-pve", "driver_version": "0.2.1+sr1"}
	w.onSample = func(n int) {
		switch n {
		case 5: // fan1 unreadable for 20 samples
			w.h.Put(w.h.HwmonRel()+"/fan1_input", "garbage\n")
		case 25:
			w.h.Put(w.h.HwmonRel()+"/fan1_input", "2003\n")
		case 30: // driver reloaded without debug_regs
			os.Remove(filepath.Join(w.h.Root, "sys", "kernel", "debug", "minisforum_n5_it5571", "dcr"))
		case 40: // reboot into another kernel
			ident = map[string]string{"kernel": "7.0.14-21-pve", "driver_version": "0.2.1+sr1"}
			w.h.Put("proc/sys/kernel/random/boot_id", "11111111-2222-3333-4444-555555555555\n")
		}
	}
	meta := map[string]any{}
	d := w.deps(meta)
	d.Identity = func() map[string]string { return ident }
	if err := logger.Run(context.Background(), logger.Config{RunID: "e", Dir: dir, Duration: 30 * time.Minute, Interval: 30 * time.Second}, d); err != nil {
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, e := range logger.ParseEventLog(dir) {
		n[e.Key]++
	}
	if n["K4"] != 1 || n["K8"] != 1 {
		t.Fatalf("events %v", n)
	}
	ev, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	for _, s := range []string{"identity (start): driver_version=0.2.1+sr1, kernel=7.0.14-20-pve", "identity changed (reboot): kernel: 7.0.14-20-pve -> 7.0.14-21-pve", "dcr not readable"} {
		if !strings.Contains(string(ev), s) {
			t.Errorf("events lack %q:\n%s", s, ev)
		}
	}
	if ch, _ := meta["identity_changes"].([]string); len(ch) != 1 {
		t.Fatalf("identity_changes %v", meta["identity_changes"])
	}
}

// Coverage reads samples.csv row by row and still finds the gaps.
func TestCoverageStreams(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	var b strings.Builder
	b.WriteString("t_iso,t_s,phase\n")
	for i := 0; i < 2000; i++ {
		if i >= 1000 && i < 1100 { // 100 samples missing: 500 s
			continue
		}
		fmt.Fprintf(&b, "%s,%d,log\n", t0.Add(time.Duration(i)*5*time.Second).Format(time.RFC3339), i*5)
	}
	os.WriteFile(filepath.Join(dir, "samples.csv"), []byte(b.String()), 0o640)
	c := logger.Coverage(dir, t0, t0.Add(9995*time.Second), 5, nil)
	want := 100 * (1 - (505.0-5)/9995)
	if c < want-0.01 || c > want+0.01 {
		t.Fatalf("coverage %.3f, want %.3f", c, want)
	}
}
