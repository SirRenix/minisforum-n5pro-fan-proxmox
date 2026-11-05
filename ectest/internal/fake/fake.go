// Package fake builds a fake host tree for tests (sys, proc, run, etc,
// dev below one root, used through N5ECTEST_ROOT) with an N5 Pro, its EC
// driver and the hwmon devices the tests need, and models the driver's
// reaction to pwm writes (pwm cache, DCR registers, tachs). It is test
// support; nothing in it touches the real host.
package fake

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sim"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// Options of a fake host.
type Options struct {
	Product, Board, Vendor, BIOS, EC string
	DriverVersion                    string
	Force                            string // module parameter force (Y/N)
	DebugRegs                        bool   // dcr file present
	PWMExposed                       bool   // pwm nodes present
	NoDriver                         bool   // module not loaded at all
	AMDGPU                           bool   // a second hwmon device with pwm1
}

// Reference is the reference N5 Pro with the patched driver and
// debug_regs=1.
func Reference() Options {
	return Options{Product: "N5 PRO", Board: "F8NAA", Vendor: n5driver.SysVendor, BIOS: "1.05", EC: "0.14",
		DriverVersion: "0.2.1+sr1~rc1", Force: "N", DebugRegs: true, PWMExposed: true, AMDGPU: true}
}

// Host is a fake host tree.
type Host struct {
	Root   string
	t      testing.TB
	mu     sync.Mutex
	hwmonN int
	dcr    [8]int
	enable [5]int
	pwm    [5]int
	// AutoValue is the DCR an auto command sets per channel (PCIe keeps).
	AutoValue [5]int
	// Writes lists every pwm write the driver model saw ("pwm3=255",
	// "pwm3_enable=1"), in order; AllWrites includes other devices' files.
	Writes    []string
	AllWrites []string
	// Lag delays a write per register DCR1..DCR5 (index = register number)
	// on the virtual clock Clock (MW1 2026-10-17 Z1/Z2: PCIe ~0.2 s, HDD
	// ramp DCR3 then DCR4); Advance applies what is due. Without Clock every
	// write applies at once.
	Lag     [8]time.Duration
	Clock   func() time.Time
	pending [8]*lagged
}

type lagged struct {
	v  int
	at time.Time
}

// SysDevice is the device directory of the EC driver below the root.
const SysDevice = "sys/devices/platform/" + n5driver.ModuleName

func (h *Host) path(rel string) string { return filepath.Join(h.Root, filepath.FromSlash(rel)) }

// Put writes a file below the root (parents created).
func (h *Host) Put(rel, content string) {
	h.t.Helper()
	p := h.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// Get reads a file below the root, trimmed.
func (h *Host) Get(rel string) string {
	b, err := os.ReadFile(h.path(rel))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Remove deletes a file below the root (e.g. fan4_input, which driver sr1 does not register).
func (h *Host) Remove(rel string) {
	h.t.Helper()
	if err := os.Remove(h.path(rel)); err != nil {
		h.t.Fatal(err)
	}
}

func (h *Host) link(rel, target string) {
	h.t.Helper()
	p := h.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	_ = os.Remove(p)
	if err := os.Symlink(target, p); err != nil {
		h.t.Fatal(err)
	}
}

// New builds the tree in t.TempDir() and sets N5ECTEST_ROOT for the test.
func New(t testing.TB, o Options) *Host {
	t.Helper()
	h := &Host{Root: t.TempDir(), t: t, hwmonN: 14, AutoValue: [5]int{0, 0x55, 0x4a, 0x57, -1}}
	if tt, ok := t.(*testing.T); ok {
		tt.Setenv("N5ECTEST_ROOT", h.Root)
	}
	for k, v := range map[string]string{"product_name": o.Product, "board_name": o.Board, "board_version": "Default string",
		"sys_vendor": o.Vendor, "bios_version": o.BIOS, "bios_date": "03/31/2026", "bios_release": "1.5", "ec_firmware_release": o.EC,
		"product_serial": "SERIAL-SECRET-1", "product_uuid": "11111111-2222-3333-4444-555555555555"} {
		h.Put("sys/class/dmi/id/"+k, v+"\n")
	}
	h.Put("sys/kernel/security/lockdown", "[none] integrity confidentiality\n")
	h.Put("proc/loadavg", "0.20 0.25 0.30 1/500 12345\n")
	h.Put("sys/devices/system/cpu/online", "0-23\n")
	h.Put("proc/uptime", "3600.00 80000.00\n")
	h.Put("proc/sys/kernel/osrelease", "7.0.14-20-pve\n")
	h.Put("proc/sys/kernel/version", "#1 SMP PREEMPT_DYNAMIC PMX 7.0.14-20\n")
	h.Put("proc/sys/kernel/tainted", "12288\n")
	h.Put("proc/sys/kernel/random/boot_id", "0f0e0d0c-0b0a-0908-0706-050403020100\n")
	h.Put("proc/sys/kernel/hostname", "fakehost-secret\n")
	h.Put("proc/1/environ", "PATH=/usr/bin\x00")
	h.Put("proc/ioports", "0000-0cf7 : PCI Bus 0000:00\n  0060-0060 : keyboard\n")
	h.Put("etc/os-release", "ID=debian\nVERSION_ID=\"13\"\n")
	if err := os.MkdirAll(h.path("run"), 0o755); err != nil {
		t.Fatal(err)
	}
	// independent sensors
	h.mkHwmon("sys/devices/pci0000:00/0000:00:18.3/hwmon/hwmon0", "hwmon0", map[string]string{"name": "k10temp", "temp1_label": "Tctl", "temp1_input": "40000"})
	h.mkHwmon("sys/devices/pci0000:00/0000:01:00.0/nvme/nvme0/hwmon1", "hwmon1", map[string]string{"name": "nvme", "temp1_input": "45000", "temp2_input": "44000"})
	h.mkHwmon("sys/devices/pci0000:00/0000:02:00.0/ata1/host0/target0:0:0/0:0:0:0/hwmon/hwmon2", "hwmon2", map[string]string{"name": "drivetemp", "temp1_input": "38000"})
	if o.AMDGPU {
		h.mkHwmon("sys/devices/pci0000:00/0000:c4:00.0/hwmon/hwmon3", "hwmon3", map[string]string{"name": "amdgpu", "pwm1": "120", "pwm1_enable": "2", "fan1_input": "0"})
	}
	h.dcr = [8]int{0, 0x55, 0x4a, 0x64, 0x64, 0x8c, 0, 0}
	for ch := 1; ch <= 4; ch++ {
		h.enable[ch], h.pwm[ch] = 2, 255
	}
	if !o.NoDriver {
		mod := "sys/module/" + n5driver.ModuleName
		h.Put(mod+"/version", o.DriverVersion+"\n")
		h.Put(mod+"/srcversion", "886E3C25669AF187D2E96F0\n")
		h.Put(mod+"/taint", "OE\n")
		h.Put(mod+"/parameters/force", o.Force+"\n")
		h.Put(mod+"/parameters/experimental_write", "Y\n")
		dr := "N"
		if o.DebugRegs {
			dr = "Y"
		}
		h.Put(mod+"/parameters/debug_regs", dr+"\n")
		h.Put("proc/modules", n5driver.ModuleName+" 24576 0 - Live 0x0000000000000000 (OE)\nk10temp 12288 0 - Live 0x0000000000000000\n")
		h.mkDriverHwmon(o.PWMExposed)
		if o.DebugRegs {
			h.writeDCR()
		}
	} else {
		h.Put("proc/modules", "k10temp 12288 0 - Live 0x0000000000000000\n")
	}
	return h
}

func (h *Host) mkHwmon(devRel, name string, attrs map[string]string) {
	for k, v := range attrs {
		h.Put(devRel+"/"+k, v+"\n")
	}
	rel, err := filepath.Rel(h.path("sys/class/hwmon"), h.path(devRel))
	if err != nil {
		h.t.Fatal(err)
	}
	h.link("sys/class/hwmon/"+name, rel)
}

// HwmonRel is the class path of the driver's hwmon device ("sys/class/hwmon/hwmon14").
func (h *Host) HwmonRel() string { return fmt.Sprintf("sys/class/hwmon/hwmon%d", h.hwmonN) }

func (h *Host) devRel() string { return fmt.Sprintf("%s/hwmon/hwmon%d", SysDevice, h.hwmonN) }

func (h *Host) mkDriverHwmon(pwm bool) {
	attrs := map[string]string{"name": n5driver.ModuleName,
		"fan1_label": "CPU Fan", "fan2_label": "SSD Fan", "fan3_label": "HDD Fan Group", "fan4_label": "PCIe Fan (no tach)",
		"temp1_label": "CPU Temp", "temp2_label": "System Temp", "temp3_label": "Board Temp", "temp4_label": "Ambient Temp",
		"temp1_input": "37000", "temp2_input": "33000", "temp3_input": "27000", "temp4_input": "22000", "fan4_input": "0"}
	if pwm {
		for ch := 1; ch <= 4; ch++ {
			attrs[fmt.Sprintf("pwm%d", ch)] = strconv.Itoa(h.pwm[ch])
			attrs[fmt.Sprintf("pwm%d_enable", ch)] = strconv.Itoa(h.enable[ch])
		}
	}
	h.mkHwmon(h.devRel(), fmt.Sprintf("hwmon%d", h.hwmonN), attrs)
	h.updateFans()
}

func (h *Host) writeDCR() {
	var b strings.Builder
	b.WriteString("1800: 00 ff")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, " %02x", h.dcr[i])
	}
	b.WriteString(" 00 06 00 00 00 00\n")
	h.Put("sys/kernel/debug/"+n5driver.ModuleName+"/dcr", b.String())
}

func (h *Host) updateFans() {
	for f := 1; f <= 3; f++ {
		h.Put(h.devRel()+fmt.Sprintf("/fan%d_input", f), strconv.Itoa(sim.RPM(f, h.dcr[f]))+"\n")
	}
}

func chDCR(ch int) int { n, _ := n5driver.ChannelDCR(ch); return n }

// Install hooks the driver model into pwmw for the test (every write the
// tool makes is applied to the tree as the driver and the EC would).
func (h *Host) Install() {
	old := pwmw.Recorder
	pwmw.Recorder = h.Apply
	h.t.Cleanup(func() { pwmw.Recorder = old })
}

// Apply models one write to path (absolute).
func (h *Host) Apply(path, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(real, filepath.Base(path))
	}
	root, _ := filepath.EvalSymlinks(h.Root)
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	h.AllWrites = append(h.AllWrites, rel+"="+value)
	if !strings.HasPrefix(rel, SysDevice+"/") {
		return
	}
	base := filepath.Base(rel)
	v, _ := strconv.Atoi(value)
	h.Writes = append(h.Writes, base+"="+value)
	var ch int
	switch {
	case strings.HasSuffix(base, "_enable"):
		fmt.Sscanf(base, "pwm%d_enable", &ch)
		switch v {
		case 1:
			if h.enable[ch] != 1 {
				h.pwm[ch] = 255
				h.setDCR(ch, 255)
			}
			h.enable[ch] = 1
		case 2:
			h.enable[ch] = 2
			if a := h.AutoValue[ch]; a >= 0 {
				h.setDCR(ch, a)
			}
		}
	default:
		fmt.Sscanf(base, "pwm%d", &ch)
		h.pwm[ch] = v
		h.setDCR(ch, v)
	}
	// the driver's cache: what pwmN/pwmN_enable read back
	dir := filepath.Dir(rel)
	h.Put(dir+fmt.Sprintf("/pwm%d", ch), strconv.Itoa(h.pwm[ch])+"\n")
	h.Put(dir+fmt.Sprintf("/pwm%d_enable", ch), strconv.Itoa(h.enable[ch])+"\n")
	if h.Get("sys/kernel/debug/"+n5driver.ModuleName+"/dcr") != "" {
		h.writeDCR()
	}
	h.updateFans()
}

func (h *Host) setDCR(ch, v int) {
	regs := []int{chDCR(ch)}
	if ch == 3 {
		regs = append(regs, 4)
	}
	for _, n := range regs {
		if h.Clock != nil && h.Lag[n] > 0 {
			h.pending[n] = &lagged{v: v, at: h.Clock().Add(h.Lag[n])}
			continue
		}
		h.pending[n] = nil
		h.dcr[n] = v
	}
}

// Advance applies the lagged writes that are due at Clock() and rewrites
// the dcr file and the tachs.
func (h *Host) Advance() {
	if h.Clock == nil {
		return
	}
	t := h.Clock()
	h.mu.Lock()
	defer h.mu.Unlock()
	changed := false
	for n, p := range h.pending {
		if p != nil && !t.Before(p.at) {
			h.dcr[n] = p.v
			h.pending[n] = nil
			changed = true
		}
	}
	if !changed {
		return
	}
	if h.Get("sys/kernel/debug/"+n5driver.ModuleName+"/dcr") != "" {
		h.writeDCR()
	}
	h.updateFans()
}

// DCR returns DCRn of the model.
func (h *Host) DCR(n int) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dcr[n]
}

// Reload simulates a module reload: the hwmon device gets a new number
// (same device path); src changes the srcversion when not "".
func (h *Host) Reload(src string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = os.Remove(h.path(h.HwmonRel()))
	_ = os.RemoveAll(h.path(h.devRel()))
	h.hwmonN++
	for ch := 1; ch <= 4; ch++ {
		h.enable[ch], h.pwm[ch] = 2, 255
	}
	h.mkDriverHwmon(true)
	if src != "" {
		h.Put("sys/module/"+n5driver.ModuleName+"/srcversion", src+"\n")
	}
}

// SetTemp sets an independent sensor ("tctl", "nvme", "hdd") in degrees C;
// NaN-like "" removes the input file.
func (h *Host) SetTemp(which string, milli string) {
	rel := map[string]string{"tctl": "sys/devices/pci0000:00/0000:00:18.3/hwmon/hwmon0/temp1_input",
		"nvme": "sys/devices/pci0000:00/0000:01:00.0/nvme/nvme0/hwmon1/temp1_input",
		"hdd":  "sys/devices/pci0000:00/0000:02:00.0/ata1/host0/target0:0:0/0:0:0:0/hwmon/hwmon2/temp1_input"}[which]
	if milli == "" {
		_ = os.Remove(h.path(rel))
		return
	}
	h.Put(rel, milli+"\n")
}

// AddProcess adds /proc/<pid> with comm and argv (NUL-separated cmdline).
func (h *Host) AddProcess(pid int, comm string, argv ...string) {
	d := fmt.Sprintf("proc/%d", pid)
	h.Put(d+"/comm", comm+"\n")
	h.Put(d+"/cmdline", strings.Join(argv, "\x00")+"\x00")
	h.Put(d+"/stat", fmt.Sprintf("%d (%s) S 1 %d %d 0 -1", pid, comm, pid, pid))
	if err := os.MkdirAll(h.path(d+"/fd"), 0o755); err != nil {
		h.t.Fatal(err)
	}
}

// AddFD adds /proc/<pid>/fd/<n> -> target.
func (h *Host) AddFD(pid, n int, target string) {
	h.link(fmt.Sprintf("proc/%d/fd/%d", pid, n), target)
}

// EnableValue and PWMValue are the driver cache of channel ch.
func (h *Host) EnableValue(ch int) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enable[ch]
}

func (h *Host) PWMValue(ch int) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pwm[ch]
}

// ForeignWrite changes the pwm cache like `echo V > pwmN` of another
// program (bypassing n5-ectest).
func (h *Host) ForeignWrite(ch int, attr string, v int) {
	h.Put(h.devRel()+"/"+attr, strconv.Itoa(v)+"\n")
	h.mu.Lock()
	if strings.HasSuffix(attr, "_enable") {
		h.enable[ch] = v
	} else {
		h.pwm[ch] = v
	}
	h.mu.Unlock()
}
