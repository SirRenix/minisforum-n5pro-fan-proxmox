// Package snapshot writes and compares state snapshots in the text format
// of tools/f8a/f8a-snapshot.sh, so `n5-ectest diff` and
// `f8a-snapshot.sh --diff` read the files of both tools: sections
// "=== name ===", EC RAM dumps between "=== BEGIN EC_RAM n ===" and
// "=== END EC_RAM n ===" ("00: b0 b1 ..." rows), I2EC between
// "=== BEGIN I2EC ===" and "=== END I2EC ===" ("1800: ..." rows), tachs as
// "TACHn: <rpm> RPM", and "RESULT: complete|INCOMPLETE (...)".
//
// Mode B (driver loaded) never touches I/O ports: hwmon values (the pwm
// files are the driver's cache, not the EC state) and the debugfs dcr file.
// Mode A (module unloaded, native /dev/port reads) is built after
// maintenance window 1 (design §3); until then f8a-snapshot.sh is the mode-A
// path.
package snapshot

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/hwread"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// Header carries the provenance lines of a snapshot file.
type Header struct {
	Tool, Version, SpecVersion, Label, Mode, Boot, DateUTC string
	FangovState                                            string
}

// ModeB is the result of a mode-B snapshot.
type ModeB struct {
	Text       string
	Incomplete []string
	Reading    hwread.Reading
	DCR        identity.DCRInfo
}

func section(b *strings.Builder, name string) { fmt.Fprintf(b, "\n=== %s ===\n", name) }

func c1(v float64) string {
	if math.IsNaN(v) {
		return "n/a"
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// I2ECRows renders bytes from base as "1800: xx ..." rows of 16.
func I2ECRows(base int, data []byte) string {
	var b strings.Builder
	for off := 0; off < len(data); off += 16 {
		fmt.Fprintf(&b, "%04x:", base+off)
		for i := off; i < off+16 && i < len(data); i++ {
			fmt.Fprintf(&b, " %02x", data[i])
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ECRAMSection renders dump n of EC RAM 0x00-0xff ("??" = timeout) with the
// markers f8a-snapshot.sh uses.
func ECRAMSection(n int, data [256]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== BEGIN EC_RAM %d ===\n", n)
	for row := 0; row < 256; row += 16 {
		fmt.Fprintf(&b, "%02x:", row)
		for i := row; i < row+16; i++ {
			fmt.Fprintf(&b, " %s", data[i])
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "=== END EC_RAM %d ===\n", n)
	return b.String()
}

// TakeModeB reads everything for a mode-B snapshot. procRoot and etcRoot
// are needed for the module and configuration lines.
func TakeModeB(h Header, id identity.Identity, sysRoot, procRoot, etcRoot string) ModeB {
	var b strings.Builder
	m := ModeB{}
	section(&b, "provenance")
	fmt.Fprintf(&b, "tool: %s %s\nspec_version: %s\nlabel: %s\nmode: %s\nboot_type: %s\ndate_utc: %s\n", h.Tool, h.Version, h.SpecVersion, h.Label, h.Mode, h.Boot, h.DateUTC)
	b.WriteString("mode B: driver module loaded; no port I/O (hwmon and the debugfs dcr file only)\n")
	section(&b, "identity")
	fmt.Fprintf(&b, "uptime_s: %.2f\nboot_id: %s\nkernel: %s\nkernel_version: %s\nhost: %s\n", id.UptimeS, id.BootID, id.Kernel, id.KernelVersion, identity.HostPlaceholder)
	d := id.DMI
	for _, kv := range [][2]string{{"product_name", d.Product}, {"board_name", d.Board}, {"board_version", d.BoardVersion}, {"sys_vendor", d.SysVendor},
		{"bios_version", d.BIOS}, {"bios_date", d.BIOSDate}, {"bios_release", d.BIOSRelease}, {"ec_firmware_release", d.EC}} {
		fmt.Fprintf(&b, "dmi.%s: %s\n", kv[0], kv[1])
	}
	fmt.Fprintf(&b, "os: %s %s\nlockdown: %s\ncontainer: %s\n", id.OS.ID, id.OS.VersionID, orNA(id.Lockdown), orDash(id.Container))
	dr := id.Driver
	fmt.Fprintf(&b, "driver: %s %s, srcversion %s, taint %s, %s; package %s\n", n5driver.ModuleName, orDash(dr.Version), orDash(dr.SrcVersion), orDash(dr.Taint), dr.Mode(), orDash(id.DKMSPackage))
	section(&b, "lsmod (filtered)")
	b.WriteString(filterLines(filepath.Join(procRoot, "modules"), func(l string) bool {
		f := strings.Fields(l)
		if len(f) == 0 {
			return false
		}
		switch f[0] {
		case n5driver.ModuleName, "k10temp", "drivetemp", "nvme", "it87", "nct6775":
			return true
		}
		return false
	}, "(none)"))
	section(&b, "/proc/ioports 002e-002f/0062/0066/0068/006c")
	b.WriteString(filterLines(filepath.Join(procRoot, "ioports"), func(l string) bool {
		t := strings.TrimSpace(l)
		for _, p := range []string{"002e-", "002f-", "0062-", "0066-", "0068-", "006c-"} {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
		return false
	}, "(no entries: ports not reserved)"))
	section(&b, "module parameters")
	if len(dr.Params) == 0 {
		b.WriteString("module not loaded\n")
	} else {
		var ks []string
		for k := range dr.Params {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Fprintf(&b, "%s=%s\n", k, dr.Params[k])
		}
	}
	b.WriteString("modprobe.d / modules-load.d entries:\n")
	var conf []string
	for _, g := range []string{"modprobe.d/*.conf", "modules-load.d/*.conf"} {
		files, _ := filepath.Glob(filepath.Join(etcRoot, g))
		for _, f := range files {
			for _, l := range strings.Split(readAll(f), "\n") {
				if strings.Contains(l, "minisforum") {
					conf = append(conf, f+":"+l)
				}
			}
		}
	}
	if len(conf) == 0 {
		b.WriteString("(none)\n")
	} else {
		b.WriteString(strings.Join(conf, "\n") + "\n")
	}
	r := hwread.Read(sysRoot, procRoot)
	m.Reading = r
	section(&b, "independent temperatures (k10temp, nvme, drivetemp)")
	fmt.Fprintf(&b, "Tctl=%s NVMe_max=%s HDD_max=%s\n", c1(r.Tctl), c1(r.NVMeMax), c1(r.HDDMax))
	section(&b, "n5-fangov")
	fmt.Fprintf(&b, "is-active: %s\n", orNA(h.FangovState))
	section(&b, fmt.Sprintf("hwmon %s (%s)", n5driver.ModuleName, orNA(r.Hwmon)))
	if r.Hwmon == "" {
		m.Incomplete = append(m.Incomplete, "no hwmon device of the driver")
	} else {
		for n := 1; n <= 4; n++ {
			v := strconv.Itoa(r.Fan[n])
			if r.Fan[n] < 0 {
				v = "error"
				m.Incomplete = append(m.Incomplete, fmt.Sprintf("fan%d_input unreadable", n))
			}
			fmt.Fprintf(&b, "fan%d_input=%s (%s)\n", n, v, orText(r.Label[n], "no label"))
		}
		for _, k := range []string{"cpu", "system", "board", "ambient"} {
			fmt.Fprintf(&b, "ec_%s=%s\n", k, c1(r.EC[k]))
		}
		for n := 1; n <= 3; n++ {
			if r.Fan[n] >= 0 {
				fmt.Fprintf(&b, "TACH%d: %d RPM (hwmon fan%d_input)\n", n, r.Fan[n], n)
			}
		}
		for n := 1; n <= 4; n++ {
			if r.PWM[n] < 0 && r.Enable[n] < 0 {
				fmt.Fprintf(&b, "pwm%d: not visible (read-only driver profile)\n", n)
				continue
			}
			fmt.Fprintf(&b, "pwm%d=%d pwm%d_enable=%d (driver cache, not EC state)\n", n, r.PWM[n], n, r.Enable[n])
		}
	}
	section(&b, "dcr (debugfs, I2EC 0x1800-0x180F)")
	m.DCR = identity.ReadDCRInfo(sysRoot)
	if m.DCR.Dump != "" {
		dcr, _ := n5driver.ParseDCR(m.DCR.Dump)
		b.WriteString("=== BEGIN I2EC ===\n")
		b.WriteString(I2ECRows(0x1800, dcr[:]))
		b.WriteString("=== END I2EC ===\n")
		fmt.Fprintf(&b, "DCR1(CPU)=%02x DCR2(SSD)=%02x DCR3/4(HDD)=%02x/%02x DCR5(PCIe)=%02x\n", dcr.Reg(1), dcr.Reg(2), dcr.Reg(3), dcr.Reg(4), dcr.Reg(5))
	} else {
		fmt.Fprintf(&b, "dcr unavailable: %s\n", m.DCR.Error)
	}
	section(&b, "independent temperatures (end)")
	fmt.Fprintf(&b, "Tctl=%s NVMe_max=%s HDD_max=%s\n", c1(hwread.Tctl(sysRoot)), c1(r.NVMeMax), c1(r.HDDMax))
	b.WriteString("\n")
	if len(m.Incomplete) > 0 {
		fmt.Fprintf(&b, "RESULT: INCOMPLETE (%s)\n", strings.Join(m.Incomplete, "; "))
	} else {
		b.WriteString("RESULT: complete\n")
	}
	m.Text = b.String()
	return m
}

func readAll(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func filterLines(p string, keep func(string) bool, none string) string {
	var out []string
	for _, l := range strings.Split(readAll(p), "\n") {
		if keep(l) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return none + "\n"
	}
	return strings.Join(out, "\n") + "\n"
}

func orDash(s string) string { return orText(s, "-") }
func orNA(s string) string   { return orText(s, "n/a") }
func orText(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---- parsing and diff -------------------------------------------------

// File is a parsed snapshot.
type File struct {
	Name       string
	ECRAM      map[int]map[int]string // dump number -> offset -> byte ("??" kept)
	I2EC       map[int]string         // address -> byte
	Incomplete string                 // the RESULT: INCOMPLETE reason, "" = none
}

func isHexAddr(s string, digits int) bool {
	if len(s) != digits+1 || s[digits] != ':' {
		return false
	}
	_, err := strconv.ParseUint(s[:digits], 16, 32)
	return err == nil
}

// Parse reads a snapshot file of f8a-snapshot.sh or n5-ectest.
func Parse(name, text string) File {
	f := File{Name: name, ECRAM: map[int]map[int]string{}, I2EC: map[int]string{}}
	dump := 0
	inI2EC := false
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		var n int
		switch {
		case strings.HasPrefix(l, "=== BEGIN EC_RAM ") && strings.HasSuffix(l, " ==="):
			if _, err := fmt.Sscanf(l, "=== BEGIN EC_RAM %d ===", &n); err == nil {
				dump = n
				if f.ECRAM[n] == nil {
					f.ECRAM[n] = map[int]string{}
				}
			}
			continue
		case strings.HasPrefix(l, "=== END EC_RAM "):
			dump = 0
			continue
		case l == "=== BEGIN I2EC ===":
			inI2EC = true
			continue
		case l == "=== END I2EC ===":
			inI2EC = false
			continue
		case strings.HasPrefix(l, "RESULT: INCOMPLETE"):
			f.Incomplete = strings.TrimSpace(strings.TrimPrefix(l, "RESULT: INCOMPLETE"))
			continue
		}
		fl := strings.Fields(l)
		if len(fl) == 0 {
			continue
		}
		switch {
		case dump > 0 && isHexAddr(fl[0], 2):
			base, _ := strconv.ParseUint(fl[0][:2], 16, 16)
			for i, v := range fl[1:] {
				f.ECRAM[dump][int(base)+i] = strings.ToLower(v)
			}
		case inI2EC && isHexAddr(fl[0], 4):
			base, _ := strconv.ParseUint(fl[0][:4], 16, 16)
			for i, v := range fl[1:] {
				f.I2EC[int(base)+i] = strings.ToLower(v)
			}
		}
	}
	return f
}

// ParsePath reads a snapshot file, or snapshot.txt inside a bundle
// directory.
func ParsePath(p string) (File, error) {
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		p = filepath.Join(p, "snapshot.txt")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return File{}, err
	}
	return Parse(filepath.Base(p), string(b)), nil
}

// complete reports whether dump n covers 0x00-0xff (as f8a: at least 256
// bytes in range).
func (f File) complete(n int) bool {
	cnt := 0
	for off := range f.ECRAM[n] {
		if off >= 0 && off <= 255 {
			cnt++
		}
	}
	return cnt >= 256
}

// Temperature offsets excluded from the byte comparison, and the curve
// table regions of TESTSPEC C1 (end exclusive).
var (
	TempOffsets  = map[int]bool{0x04: true, 0x05: true, 0x06: true, 0x09: true}
	CurveRegions = [][2]int{{0x10, 0x28}, {0x30, 0x39}, {0x70, 0x82}}
)

// Diff exit codes (TESTSPEC §5.3).
const (
	DiffIdentical  = 0
	DiffChanged    = 1
	DiffIncomplete = 2
)

// Diff compares the first EC RAM dumps and the I2EC bytes of a and b. It
// returns the report text and the exit code: 0 curve tables identical, 1
// changed, 2 incomplete input (no complete dump 1, a file marked
// INCOMPLETE, or "??" in a curve table byte).
func Diff(a, b File) (string, int) {
	var out strings.Builder
	for _, f := range []File{a, b} {
		if !f.complete(1) {
			fmt.Fprintf(&out, "no complete EC RAM dump 1 in %s (mode-B file?)\n", f.Name)
			return out.String(), DiffIncomplete
		}
	}
	fmt.Fprintf(&out, "diff A=%s\n     B=%s\n", a.Name, b.Name)
	ra, rb := a.ECRAM[1], b.ECRAM[1]
	out.WriteString("EC RAM bytes changed (temperature offsets 0x04 0x05 0x06 0x09 excluded):\n")
	n := 0
	for o := 0; o < 256; o++ {
		if TempOffsets[o] {
			continue
		}
		if ra[o] != rb[o] {
			fmt.Fprintf(&out, "  0x%02x: %s -> %s\n", o, ra[o], rb[o])
			n++
		}
	}
	if n == 0 {
		out.WriteString("  (none)\n")
	}
	out.WriteString("curve-table regions (TESTSPEC C1):\n")
	changed, unknown := false, false
	for _, reg := range CurveRegions {
		var ch []string
		for o := reg[0]; o < reg[1]; o++ {
			if ra[o] == "??" || rb[o] == "??" {
				unknown = true
			}
			if ra[o] != rb[o] {
				ch = append(ch, fmt.Sprintf("0x%02x", o))
			}
		}
		if len(ch) == 0 {
			fmt.Fprintf(&out, "  0x%02x-0x%02x: identical\n", reg[0], reg[1]-1)
		} else {
			fmt.Fprintf(&out, "  0x%02x-0x%02x: CHANGED at %s\n", reg[0], reg[1]-1, strings.Join(ch, " "))
			changed = true
		}
	}
	out.WriteString("I2EC 0x1800-0x182F bytes changed (0x1803 DCR1 CPU, 0x1804 DCR2 SSD, 0x1805/0x1806 DCR3/4 HDD, 0x1807 DCR5 PCIe):\n")
	if len(a.I2EC) == 0 || len(b.I2EC) == 0 {
		out.WriteString("  (I2EC section missing in one file)\n")
	} else {
		var addrs []int
		for k := range a.I2EC {
			addrs = append(addrs, k)
		}
		sort.Ints(addrs)
		m := 0
		for _, k := range addrs {
			if v, ok := b.I2EC[k]; !ok || v != a.I2EC[k] {
				if !ok {
					v = "missing"
				}
				fmt.Fprintf(&out, "  0x%04x: %s -> %s\n", k, a.I2EC[k], v)
				m++
			}
		}
		if m == 0 {
			out.WriteString("  (none)\n")
		}
	}
	for _, f := range []File{a, b} {
		if f.Incomplete != "" {
			fmt.Fprintf(&out, "input incomplete: %s is marked INCOMPLETE %s\n", f.Name, f.Incomplete)
			return out.String(), DiffIncomplete
		}
	}
	if unknown {
		out.WriteString("input incomplete: a curve-table byte reads ?? (EC read timeout)\n")
		return out.String(), DiffIncomplete
	}
	if changed {
		out.WriteString("C1 curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81: CHANGED\n")
		return out.String(), DiffChanged
	}
	out.WriteString("C1 curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81: identical\n")
	return out.String(), DiffIdentical
}
