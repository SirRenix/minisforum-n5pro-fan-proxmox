package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/boards"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/hwread"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/regulator"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/runner"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/session"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sim"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/writerlock"
)

func init() {
	register("map", command{usage: "map pwmN [--boot cold|warm] [--restore pwmN=auto|D] [--out DIR] [--i-am-at-the-console] | --dry-run",
		help: "T1 channel mapping: WRITES one channel, at the device only (about 11 min)", run: func(a []string) int { return cmdValidate("map", a) }})
	register("handback", command{usage: "handback pwmN [--boot cold|warm] [--restore pwmN=auto|D] [--out DIR] [--i-am-at-the-console] | --dry-run",
		help: "T2/T3 hand-back and settling: WRITES one channel, at the device only (about 2 h 10 min)", run: func(a []string) int { return cmdValidate("handback", a) }})
}

// confirmText is what the operator types before anything is written
// (TESTSPEC G5).
const confirmText = "VALIDATE START"

// Test hooks: stdin, the session and the clock of hardware runs.
var (
	stdin         io.Reader = os.Stdin
	detectSession           = func() session.Session {
		s := session.Detect(sysroot.Proc(), os.Getpid(), os.Getenv, session.StdinTerminal())
		if os.Getenv("TMUX") != "" {
			s = session.WithTmux(s, sysroot.Proc(), tmuxClients())
		}
		return s
	}
	sleepCtx = func(ctx context.Context, d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		}
	}
)

// tmuxClients returns the pids of the clients attached to the tmux server
// of $TMUX (nil when tmux cannot be asked).
func tmuxClients() []int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-clients", "-F", "#{client_pid}").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if p, err := strconv.Atoi(f); err == nil && p > 0 {
			pids = append(pids, p)
		}
	}
	return pids
}

// restoreFlag collects repeated --restore pwmN=auto|D.
type restoreFlag map[int]string

func (r restoreFlag) String() string { return fmt.Sprint(map[int]string(r)) }
func (r restoreFlag) Set(s string) error {
	ch, v, err := runner.ParseRestoreArg(s)
	if err != nil {
		return err
	}
	r[ch] = v
	return nil
}

// runOpts are the parsed arguments of map/handback.
type runOpts struct {
	test       string // "map" or "handback"
	ch         int
	boot       string
	out        string
	console    bool
	dry        bool
	restore    restoreFlag
	configPath string
	cfg        runner.Config
	args       []string // the original arguments (re-exec of the worker)
}

func parseRunOpts(test string, args []string) (runOpts, int) {
	o := runOpts{test: test, restore: restoreFlag{}, args: args}
	fs := flag.NewFlagSet(test, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.boot, "boot", "", "boot type of this boot as you know it: cold or warm (recorded; T0 needs it)")
	fs.StringVar(&o.out, "out", ".", "directory for the result bundle")
	fs.BoolVar(&o.console, "i-am-at-the-console", false, "you sit at the device (required in a remote session or without a local console in the process chain)")
	fs.BoolVar(&o.dry, "dry-run", false, "simulate against the built-in EC model; writes nothing; not a result under TESTSPEC")
	fs.Var(o.restore, "restore", "restore value pwmN=auto|D (D >= 100; pwm4 only 255), repeatable; recorded as a spec deviation")
	fs.StringVar(&o.configPath, "fangov-config", "", "n5-fangov configuration with the stop values (default /etc/n5-fangov/config.toml)")
	hold := fs.Duration("hold", spec.T1StepHoldS*time.Second, "time per step (--dry-run only)")
	sample := fs.Duration("sample", spec.SampleS*time.Second, "sample interval (--dry-run only)")
	observe := fs.Duration("observe", spec.ObserveS*time.Second, "log time after the auto command (--dry-run only)")
	window := fs.Duration("gate-window", spec.GateWindowS*time.Second, "G4 window (--dry-run only)")
	repeat := fs.Int("repeat", spec.RunsRequired, "T2/T3 repetitions (--dry-run only)")
	valued := map[string]bool{"boot": true, "out": true, "restore": true, "fangov-config": true, "hold": true, "sample": true, "observe": true, "gate-window": true, "repeat": true}
	if rc, ok := parseFlags(fs, args, valued); !ok {
		if rc == exitOK {
			// -h: the flags are printed; nothing runs
			return o, exitHelp
		}
		return o, rc
	}
	if !o.dry {
		var bad []string
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "hold", "sample", "observe", "gate-window", "repeat":
				bad = append(bad, "--"+f.Name)
			}
		})
		if len(bad) > 0 {
			fmt.Fprintf(stderr, "%s: %s only with --dry-run; a real run uses the TESTSPEC %s timing\n", test, strings.Join(bad, ", "), spec.SpecVersion)
			return o, exitUsage
		}
	}
	pos := fs.Args()
	if len(pos) != 1 && !(o.dry && len(pos) == 0) {
		fmt.Fprintf(stderr, "usage: n5-ectest %s\n", commands[test].usage)
		return o, exitUsage
	}
	chArg := "pwm3"
	if len(pos) == 1 {
		chArg = pos[0]
	}
	ch, err := strconv.Atoi(strings.TrimPrefix(chArg, "pwm"))
	if err != nil || ch < 1 || ch > 4 || !strings.HasPrefix(chArg, "pwm") {
		fmt.Fprintf(stderr, "%s: channel is pwm1..pwm4, not %q\n", test, chArg)
		return o, exitUsage
	}
	o.ch = ch
	if o.boot != "" && o.boot != "cold" && o.boot != "warm" {
		fmt.Fprintf(stderr, "%s: --boot is cold or warm\n", test)
		return o, exitUsage
	}
	if *sample <= 0 || *hold < *sample || *observe < *sample || *window < 0 || *repeat < 1 {
		fmt.Fprintf(stderr, "%s: --sample > 0, --hold and --observe >= --sample, --repeat >= 1\n", test)
		return o, exitUsage
	}
	if o.configPath == "" {
		o.configPath = filepath.Join(sysroot.Etc(), "n5-fangov", "config.toml")
	}
	o.cfg = runner.SpecConfig(ch, runner.TachOf(ch), []int{1, 2, 3})
	if o.dry {
		o.cfg.Hold, o.cfg.Sample, o.cfg.Observe, o.cfg.Window, o.cfg.Repeat = *hold, *sample, *observe, *window, *repeat
	}
	return o, exitOK
}

// writing reports whether the command writes: map always, handback except
// on pwm4 (T2/T3 N/A there, nothing written; TESTSPEC §6).
func (o runOpts) writing() bool {
	return o.test == "map" || o.ch != spec.PCIeChannel
}

func cmdValidate(test string, args []string) int {
	o, rc := parseRunOpts(test, args)
	if rc == exitHelp {
		return exitOK
	}
	if rc != exitOK {
		return rc
	}
	if o.dry {
		return runValidation(o, nil, "")
	}
	return guardian(test, args)
}

// ---- the run ----------------------------------------------------------

// validation is one map/handback run.
type validation struct {
	o       runOpts
	id      identity.Identity
	env     runner.Env
	checker *regulator.Checker
	sess    session.Session
	state   *stateFile
	red     *identity.Redactor
	rv      runner.RestoreValues
}

// runValidation runs map/handback. lock is the writer lock handed down by
// the guardian (nil for --dry-run); statePath is the guardian state file.
func runValidation(o runOpts, lock *writerlock.Lock, statePath string) (rc int) {
	// a closed stdout (`... | tee`, tee killed by the same Ctrl-C) must not
	// kill the process by SIGPIPE before the channels are restored, and
	// Ctrl-Z must not stop it with a channel manual and no limit check
	ignoreTerminalStops()
	v := &validation{o: o, red: newRedactor()}
	if !o.dry {
		if !requireRoot(o.test) {
			return exitFail
		}
		if run, st := fangovRunning(); run {
			fmt.Fprintf(stderr, "%s: the n5-fangov daemon is %s. Stop it first: systemctl stop n5-fangov\n", o.test, st)
			fmt.Fprintln(stderr, "          (TESTSPEC G1: no other pwm writer; there is no override)")
			return exitFail
		}
	}
	// the bundle directory must be creatable before any gate, the
	// confirmation and the first write
	if err := prepareOutDir(o.out); err != nil {
		fmt.Fprintf(stderr, "%s: %v (nothing written)\n", o.test, err)
		return exitFail
	}
	v.id = collectIdentity()
	v.rv = runner.ResolveRestore(o.restore, o.configPath, []int{o.ch})
	if !o.dry {
		v.sess = detectSession()
		if o.writing() {
			if v.sess.Stdin == "" {
				fmt.Fprintf(stderr, "%s: standard input is not a terminal. The confirmation must be typed at the device (TESTSPEC G5); piped input is refused.\n", o.test)
				return exitFail
			}
			if !o.console && len(v.sess.Remote) > 0 {
				fmt.Fprintf(stderr, "%s: this session is remote (%s). Writes run only with the operator at the device (TESTSPEC G5).\n", o.test, strings.Join(v.sess.Remote, "; "))
				fmt.Fprintln(stderr, "          Run it on the console, or add --i-am-at-the-console if you sit at the device.")
				return exitFail
			}
			if !o.console && !v.sess.Console {
				fmt.Fprintf(stderr, "%s: no local console (VT or serial login) in this process chain. Writes run only with the operator at the device (TESTSPEC G5).\n", o.test)
				fmt.Fprintln(stderr, "          Run it on the console (tty1, KVM), or add --i-am-at-the-console if you sit at the device.")
				return exitFail
			}
		}
	}
	return v.run(lock, statePath)
}

func (v *validation) run(lock *writerlock.Lock, statePath string) (rc int) {
	o := v.o
	id := v.id
	mode := "hardware"
	var gates []spec.Gate
	boardReason, pwmReason := "", ""
	var dev pwmw.Device
	if o.dry {
		mode = "dry-run (simulated EC; nothing is written; not a result under TESTSPEC)"
		s := sim.New(time.Now().UTC().Truncate(time.Second))
		v.env = s
		gates = append(gates, spec.Gate{ID: "G1", Condition: g1Condition, Result: "SKIPPED", Detail: "dry-run"},
			spec.Gate{ID: "G2", Condition: "no scrub/resilver/backup", Result: "SKIPPED", Detail: "dry-run"})
	} else {
		boardReason, pwmReason = boards.WriteGate(id, o.ch)
		var derr error
		dev, _, derr = pwmw.Resolve(sysroot.Sys())
		if derr != nil && pwmReason == "" {
			pwmReason = derr.Error()
		}
		dir := func() string { return hwread.DriverHwmon(sysroot.Sys()) }
		expect := regulator.NewExpectation(dir)
		w := &pwmw.Writer{SysRoot: sysroot.Sys(), Want: dev, Before: expect.Expect, After: expect.After}
		v.env = &liveEnv{w: w, restore: v.rv.Values}
		self := map[int]bool{os.Getpid(): true, os.Getppid(): true}
		v.checker = &regulator.Checker{ProcRoot: sysroot.Proc(), Self: self, Lock: lock, Expect: expect}
		gates = append(gates, v.gateG1(), runner.G2(sysroot.Proc()))
		if boardReason == "" {
			gates = append(gates, spec.Gate{ID: "G7", Condition: g7Condition, Result: "PASS"})
		} else {
			gates = append(gates, spec.Gate{ID: "G7", Condition: g7Condition, Result: "FAIL", Detail: boardReason})
		}
		if pwmReason == "" {
			gates = append(gates, spec.Gate{ID: "pwm", Condition: fmt.Sprintf("pwm%d exposed by the driver", o.ch), Result: "PASS", Detail: dev.String()})
		} else {
			gates = append(gates, spec.Gate{ID: "pwm", Condition: fmt.Sprintf("pwm%d exposed by the driver", o.ch), Result: "FAIL", Detail: pwmReason})
		}
	}
	cfg := o.cfg
	if v.checker != nil {
		cfg.Guard = func() string { return strings.Join(v.checker.Check(), "; ") }
	}
	r := runner.NewRunner(v.env, cfg)
	r.Tag = fmt.Sprintf("%s %s, driver %s", toolName, version, orDash(id.Driver.Version))
	started := r.Start()
	dcrOK, dcrErr := r.DCRAvailable()
	blocked := ""
	for _, g := range gates {
		if g.Result == "FAIL" {
			blocked = g.ID + " failed: " + g.Detail
			if g.ID == "G7" {
				blocked = "G7 (board/vendor/driver): " + g.Detail
			}
			if g.ID == "pwm" {
				blocked = "pwm not exposed: " + g.Detail
			}
			break
		}
	}
	willWrite := o.writing() && blocked == "" && dcrOK
	fmt.Fprintf(stdout, "=== n5-ectest %s  pwm%d  TESTSPEC %s  %s  %s ===\n", o.test, o.ch, spec.SpecVersion, mode, started.Format(time.RFC3339))
	fmt.Fprintf(stdout, "%s\n", v.red.String(fmt.Sprintf("machine: %s, driver %s %s, hardware status %s", id.DMI.Key(), n5driver.ModuleName, orDash(id.Driver.Version), id.HWStatus.Status)))
	// TESTSPEC §3: G5 (the confirmation) is measured for every writing
	// test; a failed gate is reported in the bundle, nothing is written
	if !o.dry && o.writing() {
		fmt.Fprintln(stdout)
		if willWrite {
			fmt.Fprintln(stdout, "This command WRITES duty values to the EC (one channel, duty >= 100 only).")
			fmt.Fprintln(stdout, "The channel is put back into its restore value at the end, on abort and on a signal.")
			fmt.Fprintln(stdout, "Limits (abort): CPU Tctl 85 C, NVMe 70 C, HDD 50 C, a tach at 0 rpm, another pwm writer.")
			if o.test == "map" {
				fmt.Fprintf(stdout, "Duration: about %s (gate window and T1).\n", cfg.Window+time.Duration(len(spec.T1Steps)+1)*cfg.Hold)
			} else {
				fmt.Fprintf(stdout, "Duration: about %s (gate window, A, %d runs of T2/T3).\n", cfg.Window+cfg.Observe+time.Duration(cfg.Repeat)*2*(cfg.Hold+cfg.HandbackObserve()), cfg.Repeat)
			}
			fmt.Fprintf(stdout, "Restore value pwm%d: %s (%s)\n", o.ch, v.rv.Values[o.ch], v.rv.Sources[o.ch])
			fmt.Fprintf(stdout, "Run it inside tmux; if both n5-ectest processes are killed, run: n5-ectest restore\n")
		} else {
			// TESTSPEC §3: the gates are still measured and recorded
			fmt.Fprintf(stdout, "Nothing will be written: %s.\n", orText(blocked, "duty registers not readable ("+errText(dcrErr)+")"))
			if dcrOK {
				fmt.Fprintf(stdout, "The run records T0 and the gates, with the %s gate window (no writes), in a bundle.\n", cfg.Window)
			} else {
				fmt.Fprintln(stdout, "The run records T0 and the gates in a bundle.")
			}
		}
		fmt.Fprintf(stdout, "Type exactly  %s  to continue: ", confirmText)
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.TrimSpace(line) != confirmText {
			fmt.Fprintln(stdout, "\nAborted, nothing written.")
			return exitFail
		}
		r.Event("operator confirmation typed (stdin %s, console flag %v, local console %v, remote: %s, tmux: %s)", v.sess.Stdin, o.console, v.sess.Console, joinOr(v.sess.Remote, "; ", "-"), orDash(v.sess.Tmux))
	}
	// bundle: created after the confirmation (a refusal leaves none)
	test := fmt.Sprintf("validate-pwm%d", o.ch)
	dir := filepath.Join(o.out, bundle.DirName(started, id.DMI.Board, id.DMI.BIOS, test))
	if o.dry {
		dir += "-" + o.test + "-dryrun" // not a result: the name tells map and handback apart
	}
	bw, err := bundle.Create(dir, v.red)
	if err != nil {
		fmt.Fprintf(stderr, "%s: bundle: %v (nothing written)\n", o.test, err)
		return exitFail
	}
	for _, e := range r.Events {
		bw.Event(e)
	}
	r.OnEvent, r.OnSample = bw.Event, bw.Sample
	r.Event("start %s %s pwm%d: %s; mode %s", toolName, o.test, o.ch, toolLine(id), mode)
	// provisional meta.json before anything is written: a killed run keeps
	// its provenance, gates so far and restore values (replaced at the end)
	deviations := append(append(baseDeviations(), v.rv.Deviations...), runDeviations(o)...)
	sessMeta := map[string]any{"stdin": v.sess.Stdin, "local_console": v.sess.Console, "remote": nonNil(v.sess.Remote), "tmux": v.sess.Tmux, "console_flag": o.console}
	prov := map[string]any{
		"spec_version": spec.SpecVersion, "tool": toolName, "tool_version": version, "command": o.test,
		"mode": mode, "channel": o.ch, "started": started.UTC().Format(time.RFC3339), "boot_type": o.boot,
		"identity": id, "gates": gates, "restore_values": v.rv.Values, "restore_value_sources": v.rv.Sources,
		"session": sessMeta, "spec_deviations": deviations, "provenance": provenance(id, o.boot, deviations), "provisional": true,
		"redaction": redactionRules, "settle_s": spec.SettleS(o.ch),
	}
	if err := bw.WriteJSON(bundle.Meta, prov); err != nil {
		_ = bw.Finish()
		fmt.Fprintf(stderr, "%s: bundle: %v (nothing written)\n", o.test, err)
		return exitFail
	}
	if !o.dry && willWrite {
		// absolute: `n5-ectest restore` may run from another directory
		// (default --out is ".")
		absDir, aerr := filepath.Abs(dir)
		if aerr != nil {
			_ = bw.Finish()
			fmt.Fprintf(stderr, "%s: bundle path: %v (nothing written)\n", o.test, aerr)
			return exitFail
		}
		st := &stateFile{Tool: toolName, Version: version, PID: os.Getpid(), Started: started.UTC().Format(time.RFC3339), State: stateActive,
			Device: dev, Channels: map[string]string{strconv.Itoa(o.ch): v.rv.Values[o.ch]}, Bundle: absDir, TestRoot: sysroot.Testing()}
		v.state = st
		r.BeforeWrite = func() error {
			if statePath == "" {
				return errors.New("no guardian state file")
			}
			if err := st.write(statePath); err != nil {
				return err
			}
			r.Event("guardian state written: %s (restore pwm%d=%s, device %s)", statePath, o.ch, v.rv.Values[o.ch], dev)
			return nil
		}
		// TESTSPEC 0.4 §4 (#13): the channel of a tripped temperature limit
		// goes to 255; a guardian that restores after a killed worker must
		// not put it back to its restore value
		r.BeforeHot = func(ch int) error {
			if statePath == "" {
				return errors.New("no guardian state file")
			}
			st.Channels[strconv.Itoa(ch)] = "255"
			if err := st.write(statePath); err != nil {
				return err
			}
			r.Event("guardian state written: %s (pwm%d=255 after a temperature abort)", statePath, ch)
			return nil
		}
	}

	// Ctrl-\ (SIGQUIT) ends the run like Ctrl-C, with the restore
	ctx, cancel := signal.NotifyContext(context.Background(), stopSignals...)
	defer cancel()
	finished := false
	defer func() {
		p := recover()
		r.Close() // every exit path: channel back to its restore value, before any output
		if p != nil {
			r.Event("PANIC %v", p)
			fmt.Fprintf(stdout, "!! panic: %v\n", p)
			rc = exitFail
		}
		if !finished {
			_ = bw.Finish()
		}
		if v.state != nil && statePath != "" && len(r.Unrestored()) == 0 {
			v.state.State = stateRestored
			_ = v.state.write(statePath)
		}
	}()
	closeOnAbort := func() bool {
		if r.Abort != "" || ctx.Err() != nil {
			r.Close()
			return true
		}
		return false
	}

	_, firstAbort := r.Sample(spec.PhaseBoot, -1)
	if firstAbort != "" {
		r.Stop(firstAbort)
	}
	g3 := spec.Gate{ID: "G3", Condition: "load average (1 min) < 1.0", Result: "SKIPPED"}
	g4 := spec.Gate{ID: "G4", Condition: runner.G4Condition, Result: "SKIPPED"}
	switch {
	case !o.writing():
		g3.Detail, g4.Detail = "no writing test (pwm4: T2/T3 N/A)", "no writing test (pwm4: T2/T3 N/A)"
	case !dcrOK:
		g3.Detail, g4.Detail = "duty registers not readable: nothing is written", "duty registers not readable: nothing is written"
	case boardReason != "":
		// TESTSPEC 0.4 §3 (#12): without G7 nothing is written, the window
		// is not spent
		g3.Detail, g4.Detail = "gate G7 failed: nothing is written", "gate G7 failed: nothing is written"
	default:
		fmt.Fprintf(stdout, "gate G3/G4: sampling %s without writes ...\n", cfg.Window)
		g3, g4 = r.Gates(ctx)
	}
	g5 := spec.Gate{ID: "G5", Condition: "operator at the device", Result: "PASS", Detail: "confirmation typed (stdin " + v.sess.Stdin + ")"}
	switch {
	case o.dry:
		g5.Result, g5.Detail = "SKIPPED", "dry-run, nothing written"
	case !o.writing():
		g5.Result, g5.Detail = "SKIPPED", "no writing test (pwm4: T2/T3 N/A)"
	case len(v.sess.Remote) > 0:
		g5.Detail += ", remote session (" + strings.Join(v.sess.Remote, "; ") + ") with --i-am-at-the-console"
	case !v.sess.Console:
		g5.Detail += ", no local console in the process chain, --i-am-at-the-console given"
	default:
		g5.Detail += ", local console"
	}
	if v.sess.Tmux != "" && g5.Result == "PASS" {
		g5.Detail += ", inside tmux (" + v.sess.Tmux + ")"
	}
	// G6 before the first write of the run (TESTSPEC 0.4 §3, #9)
	g6, ecTables := spec.Gate{ID: "G6", Condition: g6Condition, Result: "SKIPPED", Detail: "dry-run (simulated EC; curve tables not read)"}, map[string]string{}
	if !o.dry {
		g6, ecTables = gateG6(dcrOK, dcrErr)
	}
	gates = append(gates, g3, g4, g5, g6)
	for _, g := range gates {
		r.Event("gate %s %s: %s", g.ID, g.Result, g.Detail)
		fmt.Fprintf(stdout, "%s\n", v.red.String(fmt.Sprintf("gate %-5s %-8s %s", g.ID, g.Result, g.Detail)))
	}
	if blocked == "" {
		for _, g := range gates {
			if g.Result == "FAIL" && g.ID != "G6" {
				blocked = g.ID + " failed: " + g.Detail
				break
			}
		}
	}
	writeSkipped := o.writing() && (blocked != "" || !dcrOK)

	results := []spec.Result{v.t0()}
	notRun := func(t, reason string) spec.Result {
		if o.ch == spec.PCIeChannel && (t == "T2" || t == "T3") {
			return spec.NotApplicableResult(t, o.ch, spec.ReasonNoPCIeAuto)
		}
		return spec.NotImplemented(t, o.ch, reason)
	}
	tests := []string{"T1"}
	if o.test == "handback" {
		tests = []string{"T2", "T3"}
	}
	switch {
	case blocked != "" && o.writing():
		for _, t := range tests {
			results = append(results, notRun(t, "not run: gate "+blocked))
		}
	case o.test == "map":
		if !closeOnAbort() {
			fmt.Fprintln(stdout, "T1 mapping ...")
			results = append(results, r.RunT1(ctx))
		} else {
			results = append(results, spec.NotImplemented("T1", o.ch, "not run: "+orText(r.Abort, "cancelled (signal)")))
		}
	default:
		if !closeOnAbort() {
			if o.writing() {
				fmt.Fprintln(stdout, "T2/T3 hand-back and settling ...")
			}
			t2, t3 := r.RunHandback(ctx)
			results = append(results, t2, t3)
		} else {
			results = append(results, notRun("T2", "not run: "+orText(r.Abort, "cancelled (signal)")), notRun("T3", "not run: "+orText(r.Abort, "cancelled (signal)")))
		}
	}
	closeOnAbort()
	if o.test == "handback" {
		// T4/T5 are not evaluated by this tool yet (spec_deviations)
		results = append(results, spec.T4T5Result("T4", o.ch, r.Samples), spec.T4T5Result("T5", o.ch, r.Samples))
	}
	r.Close()
	unrestored := r.Unrestored()
	finishedAt := v.env.Now()
	limits := r.LimitSensors
	if limits == nil {
		limits = map[string]bool{}
	}
	// restore values as used: after a temperature abort the channel of that
	// sensor is at 255, with the reason (TESTSPEC 0.4 §4, #13)
	restoreValues, restoreSources := map[int]string{}, map[int]string{}
	for ch, val := range v.rv.Values {
		restoreValues[ch], restoreSources[ch] = val, v.rv.Sources[ch]
	}
	for ch, why := range r.RestoreOverrides {
		restoreValues[ch], restoreSources[ch] = "255", why
	}
	meta := map[string]any{
		"spec_version": spec.SpecVersion, "tool": toolName, "tool_version": version, "command": o.test,
		"mode": mode, "channel": o.ch, "started": started.UTC().Format(time.RFC3339), "finished": finishedAt.UTC().Format(time.RFC3339),
		"boot_type": o.boot, "identity": id, "gates": gates, "aborted": r.Abort,
		"timing": map[string]any{"hold_s": cfg.Hold.Seconds(), "sample_s": cfg.Sample.Seconds(), "observe_s": cfg.Observe.Seconds(),
			"handback_observe_s": cfg.HandbackObserve().Seconds(), "gate_window_s": cfg.Window.Seconds(), "repeat": cfg.Repeat,
			"due_tolerance_s": spec.DueToleranceS},
		"settle_s":              spec.SettleS(o.ch),
		"ec_ram_tables":         ecTables,
		"redaction":             redactionRules,
		"limit_sensors":         limits,
		"restore_values":        restoreValues,
		"restore_value_sources": restoreSources,
		"restore_failed":        nonNil(unrestored),
		"restore_warnings":      nonNil(append(append([]string{}, v.rv.Warnings...), r.RestoreWarnings...)),
		"session":               sessMeta,
		"spec_deviations":       deviations,
		"provenance":            provenance(id, o.boot, deviations),
	}
	verdict := map[string]any{"spec_version": spec.SpecVersion, "channel": o.ch, "command": o.test, "tests": results}
	r.Event("finish: %d sample(s), aborted %q", len(r.Samples), r.Abort)
	ferr := bw.WriteJSON(bundle.Meta, meta)
	if ferr == nil {
		ferr = bw.WriteJSON(bundle.Verdict, verdict)
	}
	if ferr == nil && v.state != nil {
		ferr = bw.WriteJSON("restore.json", v.state)
	}
	if err := bw.Finish(); ferr == nil {
		ferr = err
	}
	finished = true
	if ferr != nil {
		fmt.Fprintf(stderr, "%s: bundle: %v\n", o.test, ferr)
		rc = exitFail
	} else {
		fmt.Fprintf(stdout, "\nbundle: %s\n", dir)
	}
	fmt.Fprintln(stdout)
	for _, res := range results {
		line := ""
		if len(res.Reasons) > 0 {
			line = res.Reasons[0]
		}
		fmt.Fprintf(stdout, "%s\n", v.red.String(fmt.Sprintf("%-3s %-12s %s", res.ID, res.Verdict, line)))
	}
	for _, w := range r.RestoreWarnings {
		fmt.Fprintf(stdout, "\nWARNING: restore %s\n", w)
	}
	if len(unrestored) > 0 {
		for _, u := range unrestored {
			msg := fmt.Sprintf("!! RESTORE FAILED %s - run: n5-ectest restore", u)
			fmt.Fprintln(stdout, msg)
			fmt.Fprintln(stderr, o.test+": "+msg)
		}
		rc = exitFail
	}
	if r.Abort != "" {
		fmt.Fprintf(stdout, "\nABORTED: %s\n", r.Abort)
		return exitFail
	}
	if writeSkipped {
		fmt.Fprintf(stdout, "\nWriting tests not run: %s\n", orText(blocked, "duty registers not readable ("+errText(dcrErr)+")"))
		return exitFail
	}
	return rc
}

// curveTables are the EC RAM ranges of the curve tables (TESTSPEC 0.4 §3
// G6, §6).
var curveTables = [][2]int{{0x10, 0x27}, {0x30, 0x38}, {0x70, 0x81}}

const g6Condition = "register snapshot before the first write: duty registers and EC RAM curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81"

// gateG6 is TESTSPEC 0.4 §3 G6 (#9): the duty registers are recorded in
// the first sample; with the driver's debugfs ecram (0.2.1+sr2,
// debug_regs=1) the tool reads the curve tables itself: PASS when read,
// FAIL when ecram is present but the read fails or the duty registers are
// not readable; without ecram PARTIAL (the tables come from a mode-A
// snapshot before the module load, named in the run record). Never
// blocking. tables is the hex of each range when read (meta.json
// ec_ram_tables; no C1 path before the byte-compare of §9 #9).
func gateG6(dcrOK bool, dcrErr error) (spec.Gate, map[string]string) {
	g := spec.Gate{ID: "G6", Condition: g6Condition}
	tables := map[string]string{}
	if !dcrOK {
		g.Result, g.Detail = "FAIL", "duty registers not readable: "+errText(dcrErr)
		return g, tables
	}
	p := n5driver.ECRAMPath(sysroot.Sys())
	if !ecramPresent() {
		g.Result = "PARTIAL"
		g.Detail = "duty registers recorded (first sample); no " + p + " (driver 0.2.1+sr2 with debug_regs=1): the curve tables come from a mode-A snapshot (tools/f8a/f8a-snapshot.sh) taken after boot and before the module load, named in the run record; whether something wrote earlier in this boot is not visible"
		return g, tables
	}
	s, err := sysroot.ReadTimeout(p, sysroot.DefaultTimeout)
	var e n5driver.ECRAM
	if err == nil {
		e, err = n5driver.ParseECRAM(s)
	}
	if err != nil {
		g.Result, g.Detail = "FAIL", "duty registers recorded (first sample); curve tables not read through ecram: "+err.Error()
		return g, tables
	}
	for _, r := range curveTables {
		tables[fmt.Sprintf("0x%02x-0x%02x", r[0], r[1])] = e.Hex(r[0], r[1])
	}
	g.Result = "PASS"
	g.Detail = "duty registers (first sample) and curve tables (driver ecram, meta.json ec_ram_tables) recorded before the first write of this run; whether something wrote earlier in this boot is not visible"
	return g, tables
}

const g7Condition = "board, vendor and driver state allow writes (TESTSPEC 0.4 G7): N5 PRO/F8NAA in the write table, measured sys_vendor, driver +sr1, force=N, dcr readable, not in a container"

const g1Condition = "no other pwm writer: units and processes n5-fangov, n5-fand, fancontrol, fan2go, coolercontrold, thinkfan; processes f8a-pcie.sh, f8a-stress.sh; no pwm file held open; writer lock held and in place; pwm cache unchanged by others"

// gateG1 checks for other pwm writers before the run (design §6).
func (v *validation) gateG1() spec.Gate {
	g := spec.Gate{ID: "G1", Condition: g1Condition}
	_, note := regulator.Units()
	if hits := v.checker.Check(); len(hits) > 0 {
		g.Result, g.Detail = "FAIL", strings.Join(hits, "; ")
	} else {
		g.Result, g.Detail = "PASS", "none active; checked again at every sample, writer lock held"
	}
	if note != "" {
		g.Detail += " (" + note + ")"
	}
	return g
}

// t0 checks the identity fields of TESTSPEC §2.
func (v *validation) t0() spec.Result {
	return spec.T0(spec.T0Values(v.id, version, v.o.boot), v.o.boot)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func errText(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

// ---- hardware environment ---------------------------------------------

// liveEnv is runner.Env on the N5 Pro through hwmon sysfs; every write
// goes through pwmw.
type liveEnv struct {
	w       *pwmw.Writer
	restore map[int]string
}

func (e *liveEnv) Now() time.Time                                  { return now() }
func (e *liveEnv) Sleep(ctx context.Context, d time.Duration) bool { return sleepCtx(ctx, d) }
func (e *liveEnv) EnterManual(ch int) error                        { return e.w.EnterManual(ch) }
func (e *liveEnv) WriteDuty(ch, duty int) error                    { return e.w.SetDuty(ch, duty) }
func (e *liveEnv) Auto(ch int) error                               { return e.w.Auto(ch) }

// Restore writes the channel's restore value; a fixed duty whose register
// reads back differently is runner.ErrRestoreUnverified.
func (e *liveEnv) Restore(ch int) error {
	val := e.restore[ch]
	if val == "" {
		val = "auto"
	}
	if ch == spec.PCIeChannel {
		// no EC automatic on DCR5: always 255 (decision 3, 2026-10-16)
		val = runner.PCIeRestore
	}
	t0 := now()
	if err := e.w.RestoreTo(ch, val); err != nil {
		return err
	}
	if val == "auto" {
		return nil
	}
	want, _ := strconv.Atoi(val)
	// the EC takes the write over with a delay (PCIe ~0.2 s, HDD ramp
	// DCR3 then DCR4 up to ~4.8 s; MW1 Z1/Z2): the read-back is compared
	// after the settle time of the channel (TESTSPEC 0.4 §4, §2.1), not
	// right after the write. Background context: the restore runs on the
	// signal path too and must not be cut short by it.
	regs := spec.JudgedRegs(ch)
	st := runner.SettleTime(ch)
	s := runner.WaitDCR(context.Background(), now, sleepCtx, e.DCR, t0, regs, want, st, st)
	if s.Err != nil {
		return nil
	}
	if !s.Reached {
		var bad []string
		for _, n := range regs {
			if s.DCR.Reg(n) != want {
				bad = append(bad, fmt.Sprintf("DCR%d reads %d", n, s.DCR.Reg(n)))
			}
		}
		return fmt.Errorf("%w: pwm%d=%d written, %s after %.1f s", runner.ErrRestoreUnverified, ch, want, strings.Join(bad, ", "), s.After.Seconds())
	}
	return nil
}

func (e *liveEnv) DCR() (n5driver.DCR, error) {
	s, err := sysroot.ReadTimeout(n5driver.DCRPath(sysroot.Sys()), sysroot.DefaultTimeout)
	if err != nil {
		return n5driver.DCR{}, err
	}
	return n5driver.ParseDCR(s)
}

func (e *liveEnv) Read() runner.Readings {
	h := hwread.Read(sysroot.Sys(), sysroot.Proc())
	rd := runner.Readings{Fan: map[int]int{}, Tctl: h.Tctl, NVMeMax: h.NVMeMax, HDDMax: h.HDDMax,
		ECCPU: h.EC["cpu"], ECSys: h.EC["system"], ECBoard: h.EC["board"], ECAmb: h.EC["ambient"], Load1: h.Load1}
	for f := 1; f <= 3; f++ {
		rd.Fan[f] = h.Fan[f]
	}
	return rd
}
