package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/boards"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

func init() {
	register("identify", command{usage: "identify [--json]", help: "machine, BIOS/EC, kernel, driver and what may run here (read-only)", run: cmdIdentify})
}

// cmdIdentify prints the identity of the machine (T0 fields, hardware
// status, what n5-ectest may do here). Read-only; output redacted.
func cmdIdentify(args []string) int {
	fs := flag.NewFlagSet("identify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if rc, ok := parseFlags(fs, args, nil); !ok {
		return rc
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: n5-ectest identify [--json]")
		return exitUsage
	}
	id := collectIdentity()
	red := newRedactor()
	if *asJSON {
		b, _ := json.MarshalIndent(map[string]any{"tool": toolName, "version": version, "commit": commitID(), "identity": id}, "", "  ")
		fmt.Fprintln(stdout, red.String(string(b)))
		return exitOK
	}
	or := func(s string) string { return orText(s, "?") }
	var b strings.Builder
	d := id.DMI
	fmt.Fprintf(&b, "n5-ectest       %s (commit %s)\n", version, commitID())
	fmt.Fprintf(&b, "product         %s (vendor %s)\n", or(d.Product), or(d.SysVendor))
	fmt.Fprintf(&b, "board           %s, version %s\n", or(d.Board), or(d.BoardVersion))
	fmt.Fprintf(&b, "BIOS            %s (%s, release %s)\n", or(d.BIOS), or(d.BIOSDate), or(d.BIOSRelease))
	fmt.Fprintf(&b, "EC firmware     %s\n", or(d.EC))
	fmt.Fprintf(&b, "kernel          %s, taint %s\n", or(id.Kernel), or(id.KernelTaint))
	fmt.Fprintf(&b, "OS              %s %s, lockdown %s, container %s\n", or(id.OS.ID), or(id.OS.VersionID), or(id.Lockdown), orText(id.Container, "none"))
	fmt.Fprintf(&b, "uptime          %.0f s, boot id %s\n", id.UptimeS, or(id.BootID))
	dr := id.Driver
	if dr.Loaded || dr.HwmonPath != "" {
		fmt.Fprintf(&b, "driver          %s %s, srcversion %s, taint %s, %s\n", n5driver.ModuleName, or(dr.Version), or(dr.SrcVersion), or(dr.Taint), dr.Mode())
		var ps []string
		for _, k := range []string{"force", "experimental_write", "debug_regs"} {
			if v, ok := dr.Params[k]; ok {
				ps = append(ps, k+"="+v)
			}
		}
		fmt.Fprintf(&b, "parameters      %s\n", or(strings.Join(ps, " ")))
		fmt.Fprintf(&b, "package         %s %s\n", n5driver.PackageName, orText(id.DKMSPackage, "not installed"))
		for _, f := range dr.Findings() {
			fmt.Fprintf(&b, "note            %s\n", f)
		}
	} else {
		fmt.Fprintf(&b, "driver          %s not loaded\n", n5driver.ModuleName)
	}
	if id.DCR.Dump != "" {
		fmt.Fprintf(&b, "dcr             %s  (pwm1 %d, pwm2 %d, pwm3 %d, pwm4 %d)\n", id.DCR.Dump, id.DCR.Channels["pwm1"], id.DCR.Channels["pwm2"], id.DCR.Channels["pwm3"], id.DCR.Channels["pwm4"])
	} else {
		fmt.Fprintf(&b, "dcr             unavailable: %s\n", id.DCR.Error)
	}
	fmt.Fprintf(&b, "hardware status %s\n", id.HWStatus.Describe())
	root := isRoot()
	if !root {
		fmt.Fprintf(&b, "note            not root: dcr and the container state need root - run: sudo n5-ectest identify\n")
	}
	if ok, why := boards.ReadOnly(d); !ok {
		fmt.Fprintf(&b, "n5-ectest       identify, diff and report only: %s\n", why)
	} else {
		fmt.Fprintf(&b, "n5-ectest       snapshot, log: yes\n")
		if !root {
			fmt.Fprintf(&b, "map/handback    not evaluated without root (sudo n5-ectest identify)\n")
		}
		for ch := 1; ch <= 4 && root; ch++ {
			br, pr := boards.WriteGate(id, ch)
			switch {
			case br == "" && pr == "":
				fmt.Fprintf(&b, "map/handback    pwm%d: allowed\n", ch)
			default:
				fmt.Fprintf(&b, "map/handback    pwm%d: gate failure, nothing would be written: %s\n", ch, strings.Trim(br+"; "+pr, "; "))
			}
		}
	}
	fmt.Fprint(stdout, red.String(b.String()))
	return exitOK
}
