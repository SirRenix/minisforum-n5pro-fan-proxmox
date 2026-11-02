package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/regulator"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/hwstatus"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

func specVersion() string { return spec.SpecVersion }

// Spec deviations (TESTSPEC 0.4 §1 rule 6): only where the tool knowingly
// differs from the version it names. Every bundle carries the open
// conditions of rule 4; a writing run adds what it does not implement.
// #13 (255 hold after a temperature abort) and #9 (G6 through ecram) are
// implemented and no longer listed.
const (
	DeviationConditions = "conditions of TESTSPEC 0.4 §1 rule 4 open: #1 D4 cross-check of n5-ectest against a second implementation (§9) and #4 report reproducing a hand evaluation of the first T4/T5 log not done yet"
	DeviationT4T5       = "T4/T5 not evaluated by the tool (TESTSPEC 0.4 §1 rule 4, §5.5, #4): reported INCONCLUSIVE \"not implemented\"; evaluate by hand per §5.5"
)

func baseDeviations() []string { return []string{DeviationConditions} }

// runDeviations are the deviations of a map/handback run.
func runDeviations(o runOpts) []string {
	var d []string
	if o.test == "handback" && o.ch != spec.PCIeChannel {
		d = append(d, DeviationT4T5)
	}
	return d
}

// ecramPresent reports the driver's debugfs ecram file (driver 0.2.1+sr2,
// debug_regs=1).
func ecramPresent() bool {
	_, err := os.Stat(n5driver.ECRAMPath(sysroot.Sys()))
	return err == nil
}

// redactionRules is the redaction rule set every meta.json lists (TESTSPEC
// 0.4 header, #8; identity.Redactor).
var redactionRules = []string{
	"host name (full and short) -> " + identity.HostPlaceholder + " (kernel, DMI and driver strings stay)",
	"DMI serial numbers and UUID: never collected; \"serial: X\" values -> [serial]",
	"MAC addresses -> [mac]",
	"IPv4 addresses -> [ipv4], IPv6 addresses -> [ipv6]",
	"mail addresses -> [mail] (systemd instance units stay)",
	"user names: never recorded; the user part of home directory paths -> /home/[user]",
	"SSH_* variables: only whether they are set, never their values",
}

// dpkgQuery is identity.Options.DpkgQuery (tests replace it).
var dpkgQuery func(string) string

// collectIdentity reads the machine identity below the sysroot.
func collectIdentity() identity.Identity {
	return identity.Collect(sysroot.Roots(), identity.Options{DpkgQuery: dpkgQuery})
}

// newRedactor redacts the host names of this host (and the fake root's);
// kernel, DMI and driver strings that contain the host name stay intact.
func newRedactor() *identity.Redactor {
	r := identity.NewRedactor(sysroot.Proc())
	proc := func(rel ...string) string {
		return sysroot.Read(filepath.Join(append([]string{sysroot.Proc()}, rel...)...))
	}
	r.ProtectIdentity(identity.Identity{
		DMI:           hwstatus.ReadIdentity(sysroot.Sys()),
		Kernel:        proc("sys", "kernel", "osrelease"),
		KernelVersion: proc("sys", "kernel", "version"),
		Driver:        n5driver.State{Version: sysroot.Read(filepath.Join(sysroot.Sys(), "module", n5driver.ModuleName, "version"))},
	})
	return r
}

// Provenance is the provenance block of every meta.json (design §8).
type Provenance struct {
	Tool           string             `json:"tool"`
	Version        string             `json:"version"`
	Commit         string             `json:"commit"`
	BinarySHA256   string             `json:"binary_sha256"`
	GoVersion      string             `json:"go_version"`
	Driver         provDriver         `json:"driver"`
	Kernel         provKernel         `json:"kernel"`
	DMI            hwstatus.Identity  `json:"dmi"`
	OS             identity.OSRelease `json:"os"`
	Lockdown       string             `json:"lockdown"`
	Container      string             `json:"container"`
	BootType       string             `json:"boot_type"`
	UptimeS        float64            `json:"uptime_s"`
	BootID         string             `json:"boot_id"`
	SpecVersion    string             `json:"spec_version"`
	SpecDeviations []string           `json:"spec_deviations"`
	// TestRoot is set when the run read a fake tree (N5ECTEST_ROOT): such a
	// result is not a result under TESTSPEC.
	TestRoot bool `json:"test_root,omitempty"`
}

type provDriver struct {
	Module      string            `json:"module"`
	Version     string            `json:"version"`
	SrcVersion  string            `json:"srcversion"`
	Parameters  map[string]string `json:"parameters"`
	DKMSPackage string            `json:"dkms_package_version"`
	Taint       string            `json:"taint"`
}

type provKernel struct {
	Release string `json:"release"`
	Version string `json:"version"`
	Taint   string `json:"taint"`
}

func provenance(id identity.Identity, boot string, deviations []string) Provenance {
	if deviations == nil {
		deviations = []string{}
	}
	return Provenance{
		Tool: toolName, Version: version, Commit: commitID(), BinarySHA256: binarySHA256(), GoVersion: goVersion(),
		Driver: provDriver{Module: n5driver.ModuleName, Version: id.Driver.Version, SrcVersion: id.Driver.SrcVersion,
			Parameters: id.Driver.Params, DKMSPackage: id.DKMSPackage, Taint: id.Driver.Taint},
		Kernel: provKernel{Release: id.Kernel, Version: id.KernelVersion, Taint: id.KernelTaint},
		DMI:    id.DMI, OS: id.OS, Lockdown: id.Lockdown, Container: id.Container,
		BootType: boot, UptimeS: id.UptimeS, BootID: id.BootID,
		SpecVersion: spec.SpecVersion, SpecDeviations: deviations, TestRoot: sysroot.Testing(),
	}
}

// toolLine names tool and driver for events.log (design §8).
func toolLine(id identity.Identity) string {
	return fmt.Sprintf("%s %s (commit %s), driver %s %s srcversion %s", toolName, version, commitID(), n5driver.ModuleName, orDash(id.Driver.Version), orDash(id.Driver.SrcVersion))
}

// isRoot reports root, or a test root (N5ECTEST_ROOT) that stands in for it;
// tests replace it to reach the non-root paths.
var isRoot = func() bool { return os.Geteuid() == 0 || sysroot.Testing() }

// requireRoot refuses without root on a real host (a test root is exempt).
func requireRoot(what string) bool {
	if !isRoot() {
		fmt.Fprintf(stderr, "%s: must run as root: sudo %s %s ...\n", what, toolName, strings.Fields(what)[0])
		return false
	}
	return true
}

// fangovRunning reports a running or (re)starting n5-fangov (G1 refusal).
func fangovRunning() (bool, string) {
	s := regulator.FangovState()
	return regulator.UnitRunning(s), s
}

// prepareOutDir creates the --out directory (with parents) and proves it
// writable before the first gate: a missing or read-only --out must not be
// found only after the operator confirmation or at the end of a run (in
// n5-fangov a bundle was lost that way, MW1 2026-10-17).
func prepareOutDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("--out is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("--out %s: %w", dir, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--out %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("--out %s is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".n5-ectest-probe-*")
	if err != nil {
		return fmt.Errorf("--out %s not writable: %w", dir, err)
	}
	name := f.Name()
	cerr := f.Close()
	if rerr := os.Remove(name); rerr != nil && cerr == nil {
		cerr = rerr
	}
	if cerr != nil {
		return fmt.Errorf("--out %s: %w", dir, cerr)
	}
	return nil
}

// now and sleepCtx are the clock of hardware runs (tests replace them).
var now = time.Now

func orDash(s string) string { return orText(s, "-") }

func orText(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func joinOr(parts []string, sep, none string) string {
	if len(parts) == 0 {
		return none
	}
	return strings.Join(parts, sep)
}
