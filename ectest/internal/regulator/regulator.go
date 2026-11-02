// Package regulator detects other pwm writers (TESTSPEC G1, design §6),
// before a run and at every sample:
//
//	(a) systemd ActiveState of n5-fangov, n5-fand, fancontrol, fan2go,
//	    coolercontrold, thinkfan (activating/reloading/deactivating count as
//	    running);
//	(b) processes whose exact comm or argv[0] basename is one of those names
//	    (never a substring of the command line);
//	(c) /proc/*/fd links into .../hwmon/hwmonN/pwmN (persistent holders);
//	(d) the writer lock: the path still names the held file (inode);
//	(e) pwmN/pwmN_enable of every channel against the expected value, which
//	    is set before each own write (Expect), so a change n5-ectest did not
//	    make is a foreign write.
package regulator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/writerlock"
)

// Names are the regulators looked for as units and processes.
var Names = []string{"n5-fangov", "n5-fand", "fancontrol", "fan2go", "coolercontrold", "thinkfan"}

// ScriptNames are pwm-writing scripts looked for as processes only (comm of
// a script started through its shebang): the f8a tools of this repository.
var ScriptNames = []string{"f8a-pcie.sh", "f8a-stress.sh"}

// Systemctl returns the ActiveState of a unit; err when systemctl cannot
// be run. Tests replace it.
var Systemctl = func(unit string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", "-p", "ActiveState", "--value", unit+".service").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// UnitRunning reports a state that counts as running or about to run.
func UnitRunning(state string) bool {
	switch state {
	case "", "inactive", "failed", "unknown":
		return false
	}
	return true
}

// FangovState is the ActiveState of n5-fangov ("" = systemctl unusable).
func FangovState() string {
	s, err := Systemctl("n5-fangov")
	if err != nil {
		return ""
	}
	return s
}

// Checker runs the checks (a)-(e).
type Checker struct {
	ProcRoot string
	// Self are the pids of this tool (own process, guardian parent): their
	// fds and names are not foreign.
	Self map[int]bool
	// Lock is the held writer lock (nil: check d off).
	Lock *writerlock.Lock
	// Expect is the expected pwm state (nil: check e off).
	Expect *Expectation
}

var pwmFDRe = regexp.MustCompile(`/hwmon/hwmon[0-9]+/pwm[0-9]+(_enable)?$`)

// Units returns the regulator units that run (a), and a note when systemctl
// is not usable.
func Units() (running []string, note string) {
	for _, n := range Names {
		s, err := Systemctl(n)
		if err != nil {
			return running, "systemctl not usable (" + err.Error() + "); process check only"
		}
		if UnitRunning(s) {
			running = append(running, fmt.Sprintf("unit %s %s", n, s))
		}
	}
	return running, ""
}

// Processes returns regulator processes (b) and pwm fd holders (c).
func (c *Checker) Processes() []string {
	var hits []string
	ents, err := os.ReadDir(c.ProcRoot)
	if err != nil {
		return []string{"process list not readable: " + err.Error()}
	}
	names, scripts := map[string]bool{}, map[string]bool{}
	for _, n := range Names {
		names[n] = true
	}
	for _, n := range ScriptNames {
		names[n], scripts[n] = true, true
	}
	var pids []int
	for _, e := range ents {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	for _, pid := range pids {
		if c.Self[pid] {
			continue
		}
		d := filepath.Join(c.ProcRoot, strconv.Itoa(pid))
		comm := strings.TrimSpace(readRaw(filepath.Join(d, "comm")))
		argv0, script := "", ""
		if cl := readRaw(filepath.Join(d, "cmdline")); cl != "" {
			argv := strings.Split(cl, "\x00")
			argv0 = filepath.Base(argv[0])
			// `bash f8a-pcie.sh`: the script is argv[1]
			if (argv0 == "bash" || argv0 == "sh") && len(argv) > 1 {
				script = filepath.Base(argv[1])
			}
		}
		switch {
		case names[comm]:
			hits = append(hits, fmt.Sprintf("process %s (pid %d)", comm, pid))
		case names[argv0]:
			hits = append(hits, fmt.Sprintf("process %s (pid %d)", argv0, pid))
		case scripts[script]:
			hits = append(hits, fmt.Sprintf("process %s %s (pid %d)", argv0, script, pid))
		}
		fds, err := os.ReadDir(filepath.Join(d, "fd"))
		if err != nil {
			continue
		}
		for _, f := range fds {
			l, err := os.Readlink(filepath.Join(d, "fd", f.Name()))
			if err == nil && pwmFDRe.MatchString(l) {
				hits = append(hits, fmt.Sprintf("pid %d (%s) holds %s open", pid, orDash(comm), l))
			}
		}
	}
	return hits
}

// Check runs (a)-(e) and returns every hit; empty = no other writer.
func (c *Checker) Check() []string {
	hits, _ := Units()
	hits = append(hits, c.Processes()...)
	if c.Lock != nil {
		if err := c.Lock.Verify(); err != nil {
			hits = append(hits, err.Error())
		}
	}
	if c.Expect != nil {
		hits = append(hits, c.Expect.Diff()...)
	}
	return hits
}

func readRaw(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Expectation is the pwm state n5-ectest expects in the driver's cache. It
// models the driver: pwmN_enable=1 from another mode sets pwmN=255;
// pwmN=V sets V; pwmN_enable=2 leaves pwmN.
type Expectation struct {
	mu     sync.Mutex
	dir    func() string // the driver's current hwmon directory
	pwm    map[int]int
	enable map[int]int
}

// NewExpectation reads the current values of every channel that exposes
// them; dir returns the driver's current hwmon directory.
func NewExpectation(dir func() string) *Expectation {
	e := &Expectation{dir: dir, pwm: map[int]int{}, enable: map[int]int{}}
	for ch := 1; ch <= 4; ch++ {
		e.Refresh(ch)
	}
	return e
}

func (e *Expectation) read(ch int, a pwmw.Attr) (int, bool) {
	d := e.dir()
	if d == "" {
		return 0, false
	}
	s, err := sysroot.ReadTimeout(filepath.Join(d, a.Name(ch)), sysroot.DefaultTimeout)
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	return v, err == nil
}

// Refresh takes the channel's current values as expected (after an own
// write failed, or a module reload).
func (e *Expectation) Refresh(ch int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if v, ok := e.read(ch, pwmw.Duty); ok {
		e.pwm[ch] = v
	} else {
		delete(e.pwm, ch)
	}
	if v, ok := e.read(ch, pwmw.Enable); ok {
		e.enable[ch] = v
	} else {
		delete(e.enable, ch)
	}
}

// Expect records an own write before it happens.
func (e *Expectation) Expect(ch int, a pwmw.Attr, v int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch a {
	case pwmw.Duty:
		e.pwm[ch] = v
	case pwmw.Enable:
		if v == 1 && e.enable[ch] != 1 {
			e.pwm[ch] = 255
		}
		e.enable[ch] = v
	}
}

// After is the pwmw hook: a failed own write leaves an unknown state, so
// the channel's values are read again.
func (e *Expectation) After(ch int, _ pwmw.Attr, _ int, err error) {
	if err != nil {
		e.Refresh(ch)
	}
}

// Diff lists every attribute whose value differs from the expected one.
func (e *Expectation) Diff() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for ch := 1; ch <= 4; ch++ {
		for _, a := range []pwmw.Attr{pwmw.Enable, pwmw.Duty} {
			var want int
			var ok bool
			if a == pwmw.Enable {
				want, ok = e.enable[ch]
			} else {
				want, ok = e.pwm[ch]
			}
			if !ok {
				continue
			}
			got, ok := e.read(ch, a)
			if ok && got != want {
				out = append(out, fmt.Sprintf("%s is %d, expected %d: written by another program", a.Name(ch), got, want))
			}
		}
	}
	return out
}
