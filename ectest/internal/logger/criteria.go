// Package logger is the long-term sampler of n5-ectest (`log`, F8b and the
// raw data of T4/T5): a sample every interval into samples.csv (TESTSPEC §8
// columns) and aux.csv, the watcher criteria K1-K7 online, alert mails (one
// per key and hour), a daily summary, restart-safe state, and the outside
// check of `log check`. It never writes to sysfs and never does port I/O.
package logger

import "regexp"

// Criteria and thresholds, the same as tools/validation-watch
// (n5-validation.conf.example; a test compares them) and TESTSPEC §5a.
const (
	K1Pattern        = `Oops|BUG:|WARNING:|kernel BUG at|general protection|Call Trace|INFO: task .* blocked for more than|hung_task|soft lockup|hard LOCKUP|rcu.*stall|watchdog: |Hardware Error|mce:|Machine check|AER:|PCIe Bus Error|nvme.*(timeout|reset)|minisforum.*(timed? ?out|fail|error)`
	K1Ignore         = `NMI watchdog: Enabled|minisforum_n5_it5571: module verification failed|AER: enabled with IRQ`
	K1IgnorePrevBoot = `watchdog: watchdog[0-9]+: watchdog did not stop`
	K1MaxPerRun      = 300

	CheckIntervalS = 300 // journal, disk, alerts
	CheckGapMaxS   = 660 // `log check`: no sample for longer than this
	GapMaxS        = 90  // K5 (TESTSPEC §5a: sampler gap > 90 s)
	ThTctl         = 85.0
	ThNVMe         = 70.0
	ThHDD          = 50.0
	ThECCPU        = 90.0
	DataMaxMB      = 200
	RootMinFreeGB  = 5
	AlertCooldownS = 3600
	AnnotWindowS   = 600
	CoverMin       = 99.0

	// OutboxMaxMB bounds the queued mails when no MTA works.
	OutboxMaxMB = 20

	FangovUnit = "n5-fangov.service"
	// FangovNotInstalled is the state of a box without n5-fangov: K3 does
	// not apply.
	FangovNotInstalled = "not-installed"
)

// Keys of the criteria (TESTSPEC §5a K1-K7; K8 = the logger itself).
var Keys = []string{"K1", "K2", "K3", "K4", "K5", "K6", "K7", "K8"}

// KeyText describes a key in mails and the report.
var KeyText = map[string]string{
	"K1": "kernel log: Oops/BUG/WARNING/Call Trace/hung task/soft lockup/RCU stall/watchdog/MCE/AER/NVMe reset/EC error",
	"K2": "reboot (boot ID changed)",
	"K3": "n5-fangov inactive, restart counter increased, or warnings in its journal",
	"K4": "fan at 0 rpm in two consecutive samples, a temperature limit, EC read errors or the EC hwmon missing",
	"K5": "sampler gap > 90 s",
	"K6": "data directory above its limit or free space of the log target low",
	"K7": "kernel taint changed against the start of the run",
	"K8": "the logger itself: journal unreadable, state file not written, duty registers (dcr) not readable",
}

var (
	k1Re         = regexp.MustCompile(K1Pattern)
	k1IgnoreRe   = regexp.MustCompile(K1Ignore)
	k1PrevBootRe = regexp.MustCompile(K1IgnorePrevBoot)
)

// K1Hit reports whether a kernel log line counts for K1. prevBoot: the line
// belongs to an earlier boot.
func K1Hit(msg string, prevBoot bool) bool {
	if !k1Re.MatchString(msg) || k1IgnoreRe.MatchString(msg) {
		return false
	}
	if prevBoot && k1PrevBootRe.MatchString(msg) {
		return false
	}
	return true
}
