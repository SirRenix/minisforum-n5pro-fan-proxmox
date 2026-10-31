package logger

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
)

// ---- mail ---------------------------------------------------------------

var subjectClean = regexp.MustCompile(`[^\x20-\x7e]`)

// MailTimeout bounds one mail attempt. With at most one failed attempt per
// sampling pass the sampler stays well inside GapMaxS and WatchdogSec=180.
const MailTimeout = 30 * time.Second

// trySend makes one mail attempt, unless one failed already in this loop
// pass (mailDown). The watchdog is fed before the attempt: a send may take
// up to its timeout.
func (l *Logger) trySend(subject, body string) bool {
	if l.d.Mail == nil || l.mailDown {
		return false
	}
	if l.d.Notify != nil {
		l.d.Notify("WATCHDOG=1")
	}
	if err := l.d.Mail(subject, body); err != nil {
		l.mailDown = true
		return false
	}
	return true
}

// mail sends through Deps.Mail; on error (or without an MTA) the message
// goes to the outbox (OutboxDir) and stdout. The outbox is bounded
// (OutboxMaxMB); every queued mail gets its own file.
func (l *Logger) mail(subject, body string) {
	subject = "[n5-ectest] " + subjectClean.ReplaceAllString(l.red(subject), "")
	body = l.red(body)
	if l.trySend(subject, body) {
		return
	}
	dir := OutboxDir(l.cfg.Dir)
	if dirSize(dir) > OutboxMaxMB*1024*1024 {
		l.st.MailsDropped++
		return
	}
	_ = os.MkdirAll(dir, 0o750)
	l.st.MailSeq++
	// sortable (time, sequence) and unique (CreateTemp, O_EXCL)
	f, err := os.CreateTemp(dir, fmt.Sprintf("%011d-%06d-*.eml", l.d.Now().Unix(), l.st.MailSeq))
	if err != nil {
		l.st.MailsDropped++
		return
	}
	msg := fmt.Sprintf("To: %s\nSubject: %s\nMIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\n%s\n", l.cfg.MailTo, subject, body)
	_, werr := f.WriteString(msg)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		l.st.MailsDropped++
		return
	}
	_ = os.Chmod(f.Name(), 0o640)
	if l.d.Print != nil {
		l.d.Print("mail queued in " + f.Name() + ": " + subject)
	}
}

// flushOutbox retries queued mails (oldest first) while the MTA works.
func (l *Logger) flushOutbox() {
	if l.d.Mail == nil || l.mailDown {
		return
	}
	dir := OutboxDir(l.cfg.Dir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".eml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		head, body, _ := strings.Cut(string(b), "\n\n")
		subj := ""
		for _, h := range strings.Split(head, "\n") {
			if v, ok := strings.CutPrefix(h, "Subject: "); ok {
				subj = v
			}
		}
		if !l.trySend(subj, strings.TrimSuffix(body, "\n")) {
			return
		}
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// CommandMailer returns a Mail function that runs cmd without a shell:
// mail/mailx/s-nail get "-s SUBJECT RCPT" and the body on stdin; sendmail,
// msmtp and others get "RCPT" and the message with headers on stdin. No log
// line ever goes into argv. One attempt takes at most MailTimeout.
func CommandMailer(cmd []string, rcpt string) func(subject, body string) error {
	return func(subject, body string) error {
		if len(cmd) == 0 || rcpt == "" {
			return fmt.Errorf("no mail command or recipient")
		}
		if strings.HasPrefix(rcpt, "-") {
			// it would be read as an option of the mail command
			return fmt.Errorf("recipient starts with '-': refused")
		}
		ctx, cancel := context.WithTimeout(context.Background(), MailTimeout)
		defer cancel()
		args := append([]string{}, cmd[1:]...)
		var stdin string
		switch filepath.Base(cmd[0]) {
		case "mail", "mailx", "s-nail", "bsd-mailx":
			args = append(args, "-s", subject, rcpt)
			stdin = body + "\n"
		default:
			args = append(args, rcpt)
			stdin = fmt.Sprintf("To: %s\nSubject: %s\nMIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\nContent-Transfer-Encoding: 8bit\n\n%s\n", rcpt, subject, body)
		}
		c := exec.CommandContext(ctx, cmd[0], args...)
		c.Stdin = strings.NewReader(stdin)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v: %.200s", cmd[0], err, out)
		}
		return nil
	}
}

// ---- journal ------------------------------------------------------------

// JournalCtl reads the journal through journalctl -o json (kernel log for
// unit "", else the unit's lines with priority warning or higher).
func JournalCtl(unit, cursor string, since time.Time) ([]JLine, string, error) {
	args := []string{"-q", "--no-pager", "-o", "json"}
	if unit == "" {
		args = append(args, "-k")
	} else {
		args = append(args, "-u", unit, "-p", "warning")
	}
	if cursor != "" {
		args = append(args, "--after-cursor="+cursor)
	} else {
		args = append(args, "--since=@"+strconv.FormatInt(since.Unix(), 10))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", args...).Output()
	if err != nil {
		return nil, "", fmt.Errorf("journalctl: %w", err)
	}
	return ParseJournalJSON(string(out))
}

// Kmsg reads the kernel ring buffer from path (/dev/kmsg) without blocking,
// after the sequence number in cursor ("kmsg:<seq>"); the fallback for K1
// when there is no journal. Times are approximate (boot time + stamp).
func Kmsg(path, cursor string, bootTime time.Time) ([]JLine, string, error) {
	recs, err := kmsgRecords(path)
	if err != nil {
		return nil, "", err
	}
	after := int64(-1)
	if s, ok := strings.CutPrefix(cursor, "kmsg:"); ok {
		after, _ = strconv.ParseInt(s, 10, 64)
	}
	var out []JLine
	cur := cursor
	for _, rec := range recs {
		head, msg, ok := strings.Cut(rec, ";")
		if !ok {
			continue
		}
		fl := strings.Split(head, ",")
		if len(fl) < 3 {
			continue
		}
		seq, err1 := strconv.ParseInt(fl[1], 10, 64)
		us, err2 := strconv.ParseInt(fl[2], 10, 64)
		if err1 != nil || err2 != nil || seq <= after {
			continue
		}
		msg, _, _ = strings.Cut(msg, "\n")
		out = append(out, JLine{Time: bootTime.Add(time.Duration(us) * time.Microsecond), Msg: msg})
		cur = "kmsg:" + strconv.FormatInt(seq, 10)
	}
	return out, cur, nil
}

// ParseJournalJSON parses `journalctl -o json` output.
func ParseJournalJSON(out string) ([]JLine, string, error) {
	var lines []JLine
	cur := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		jl := JLine{}
		switch v := m["MESSAGE"].(type) {
		case string:
			jl.Msg = v
		case []any: // non-UTF-8 message: byte array
			var b []byte
			for _, x := range v {
				if f, ok := x.(float64); ok {
					b = append(b, byte(f))
				}
			}
			jl.Msg = string(b)
		}
		if s, ok := m["__REALTIME_TIMESTAMP"].(string); ok {
			if us, err := strconv.ParseInt(s, 10, 64); err == nil {
				jl.Time = time.UnixMicro(us)
			}
		}
		jl.Boot, _ = m["_BOOT_ID"].(string)
		jl.Boot = strings.ReplaceAll(jl.Boot, "-", "")
		jl.Cursor, _ = m["__CURSOR"].(string)
		if jl.Cursor != "" {
			cur = jl.Cursor
		}
		lines = append(lines, jl)
	}
	return lines, cur, sc.Err()
}

// ---- events, annotations, coverage -----------------------------------------

// Event is one line of a log run's events.log.
type Event struct {
	Time   time.Time
	Key    string
	ID     int
	Detail string
	Line   string
}

var eventLineRe = regexp.MustCompile(`^(\S+) \[t_s=[0-9.-]+\] (K[0-9]) #([0-9]+) (.*)$`)

// ParseEventLog reads the criterion events of a run directory.
func ParseEventLog(dir string) []Event {
	b, err := os.ReadFile(filepath.Join(dir, bundle.Events))
	if err != nil {
		return nil
	}
	var out []Event
	for _, l := range strings.Split(string(b), "\n") {
		m := eventLineRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, m[1])
		if err != nil {
			continue
		}
		id, _ := strconv.Atoi(m[3])
		out = append(out, Event{Time: t, Key: m[2], ID: id, Detail: m[4], Line: l})
	}
	return out
}

// Annotation is one line of annotations.txt: "<time> #12,#15 text" or
// "<time> DOWN <time-end> text".
type Annotation struct {
	Time   time.Time
	Events map[int]bool
	Down   bool
	End    time.Time
	Text   string
	Raw    string
}

// ParseAnnotations reads annotations.txt of a run directory.
func ParseAnnotations(dir string) []Annotation {
	b, err := os.ReadFile(filepath.Join(dir, Annotations))
	if err != nil {
		return nil
	}
	var out []Annotation
	for _, raw := range strings.Split(string(b), "\n") {
		f := strings.Fields(raw)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		t, err := time.Parse(time.RFC3339, f[0])
		if err != nil {
			continue
		}
		a := Annotation{Time: t, Raw: raw, Events: map[int]bool{}}
		if f[1] == "DOWN" {
			if len(f) < 3 {
				continue
			}
			e, err := time.Parse(time.RFC3339, f[2])
			if err != nil {
				continue
			}
			a.Down, a.End, a.Text = true, e, strings.Join(f[3:], " ")
		} else {
			for _, r := range strings.Split(f[1], ",") {
				if n, err := strconv.Atoi(strings.TrimPrefix(r, "#")); err == nil {
					a.Events[n] = true
				}
			}
			a.Text = strings.Join(f[2:], " ")
		}
		out = append(out, a)
	}
	return out
}

// Explained reports whether an annotation explains e: per event number
// (any key), or a DOWN interval for K2/K5 events in [from, to+window]
// (K1 and K7 only per event, as the watcher).
func Explained(e Event, ann []Annotation) bool {
	for _, a := range ann {
		if a.Events[e.ID] {
			return true
		}
		if a.Down && (e.Key == "K2" || e.Key == "K5") && !e.Time.Before(a.Time) && !e.Time.After(a.End.Add(AnnotWindowS*time.Second)) {
			return true
		}
	}
	return false
}

// Coverage is the share of [from, to] covered by samples (a gap above
// GapMaxS counts as uncovered minus one interval, planned downtime
// excluded), in percent. samples.csv is read row by row (a 30-day run at
// 5 s has half a million rows; the log unit has MemoryMax=128M).
func Coverage(dir string, from, to time.Time, intervalS float64, ann []Annotation) float64 {
	f, err := os.Open(filepath.Join(dir, bundle.Samples))
	if err != nil {
		return 0
	}
	defer f.Close()
	span := to.Sub(from).Seconds()
	if span <= 0 {
		return 100
	}
	unc := 0.0
	gap := func(a, b time.Time) {
		g := b.Sub(a).Seconds()
		if g <= GapMaxS {
			return
		}
		ov := 0.0
		for _, x := range ann {
			if !x.Down {
				continue
			}
			lo, hi := maxT(a, x.Time), minT(b, x.End)
			if hi.After(lo) {
				ov += hi.Sub(lo).Seconds()
			}
		}
		if u := g - intervalS - ov; u > 0 {
			unc += u
		}
	}
	prev := from
	if err := bundle.ScanTimes(f, func(t time.Time) {
		if t.Before(from) || t.After(to) {
			return
		}
		gap(prev, t)
		prev = t
	}); err != nil {
		return 0
	}
	gap(prev, to)
	c := 100 * (1 - unc/span)
	if c < 0 {
		c = 0
	}
	return c
}

func maxT(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minT(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// ---- annotate and the outside check ---------------------------------------

// AnnotateEvents appends "<now> #N,#M text" after checking the numbers.
func AnnotateEvents(dir string, now time.Time, ids []int, text string) error {
	if strings.TrimSpace(text) == "" || len(ids) == 0 {
		return fmt.Errorf("annotate: event number(s) and a text are needed")
	}
	have := map[int]bool{}
	for _, e := range ParseEventLog(dir) {
		have[e.ID] = true
	}
	var tok []string
	for _, id := range ids {
		if !have[id] {
			return fmt.Errorf("annotate: no event #%d in %s", id, filepath.Join(dir, bundle.Events))
		}
		tok = append(tok, "#"+strconv.Itoa(id))
	}
	return appendLine(filepath.Join(dir, Annotations), fmt.Sprintf("%s %s %s", now.UTC().Format(time.RFC3339), strings.Join(tok, ","), oneLine(text)))
}

// AnnotateDown appends a planned downtime.
func AnnotateDown(dir string, from, to time.Time, text string) error {
	if to.Before(from) {
		return fmt.Errorf("annotate: TO before FROM")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("annotate: a text is needed")
	}
	return appendLine(filepath.Join(dir, Annotations), fmt.Sprintf("%s DOWN %s %s", from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), oneLine(text)))
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// CheckResult is the outcome of the outside check.
type CheckResult struct {
	Stale    bool
	AgeS     int64
	Finished bool
	Text     string
}

// CheckFile is the outside checker's own state in the run directory.
const CheckFile = "check.json"

// Check is `log check`: it reads only state.json and the age of
// samples.csv and reports K5 when no sample was written for more than
// CheckGapMaxS. It mails at most once per hour (check.json).
func Check(dir string, now time.Time, mail func(subject, body string) error, unitFailed string) CheckResult {
	st, err := LoadState(dir)
	if err != nil {
		return CheckResult{Stale: true, Text: "no state.json: " + err.Error()}
	}
	if st.Finished {
		return CheckResult{Finished: true, Text: "run " + st.RunID + " finished"}
	}
	last := st.LastSampleWall
	if fi, err := os.Stat(filepath.Join(dir, bundle.Samples)); err == nil && fi.ModTime().Unix() > last {
		last = fi.ModTime().Unix()
	}
	if last == 0 {
		last = st.StartEpoch
	}
	r := CheckResult{AgeS: now.Unix() - last}
	r.Stale = r.AgeS > CheckGapMaxS
	r.Text = fmt.Sprintf("run %s: last sample %d s ago (%d samples)", st.RunID, r.AgeS, st.Samples)
	var cs struct {
		LastMail       int64 `json:"last_mail_epoch"`
		LastFailedMail int64 `json:"last_failed_mail_epoch"`
		FailedSince    int   `json:"failed_since_last_mail"`
	}
	cp := filepath.Join(dir, CheckFile)
	if b, err := os.ReadFile(cp); err == nil {
		_ = json.Unmarshal(b, &cs)
	}
	subject, body := "", ""
	switch {
	case unitFailed != "":
		// a unit that fails at every start (disk full) restarts every 30 s:
		// one mail per AlertCooldownS, with the count of the failures between
		cs.FailedSince++
		if now.Unix()-cs.LastFailedMail >= AlertCooldownS {
			subject = fmt.Sprintf("[n5-ectest] FAILED %s: %s", st.RunID, subjectClean.ReplaceAllString(unitFailed, ""))
			body = fmt.Sprintf("The log unit %s failed (%d failure(s) since the last such mail; at most one mail per hour). %s.\nsystemd restarts it (Restart=on-failure, at most 10 starts per hour); the gap is recorded as K5.\nStatus: systemctl status %s\n", unitFailed, cs.FailedSince, r.Text, unitFailed)
			cs.LastFailedMail, cs.FailedSince = now.Unix(), 0
		}
	case r.Stale && now.Unix()-cs.LastMail >= AlertCooldownS:
		subject = fmt.Sprintf("[n5-ectest] ALERT K5 %s: no sample for %d s", st.RunID, r.AgeS)
		body = fmt.Sprintf("Outside check (n5-ectest log check): %s, limit %d s.\nThe sampler is stopped or hangs. Status: systemctl status n5-ectest-log@%s\n", r.Text, CheckGapMaxS, st.RunID)
		cs.LastMail = now.Unix()
	}
	if subject != "" && mail != nil {
		_ = mail(subject, body)
	}
	if unitFailed != "" || subject != "" {
		b, _ := json.Marshal(cs)
		_ = bundle.WriteAtomic(cp, b, 0o640)
	}
	return r
}
