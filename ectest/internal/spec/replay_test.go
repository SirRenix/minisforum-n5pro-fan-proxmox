package spec

// Replay of raw logs from before TESTSPEC (design §11: replay runs only in
// tests, it is not a user command).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// Replay evaluates T2/T3 from raw logs of the pre-TESTSPEC measurement of
// 2026-09-14 (n5pro-ec befunde/raw/06-pwmtest-ch*.txt and
// 06-i2ec-dcr-*.txt): the last duty the pwm test wrote, then the DCR
// snapshots taken after the hand-back. The logs carry no timestamp of the
// auto command and only one direction (no H/M pair), so the result can at
// best show that the channel left the written value; it never reaches PASS.

// PWMTestLog is what Replay needs from a 06-pwm-test log.
type PWMTestLog struct {
	Channel  int
	Started  time.Time // from the header line
	LastDuty int       // the last "--- pwmN = V" step
	HandBack bool      // the log contains the restore to EC automatic
}

var (
	pwmHeaderRe = regexp.MustCompile(`^=== 06-pwm-test\s+Kanal (\d+)\s+(\S+) ===`)
	pwmStepRe   = regexp.MustCompile(`^--- pwm(\d+) = (\d+)\b`)
	sectionRe   = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?:-\d{1,2}:\d{2})?\s+(.*)$`)
)

// ParsePWMTest reads a 06-pwm-test log.
func ParsePWMTest(s string) (PWMTestLog, error) {
	var l PWMTestLog
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := pwmHeaderRe.FindStringSubmatch(line); m != nil {
			l.Channel, _ = strconv.Atoi(m[1])
			if t, err := time.Parse(time.RFC3339, m[2]); err == nil {
				l.Started = t
			}
			continue
		}
		if m := pwmStepRe.FindStringSubmatch(line); m != nil {
			ch, _ := strconv.Atoi(m[1])
			v, _ := strconv.Atoi(m[2])
			if l.Channel == 0 || ch == l.Channel {
				l.Channel, l.LastDuty = ch, v
			}
			continue
		}
		if strings.Contains(line, "Wiederherstellung") || strings.Contains(line, "_enable=2") {
			l.HandBack = true
		}
	}
	if l.Channel == 0 || l.LastDuty == 0 {
		return l, fmt.Errorf("replay: pwm test log without channel or duty step")
	}
	if !l.HandBack {
		return l, fmt.Errorf("replay: pwm test log without the hand-back to EC automatic")
	}
	return l, nil
}

// I2ECSnapshot is one register dump of a section of the I2EC log.
type I2ECSnapshot struct {
	Clock   string // "17:20"
	Minutes int    // minutes since midnight
	DCR     n5driver.DCR
}

// ParseI2EC reads the DCR snapshots of a hand log: sections start with
// "HH:MM <text>", dump lines are "18xx: hex bytes". Sections from the first
// one that records a new write (text contains "manuell", "manual" or
// "aktiv") on are ignored: from there the channel no longer shows the
// hand-back.
func ParseI2EC(s string) ([]I2ECSnapshot, error) {
	var out []I2ECSnapshot
	clock, minutes, stopped := "", -1, false
	var cur []string
	flush := func() error {
		if len(cur) == 0 || minutes < 0 {
			cur = nil
			return nil
		}
		for _, l := range cur {
			d, err := n5driver.ParseDCR(l)
			if err != nil {
				continue // a dump line outside DCR1..5 (e.g. 0x1810)
			}
			out = append(out, I2ECSnapshot{Clock: clock, Minutes: minutes, DCR: d})
		}
		cur = nil
		return nil
	}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			low := strings.ToLower(m[3])
			if strings.Contains(low, "manuell") || strings.Contains(low, "manual") || strings.Contains(low, "aktiv") {
				stopped = true
			}
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			clock, minutes = m[1]+":"+m[2], h*60+mi
			continue
		}
		if stopped {
			continue
		}
		if a, _, ok := strings.Cut(line, ":"); ok && len(a) == 4 && strings.HasPrefix(a, "18") {
			if i := strings.IndexByte(line, '('); i >= 0 {
				line = line[:i] // "1802: 00 55 4a 57 57 8c   (unveraendert, ...)"
			}
			cur = append(cur, line)
		}
	}
	_ = flush()
	if len(out) == 0 {
		return nil, fmt.Errorf("replay: no DCR snapshot in the I2EC log")
	}
	return out, nil
}

// Replay builds T2/T3 for the channel of the pwm test log from the DCR
// snapshots. The channel's DCR (pwm3 -> DCR3) is compared with the last
// written duty; sample times are minutes since the first snapshot, because
// the log has no timestamp of the auto command (TimingKnown false).
func Replay(pwmLog, i2ecLog string) ([]Result, error) {
	p, err := ParsePWMTest(pwmLog)
	if err != nil {
		return nil, err
	}
	snaps, err := ParseI2EC(i2ecLog)
	if err != nil {
		return nil, err
	}
	run := HandbackRun{Label: "replay", Written: p.LastDuty}
	t0 := snaps[0].Minutes
	for _, s := range snaps {
		v, ok := s.DCR.Channel(p.Channel)
		if !ok {
			return nil, fmt.Errorf("replay: channel pwm%d has no DCR", p.Channel)
		}
		run.Samples = append(run.Samples, DCRSample{T: float64(s.Minutes-t0) * 60, DCR: v})
	}
	sub := EvalHandback(run)
	t2, t3 := AggregateHandback(p.Channel, [][]SubResult{{sub}}, []string{"H", "M"})
	note := fmt.Sprintf("replay of raw logs (pwm test started %s, last written duty %d, %d DCR snapshot(s) %s..%s, time of the auto command not logged)",
		p.Started.Format(time.RFC3339), p.LastDuty, len(snaps), snaps[0].Clock, snaps[len(snaps)-1].Clock)
	t2.Reasons = append([]string{note}, t2.Reasons...)
	t3.Reasons = append([]string{note}, t3.Reasons...)
	return []Result{t2, t3}, nil
}

// REPLAY of the 2026-09-14 measurement (unchanged copies of n5pro-ec
// befunde/raw/06-pwmtest-ch3-20260914-171352.txt and
// 06-i2ec-dcr-20260914.txt): after the hand-back of pwm3 the HDD duty
// register read 0x57 where 0x8c (140) had been written. T2's criterion
// "left the written value" is met; the verdict is INCONCLUSIVE (one
// direction, n=1, no timestamp of the auto command), never FAIL — the
// 2026-09-14 conclusion "the EC does not resume HDD regulation" came from
// a tach criterion and was wrong.
func TestReplay20260914(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "replay-20260914")
	pwm, err := os.ReadFile(filepath.Join(dir, "06-pwmtest-ch3-20260914-171352.txt"))
	if err != nil {
		t.Fatal(err)
	}
	i2ec, err := os.ReadFile(filepath.Join(dir, "06-i2ec-dcr-20260914.txt"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePWMTest(string(pwm))
	if err != nil || p.Channel != 3 || p.LastDuty != 0x8c || !p.HandBack {
		t.Fatalf("pwm test log: %+v %v", p, err)
	}
	snaps, err := ParseI2EC(string(i2ec))
	if err != nil {
		t.Fatal(err)
	}
	// 17:20 full dump and 17:21 partial dump; the 17:29 manual write and
	// everything after it are not part of the hand-back
	if len(snaps) != 2 || snaps[0].Clock != "17:20" || snaps[1].Clock != "17:21" {
		t.Fatalf("snapshots: %+v", snaps)
	}
	for _, s := range snaps {
		if v, _ := s.DCR.Channel(3); v != 0x57 || s.DCR.Reg(4) != 0x57 {
			t.Fatalf("%s: DCR3/4 = %#x/%#x, want 0x57", s.Clock, v, s.DCR.Reg(4))
		}
	}
	res, err := Replay(string(pwm), string(i2ec))
	if err != nil {
		t.Fatal(err)
	}
	t2, t3 := res[0], res[1]
	if t2.ID != "T2" || t2.Channel != 3 || t2.Verdict != Inconclusive {
		t.Fatalf("T2 = %s %s, want INCONCLUSIVE", t2.ID, t2.Verdict)
	}
	if len(t2.Criteria) != 1 || t2.Criteria[0].Met == nil || !*t2.Criteria[0].Met || !strings.Contains(t2.Criteria[0].Name, "left written value") {
		t.Fatalf("left-written-value criterion: %+v", t2.Criteria)
	}
	sub := t2.Runs[0].Subs[0]
	if !sub.Left || sub.Written != 0x8c || sub.MaxDev != 0x8c-0x57 || sub.LeftAtS != nil || sub.T2 != Inconclusive {
		t.Fatalf("sub-run: %+v", sub)
	}
	joined := strings.Join(t2.Reasons, "\n")
	for _, want := range []string{"n=1 of 3 runs", "sub-run H, M missing (one direction only)", "time since the auto command not recorded", "last written duty 140"} {
		if !strings.Contains(joined, want) {
			t.Errorf("T2 reasons lack %q:\n%s", want, joined)
		}
	}
	if t3.ID != "T3" || t3.Verdict != Inconclusive {
		t.Fatalf("T3 = %s", t3.Verdict)
	}
	if b, err := json.Marshal(res); err != nil || !strings.Contains(string(b), `"verdict":"INCONCLUSIVE"`) {
		t.Fatalf("json: %v %s", err, b)
	}
}
