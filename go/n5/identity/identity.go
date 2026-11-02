// Package identity collects the read-only identity of an N5-family machine
// for test records (TESTSPEC §2, T0) and for the provenance of every result:
// DMI (no serials, no UUID), kernel, taint, boot id, uptime, the EC driver's
// sysfs state and its optional register dump, the DKMS package version, the
// OS release, kernel lockdown and whether the process runs in a container.
// Nothing here writes. Redact removes host names, MAC and IP addresses,
// serial numbers and home paths from text before it is stored.
package identity

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/hwstatus"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// Roots are the file system roots the identity is read from ("/sys",
// "/proc", "/etc", "/run" on a real host; a fake tree in tests).
type Roots struct {
	Sys, Proc, Etc, Run string
}

// HostRoots are the roots of the running host.
func HostRoots() Roots { return Roots{Sys: "/sys", Proc: "/proc", Etc: "/etc", Run: "/run"} }

// DCRInfo is the optional register dump of the driver (debug_regs=1).
type DCRInfo struct {
	Path     string         `json:"path,omitempty"`
	Dump     string         `json:"dump,omitempty"`
	Channels map[string]int `json:"channels,omitempty"` // pwm1..pwm4 -> DCR value
	Error    string         `json:"error,omitempty"`    // why there is no dump
}

// OSRelease is ID and VERSION_ID of /etc/os-release.
type OSRelease struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
}

// Identity is what a record needs to name the machine and its software.
type Identity struct {
	DMI           hwstatus.Identity `json:"dmi"`
	Kernel        string            `json:"kernel"`         // /proc/sys/kernel/osrelease
	KernelVersion string            `json:"kernel_version"` // /proc/sys/kernel/version
	KernelTaint   string            `json:"kernel_taint"`   // /proc/sys/kernel/tainted
	BootID        string            `json:"boot_id"`
	UptimeS       float64           `json:"uptime_s"`
	Driver        n5driver.State    `json:"driver"`
	DKMSPackage   string            `json:"dkms_package_version"` // "" = not installed or unknown
	DCR           DCRInfo           `json:"dcr"`
	OS            OSRelease         `json:"os"`
	// Lockdown is the active kernel lockdown mode (none, integrity,
	// confidentiality); "" when /sys/kernel/security/lockdown is unreadable.
	Lockdown string `json:"lockdown"`
	// Container names the container the process runs in ("" = none
	// detected); ContainerEvidence says how it was detected or why it is
	// unknown.
	Container         string          `json:"container"`
	ContainerEvidence string          `json:"container_evidence"`
	HWStatus          hwstatus.Result `json:"hw_status"`
	HWTable           hwstatus.Meta   `json:"hw_status_table"`
}

// Options of Collect.
type Options struct {
	// DpkgQuery returns the installed version of a Debian package ("" when
	// not installed). nil runs dpkg-query (5 s timeout).
	DpkgQuery func(pkg string) string
}

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Collect reads the identity. It never writes.
func Collect(r Roots, o Options) Identity {
	proc := func(rel ...string) string { return read(filepath.Join(append([]string{r.Proc}, rel...)...)) }
	id := Identity{
		DMI:           hwstatus.ReadIdentity(r.Sys),
		Kernel:        proc("sys", "kernel", "osrelease"),
		KernelVersion: proc("sys", "kernel", "version"),
		KernelTaint:   proc("sys", "kernel", "tainted"),
		BootID:        proc("sys", "kernel", "random", "boot_id"),
		Driver:        n5driver.Read(r.Sys),
		HWTable:       hwstatus.Source(),
	}
	if f := strings.Fields(proc("uptime")); len(f) > 0 {
		id.UptimeS, _ = strconv.ParseFloat(f[0], 64)
	}
	id.DCR = ReadDCRInfo(r.Sys)
	q := o.DpkgQuery
	if q == nil {
		q = dpkgQuery
	}
	id.DKMSPackage = q(n5driver.PackageName)
	id.OS = readOSRelease(filepath.Join(r.Etc, "os-release"))
	id.Lockdown = Lockdown(r.Sys)
	id.Container, id.ContainerEvidence = DetectContainer(r.Proc, r.Run)
	id.HWStatus = hwstatus.Lookup(id.DMI)
	return id
}

// ReadDCRInfo reads the driver's register dump when the driver offers one.
func ReadDCRInfo(sysRoot string) DCRInfo {
	p := n5driver.DCRPath(sysRoot)
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			// debugfs is root-only: without root the file cannot even be seen
			return DCRInfo{Path: p, Error: "debugfs not readable without root: run as root to read " + p}
		}
		return DCRInfo{Error: "no " + p + " (driver 0.2.1+sr1 with debug_regs=1 only; validation, not for operation)"}
	}
	d, err := n5driver.ReadDCR(p)
	if err != nil {
		return DCRInfo{Path: p, Error: err.Error()}
	}
	out := DCRInfo{Path: p, Dump: d.String(), Channels: map[string]int{}}
	for ch := 1; ch <= 4; ch++ {
		if v, ok := d.Channel(ch); ok {
			out.Channels[fmt.Sprintf("pwm%d", ch)] = v
		}
	}
	return out
}

func dpkgQuery(pkg string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dpkg-query", "-W", "-f", "${Status}|${Version}", pkg).Output()
	if err != nil {
		return ""
	}
	st, ver, ok := strings.Cut(strings.TrimSpace(string(out)), "|")
	if !ok || !strings.HasSuffix(st, " installed") {
		return ""
	}
	return ver
}

func readOSRelease(p string) OSRelease {
	var o OSRelease
	for _, l := range strings.Split(read(p), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			o.ID = v
		case "VERSION_ID":
			o.VersionID = v
		}
	}
	return o
}

var lockdownRe = regexp.MustCompile(`\[([a-z]+)\]`)

// Lockdown returns the active lockdown mode from
// <sys>/kernel/security/lockdown ("[none] integrity confidentiality" ->
// "none"), "" when unreadable.
func Lockdown(sysRoot string) string {
	m := lockdownRe.FindStringSubmatch(read(filepath.Join(sysRoot, "kernel", "security", "lockdown")))
	if m == nil {
		return ""
	}
	return m[1]
}

// DetectContainer reports a container: "container=" in the environment of
// pid 1 (systemd, LXC, Docker set it) or <run>/systemd/container. It returns
// the container name ("" = none) and the evidence; an unreadable
// /proc/1/environ (not root) without the run file is "unknown".
func DetectContainer(procRoot, runRoot string) (name, evidence string) {
	if v := read(filepath.Join(runRoot, "systemd", "container")); v != "" {
		return v, "/run/systemd/container"
	}
	b, err := os.ReadFile(filepath.Join(procRoot, "1", "environ"))
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return "unknown", "/proc/1/environ not readable without root: run as root"
		}
		return "unknown", "/proc/1/environ not readable (" + errnoText(err) + ")"
	}
	for _, kv := range strings.Split(string(b), "\x00") {
		if v, ok := strings.CutPrefix(kv, "container="); ok {
			if v == "" {
				v = "unnamed"
			}
			return v, "container= in the environment of pid 1"
		}
	}
	if _, err := os.Stat(filepath.Join(procRoot, "1", "root", ".dockerenv")); err == nil {
		return "docker", "/.dockerenv"
	}
	return "", "no container= in the environment of pid 1, no /run/systemd/container"
}

func errnoText(err error) string {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}
