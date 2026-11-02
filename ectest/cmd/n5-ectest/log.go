package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/boards"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/logger"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
)

func init() {
	register("log", command{usage: "log [--duration 7d] [--interval 30s] [--mail-to ADDR] [--mail-cmd CMD] [--run-id ID] [--out DIR] | log check RUN | log annotate RUN (--event N | --down FROM TO) TEXT",
		help: "long-term log with the K1-K7 checks and alert mails (read-only; F8b, T4/T5 raw data)", run: cmdLog})
}

// defaultLogDir is where the units keep the runs.
const defaultLogDir = "/var/lib/n5-ectest"

var runIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// validRunID: [A-Za-z0-9._-]{1,64}, but not "." or ".." (the id is a path
// element below --out).
func validRunID(id string) bool {
	return runIDRe.MatchString(id) && id != "." && id != ".."
}

// checkMailTo refuses a recipient the mail command would read as an
// option (it goes into argv without "--").
func checkMailTo(cmd, to string) bool {
	if strings.HasPrefix(to, "-") {
		fmt.Fprintf(stderr, "%s: --mail-to must not start with '-'\n", cmd)
		return false
	}
	return true
}

// Test hooks of the logger.
var (
	logFangov  = fangovShow
	logJournal = func(unit, cursor string, since time.Time) ([]logger.JLine, string, error) {
		lines, cur, err := logger.JournalCtl(unit, cursor, since)
		if err != nil && unit == "" && errors.Is(err, exec.ErrNotFound) {
			// no journal: the kernel ring buffer (cursor = sequence number)
			if !strings.HasPrefix(cursor, "kmsg:") {
				cursor = ""
			}
			up := 0.0
			if f := strings.Fields(sysroot.Read(filepath.Join(sysroot.Proc(), "uptime"))); len(f) > 0 {
				up, _ = strconv.ParseFloat(f[0], 64)
			}
			return logger.Kmsg(filepath.Join(sysroot.Dev(), "kmsg"), cursor, now().Add(-time.Duration(up*float64(time.Second))))
		}
		return lines, cur, err
	}
	logFree = freeBytes
)

// fangovShow reads ActiveState and NRestarts of n5-fangov; a box without
// the unit gives logger.FangovNotInstalled (K3 does not apply there).
func fangovShow() (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", logger.FangovUnit, "-p", "LoadState", "-p", "ActiveState", "-p", "NRestarts").Output()
	if err != nil {
		return "", 0, err
	}
	st, load, nr := "", "", 0
	for _, l := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
		switch k {
		case "LoadState":
			load = v
		case "ActiveState":
			st = v
		case "NRestarts":
			nr, _ = strconv.Atoi(v)
		}
	}
	if load == "not-found" {
		return logger.FangovNotInstalled, 0, nil
	}
	return st, nr, nil
}

// sdNotify sends a state to systemd (Type=notify, WatchdogSec=).
func sdNotify(state string) {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return
	}
	addr := &net.UnixAddr{Name: sock, Net: "unixgram"}
	if strings.HasPrefix(sock, "@") {
		addr.Name = "\x00" + sock[1:]
	}
	c, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte(state))
}

// parseDays accepts Go durations and "<n>d".
func parseDays(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(d, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		return time.Duration(n * 24 * float64(time.Hour)), nil
	}
	return time.ParseDuration(s)
}

// mailer picks the mail path: --mail-cmd, else sendmail when present, else
// none (outbox and stdout). The note goes into meta.json: it names the
// command, never the address.
func mailer(cmdLine, to string) (func(string, string) error, string) {
	if to == "" {
		return nil, "no --mail-to: mails go to the outbox beside the run and stdout"
	}
	var cmd []string
	if cmdLine != "" {
		cmd = strings.Fields(cmdLine)
	} else {
		for _, p := range []string{"/usr/sbin/sendmail", "/usr/bin/sendmail", "/usr/bin/msmtp"} {
			if _, err := os.Stat(p); err == nil {
				cmd = []string{p}
				break
			}
		}
	}
	if len(cmd) == 0 {
		return nil, "no sendmail and no --mail-cmd: mails go to the outbox beside the run and stdout"
	}
	return logger.CommandMailer(cmd, to), "mail via " + cmd[0] + ", recipient set"
}

// logIdentity is the software identity the logger records at the start,
// on resume and after a reboot.
func logIdentity() map[string]string {
	id := collectIdentity()
	var ps []string
	for _, k := range []string{"force", "experimental_write", "debug_regs"} {
		if v, ok := id.Driver.Params[k]; ok {
			ps = append(ps, k+"="+v)
		}
	}
	return map[string]string{"kernel": id.Kernel, "driver_version": id.Driver.Version, "srcversion": id.Driver.SrcVersion,
		"parameters": strings.Join(ps, " "), "dkms_package": id.DKMSPackage, "bios": id.DMI.BIOS, "ec_firmware": id.DMI.EC}
}

// findRun finds RUN for log check/annotate: a run directory given as a path,
// else a run with that id below out.
func findRun(out, id string) string {
	if fi, err := os.Stat(id); err == nil && fi.IsDir() {
		return id
	}
	return findRunIn(out, id)
}

// findRunIn finds the run with this id below out only; log (start) uses it,
// so an id is never resolved against the working directory.
func findRunIn(out, id string) string {
	ents, _ := os.ReadDir(out)
	for _, e := range ents {
		if e.IsDir() && strings.HasSuffix(e.Name(), "-log-"+id) {
			return filepath.Join(out, e.Name())
		}
	}
	return ""
}

func cmdLog(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "check":
			return cmdLogCheck(args[1:])
		case "annotate":
			return cmdLogAnnotate(args[1:])
		}
	}
	fl := flag.NewFlagSet("log", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dur := fl.String("duration", "7d", "run length (7d, 36h, ...)")
	interval := fl.Duration("interval", 30*time.Second, "sample interval: 30s (F8b), 5s (T4/T5 raw data)")
	mailTo := fl.String("mail-to", "", "alert, daily and end mails to this address (without: the outbox beside the run and stdout)")
	mailCmd := fl.String("mail-cmd", "", "mail command, run without a shell (default: sendmail if present), e.g. \"msmtp\" or \"mail\"")
	runID := fl.String("run-id", "", "run id (default: start time); an existing run with this id is resumed")
	out := fl.String("out", defaultLogDir, "directory of the runs")
	if rc, ok := parseFlags(fl, args, nil); !ok {
		return rc
	}
	if fl.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: n5-ectest "+commands["log"].usage)
		return exitUsage
	}
	d, err := parseDays(*dur)
	if err != nil || d <= 0 {
		fmt.Fprintf(stderr, "log: --duration: %v\n", err)
		return exitUsage
	}
	if *interval < 5*time.Second || *interval > 60*time.Second {
		fmt.Fprintln(stderr, "log: --interval is 5s..60s (each sample makes the driver talk to the EC; K5 fires above 90 s)")
		return exitUsage
	}
	if *runID == "" {
		*runID = now().UTC().Format("20060102-1504")
	}
	if !validRunID(*runID) {
		fmt.Fprintln(stderr, "log: --run-id must be [A-Za-z0-9._-]{1,64}, not . or ..")
		return exitUsage
	}
	if !checkMailTo("log", *mailTo) {
		return exitUsage
	}
	id := collectIdentity()
	if ok, why := boards.ReadOnly(id.DMI); !ok {
		fmt.Fprintln(stderr, "log: "+why)
		return exitFail
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		fmt.Fprintf(stderr, "log: %v\n", err)
		return exitFail
	}
	dir := findRunIn(*out, *runID)
	if dir == "" {
		dir = filepath.Join(*out, bundle.DirName(now(), id.DMI.Board, id.DMI.BIOS, "log-"+*runID))
	}
	mail, mailNote := mailer(*mailCmd, *mailTo)
	red := newRedactor()
	deviations := baseDeviations()
	meta := map[string]any{"spec_version": spec.SpecVersion, "tool": toolName, "tool_version": version, "command": "log",
		"run_id": *runID, "interval_s": interval.Seconds(), "duration_s": d.Seconds(), "identity": id, "mail": mailNote,
		"spec_deviations": deviations, "provenance": provenance(id, "", deviations), "redaction": redactionRules}
	fmt.Fprintf(stdout, "n5-ectest log %s: %s, interval %s, duration %s; %s\n", *runID, dir, *interval, d, mailNote)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	err = logger.Run(ctx, logger.Config{RunID: *runID, Dir: dir, Duration: d, Interval: *interval, MailTo: *mailTo}, logger.Deps{
		Now: now, Sleep: sleepCtx, Fangov: logFangov, Journal: logJournal, Mail: mail, FreeBytes: logFree, Notify: sdNotify,
		Print: func(s string) { fmt.Fprintln(stdout, s) }, Redact: red, Meta: meta, Identity: logIdentity,
	})
	if errors.Is(err, logger.ErrFinished) {
		fmt.Fprintf(stdout, "log: %v: nothing to do (report: n5-ectest report %s)\n", err, dir)
		return exitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "log: %v\n", err)
		return exitFail
	}
	return exitOK
}

func cmdLogCheck(args []string) int {
	fl := flag.NewFlagSet("log check", flag.ContinueOnError)
	fl.SetOutput(stderr)
	out := fl.String("out", defaultLogDir, "directory of the runs")
	mailTo := fl.String("mail-to", "", "alert address")
	mailCmd := fl.String("mail-cmd", "", "mail command (default: sendmail if present)")
	failed := fl.String("unit-failed", "", "the log unit that failed (OnFailure=)")
	if rc, ok := parseFlags(fl, args, map[string]bool{"out": true, "mail-to": true, "mail-cmd": true, "unit-failed": true}); !ok {
		return rc
	}
	if fl.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: n5-ectest log check RUN [--out DIR] [--mail-to ADDR] [--unit-failed UNIT]")
		return exitUsage
	}
	if !checkMailTo("log check", *mailTo) {
		return exitUsage
	}
	dir := findRun(*out, fl.Arg(0))
	if dir == "" {
		fmt.Fprintf(stderr, "log check: no run %q in %s\n", fl.Arg(0), *out)
		return exitFail
	}
	mail, _ := mailer(*mailCmd, *mailTo)
	if mail == nil {
		mail = func(subject, body string) error {
			fmt.Fprintf(stdout, "%s\n%s\n", subject, body)
			return nil
		}
	}
	r := logger.Check(dir, now(), mail, *failed)
	fmt.Fprintln(stdout, r.Text)
	if r.Stale {
		return exitFail
	}
	return exitOK
}

func cmdLogAnnotate(args []string) int {
	use := func() int {
		fmt.Fprintln(stderr, "usage: n5-ectest log annotate RUN --event N[,M...] TEXT... | --down FROM TO TEXT...   (FROM/TO: RFC 3339)")
		return exitUsage
	}
	out := defaultLogDir
	if len(args) >= 2 && args[0] == "--out" {
		out, args = args[1], args[2:]
	}
	if len(args) < 3 {
		return use()
	}
	dir := findRun(out, args[0])
	if dir == "" {
		fmt.Fprintf(stderr, "log annotate: no run %q in %s\n", args[0], out)
		return exitFail
	}
	var err error
	switch args[1] {
	case "--event":
		var ids []int
		for _, s := range strings.Split(args[2], ",") {
			n, cerr := strconv.Atoi(strings.TrimPrefix(s, "#"))
			if cerr != nil {
				return use()
			}
			ids = append(ids, n)
		}
		err = logger.AnnotateEvents(dir, now(), ids, strings.Join(args[3:], " "))
	case "--down":
		if len(args) < 5 {
			return use()
		}
		from, e1 := time.Parse(time.RFC3339, args[2])
		to, e2 := time.Parse(time.RFC3339, args[3])
		if e1 != nil || e2 != nil {
			return use()
		}
		err = logger.AnnotateDown(dir, from, to, strings.Join(args[4:], " "))
	default:
		return use()
	}
	if err != nil {
		fmt.Fprintf(stderr, "log annotate: %v\n", err)
		return exitFail
	}
	fmt.Fprintf(stdout, "annotated in %s\n", filepath.Join(dir, logger.Annotations))
	// a finished run: the annotation is part of its record
	if _, err := os.Stat(filepath.Join(dir, bundle.Sums)); err == nil {
		if err := bundle.WriteSums(dir); err != nil {
			fmt.Fprintf(stderr, "log annotate: SHA256SUMS: %v\n", err)
			return exitFail
		}
		fmt.Fprintln(stdout, "SHA256SUMS of the finished run rewritten")
	}
	return exitOK
}
