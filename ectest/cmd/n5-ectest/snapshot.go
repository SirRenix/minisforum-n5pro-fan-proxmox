package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/boards"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/hwread"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/regulator"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/runner"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/snapshot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

func init() {
	register("snapshot", command{usage: "snapshot LABEL [--mode B|A] [--boot cold|warm] [--out DIR]", help: "fans, temperatures, pwm cache, DCR, modules (read-only, driver loaded)", run: cmdSnapshot})
	register("diff", command{usage: "diff A B   (snapshot files or bundle dirs; exit 0 identical, 1 CHANGED, 2 incomplete input)", help: "compare two snapshots: EC curve tables and I2EC (TESTSPEC C1)", run: cmdDiff})
}

var labelRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// cmdSnapshot writes a read-only state snapshot bundle (T6, G6(b), C1
// input). Mode B (default) needs the driver loaded and touches no I/O port.
func cmdSnapshot(args []string) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "B", "B: driver loaded (hwmon, dcr); A: module unloaded, raw EC reads (not built yet)")
	boot := fs.String("boot", "", "boot type of this boot: cold or warm (recorded)")
	out := fs.String("out", ".", "directory for the bundle")
	if rc, ok := parseFlags(fs, args, map[string]bool{"mode": true, "boot": true, "out": true}); !ok {
		return rc
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: n5-ectest "+commands["snapshot"].usage)
		return exitUsage
	}
	label := fs.Arg(0)
	if !labelRe.MatchString(label) {
		fmt.Fprintln(stderr, "snapshot: label must be [A-Za-z0-9._-]+")
		return exitUsage
	}
	if *boot != "" && *boot != "cold" && *boot != "warm" {
		fmt.Fprintln(stderr, "snapshot: --boot is cold or warm")
		return exitUsage
	}
	switch *mode {
	case "B":
	case "A":
		fmt.Fprintln(stderr, "snapshot: mode A (raw EC reads through /dev/port, module unloaded) is not built yet (design §3: after maintenance window 1).")
		fmt.Fprintln(stderr, "          Use tools/f8a/f8a-snapshot.sh LABEL for mode A; n5-ectest diff reads its files.")
		return exitFail
	default:
		fmt.Fprintln(stderr, "snapshot: --mode is B or A")
		return exitUsage
	}
	id := collectIdentity()
	if ok, why := boards.ReadOnly(id.DMI); !ok {
		fmt.Fprintln(stderr, "snapshot: "+why)
		return exitFail
	}
	if !id.Driver.Loaded && hwread.DriverHwmon(sysroot.Sys()) == "" {
		fmt.Fprintf(stderr, "snapshot: %s is not loaded: mode B needs it. Mode A (module unloaded) is not built yet; use tools/f8a/f8a-snapshot.sh\n", n5driver.ModuleName)
		return exitFail
	}
	if err := prepareOutDir(*out); err != nil {
		fmt.Fprintf(stderr, "snapshot: %v\n", err)
		return exitFail
	}
	started := now()
	h := snapshot.Header{Tool: toolName, Version: version + " (commit " + commitID() + ")", SpecVersion: spec.SpecVersion, Label: label, Mode: "B",
		Boot: *boot, DateUTC: started.UTC().Format(time.RFC3339), FangovState: regulator.FangovState()}
	m := snapshot.TakeModeB(h, id, sysroot.Sys(), sysroot.Proc(), sysroot.Etc())
	red := newRedactor()
	dir := filepath.Join(*out, bundle.DirName(started, id.DMI.Board, id.DMI.BIOS, "snapshot-"+label))
	bw, err := bundle.Create(dir, red)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: %v\n", err)
		return exitFail
	}
	bw.Event(runner.FormatEvent(started, 0, "snapshot "+label+" mode B by "+toolLine(id)))
	s := spec.NewSample(started, 0, "snapshot", 0, -1)
	r := m.Reading
	for n := 1; n <= 3; n++ {
		s.Fan[n-1] = r.Fan[n]
	}
	s.ECCPU, s.ECSys, s.ECBoard, s.ECAmb = r.EC["cpu"], r.EC["system"], r.EC["board"], r.EC["ambient"]
	s.HDDMax, s.NVMeMax, s.Tctl, s.Load1 = r.HDDMax, r.NVMeMax, r.Tctl, r.Load1
	if m.DCR.Dump != "" {
		if d, err := n5driver.ParseDCR(m.DCR.Dump); err == nil {
			for n := 1; n <= 5; n++ {
				s.DCR[n-1] = d.Reg(n)
			}
		}
	}
	bw.Sample(s)
	deviations := baseDeviations()
	t6 := spec.Result{ID: "T6", Verdict: spec.Reported,
		Reasons: []string{fmt.Sprintf("mode-B snapshot %q; uptime %.0f s, boot type %q; writes earlier in this boot are not visible", label, id.UptimeS, *boot)},
		Values:  map[string]any{"dcr": m.DCR, "pwm_cache": map[string]any{"pwm": r.PWM, "pwm_enable": r.Enable}, "fans": r.Fan, "ec": r.EC}}
	meta := map[string]any{"spec_version": spec.SpecVersion, "tool": toolName, "tool_version": version, "command": "snapshot",
		"label": label, "mode": "B", "boot_type": *boot, "started": started.UTC().Format(time.RFC3339), "identity": id,
		"incomplete": nonNil(m.Incomplete), "spec_deviations": deviations, "provenance": provenance(id, *boot, deviations), "redaction": redactionRules}
	werr := bw.WriteFile("snapshot.txt", []byte(m.Text))
	if werr == nil {
		werr = bw.WriteJSON(bundle.Meta, meta)
	}
	if werr == nil {
		werr = bw.WriteJSON(bundle.Verdict, map[string]any{"spec_version": spec.SpecVersion, "command": "snapshot", "tests": []spec.Result{t6}})
	}
	if err := bw.Finish(); werr == nil {
		werr = err
	}
	if werr != nil {
		fmt.Fprintf(stderr, "snapshot: %v\n", werr)
		return exitFail
	}
	fmt.Fprint(stdout, red.String(m.Text))
	fmt.Fprintf(stdout, "\nbundle: %s\n", dir)
	if len(m.Incomplete) > 0 {
		return exitIncomplete
	}
	return exitOK
}

// cmdDiff compares two snapshots (C1): exit 0 identical, 1 CHANGED, 2
// incomplete input, 64 usage.
func cmdDiff(args []string) int {
	for _, a := range args {
		switch {
		case a == "-h" || a == "--help" || a == "-help":
			fmt.Fprintln(stderr, "usage: n5-ectest "+commands["diff"].usage)
			return exitOK
		case len(a) > 1 && a[0] == '-':
			fmt.Fprintf(stderr, "diff: unknown flag %s\nusage: n5-ectest %s\n", a, commands["diff"].usage)
			return exitUsage
		}
	}
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: n5-ectest "+commands["diff"].usage)
		return exitUsage
	}
	a, err := snapshot.ParsePath(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "diff: %v\n", err)
		return snapshot.DiffIncomplete
	}
	b, err := snapshot.ParsePath(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "diff: %v\n", err)
		return snapshot.DiffIncomplete
	}
	text, rc := snapshot.Diff(a, b)
	fmt.Fprint(stdout, text)
	return rc
}
