// Package hwread reads the values a sample needs, read-only: the EC
// driver's hwmon device (fan1-4, temp1-4 by label, the pwm cache) and the
// independent limit sensors (k10temp Tctl, NVMe max, drivetemp max), plus
// the load average. Every read has a timeout (sysroot.ReadTimeout); a read
// error or timeout of a driver attribute counts in Errors.
package hwread

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// Reading is one read of everything.
type Reading struct {
	Fan     map[int]int    // fan1..fan4 rpm, -1 unreadable
	Label   map[int]string // fanN_label
	EC      map[string]float64
	Tctl    float64
	NVMeMax float64
	HDDMax  float64
	Load1   float64
	PWM     map[int]int // driver cache pwmN, -1 not exposed/unreadable
	Enable  map[int]int // driver cache pwmN_enable
	Errors  int         // driver attribute reads that failed or timed out
	Hwmon   string      // the driver's hwmon directory ("" = none)
}

// EC temperature ids by hwmon label.
var ecLabels = map[string]string{"CPU Temp": "cpu", "System Temp": "system", "Board Temp": "board", "Ambient Temp": "ambient"}

// OnlineCPUs is the number of online CPUs below sysRoot (G3), 0 when it
// cannot be read.
func OnlineCPUs(sysRoot string) int {
	return spec.ParseCPUList(sysroot.Read(filepath.Join(sysRoot, "devices", "system", "cpu", "online")))
}

// DriverHwmon returns <sys>/class/hwmon/hwmonN of the EC driver, "" if none.
func DriverHwmon(sysRoot string) string {
	for _, d := range hwmonDirs(sysRoot) {
		if sysroot.Read(filepath.Join(d, "name")) == n5driver.ModuleName {
			return d
		}
	}
	return ""
}

func hwmonDirs(sysRoot string) []string {
	cls := filepath.Join(sysRoot, "class", "hwmon")
	ents, err := os.ReadDir(cls)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		out = append(out, filepath.Join(cls, e.Name()))
	}
	sort.Strings(out)
	return out
}

func readInt(p string) (int, error) {
	s, err := sysroot.ReadTimeout(p, sysroot.DefaultTimeout)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

// maxTemp is the max over temp*_input of every hwmon named name, in C.
func maxTemp(sysRoot, name string) float64 {
	m := math.NaN()
	for _, d := range hwmonDirs(sysRoot) {
		if sysroot.Read(filepath.Join(d, "name")) != name {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(d, "temp*_input"))
		for _, f := range files {
			if v, err := readInt(f); err == nil {
				c := float64(v) / 1000
				if math.IsNaN(m) || c > m {
					m = c
				}
			}
		}
	}
	return m
}

// Tctl is k10temp's Tctl in C (NaN = none).
func Tctl(sysRoot string) float64 {
	for _, d := range hwmonDirs(sysRoot) {
		if sysroot.Read(filepath.Join(d, "name")) != "k10temp" {
			continue
		}
		labels, _ := filepath.Glob(filepath.Join(d, "temp*_label"))
		for _, l := range labels {
			if sysroot.Read(l) == "Tctl" {
				if v, err := readInt(strings.TrimSuffix(l, "_label") + "_input"); err == nil {
					return float64(v) / 1000
				}
			}
		}
	}
	return math.NaN()
}

// Load1 is the 1-minute load average (NaN = unreadable).
func Load1(procRoot string) float64 {
	if f := strings.Fields(sysroot.Read(filepath.Join(procRoot, "loadavg"))); len(f) > 0 {
		if x, err := strconv.ParseFloat(f[0], 64); err == nil {
			return x
		}
	}
	return math.NaN()
}

// Read reads everything once.
func Read(sysRoot, procRoot string) Reading {
	r := Reading{Fan: map[int]int{}, Label: map[int]string{}, EC: map[string]float64{}, PWM: map[int]int{}, Enable: map[int]int{},
		Tctl: Tctl(sysRoot), NVMeMax: maxTemp(sysRoot, "nvme"), HDDMax: maxTemp(sysRoot, "drivetemp"), Load1: Load1(procRoot)}
	for _, id := range ecLabels {
		r.EC[id] = math.NaN()
	}
	r.Hwmon = DriverHwmon(sysRoot)
	for n := 1; n <= 4; n++ {
		r.Fan[n], r.PWM[n], r.Enable[n] = -1, -1, -1
	}
	if r.Hwmon == "" {
		return r
	}
	for n := 1; n <= 4; n++ {
		p := filepath.Join(r.Hwmon, fmt.Sprintf("fan%d_input", n))
		if _, err := os.Stat(p); err == nil {
			if v, err := readInt(p); err == nil {
				r.Fan[n] = v
			} else {
				r.Errors++
			}
		}
		r.Label[n] = sysroot.Read(filepath.Join(r.Hwmon, fmt.Sprintf("fan%d_label", n)))
		tp := filepath.Join(r.Hwmon, fmt.Sprintf("temp%d_input", n))
		if _, err := os.Stat(tp); err == nil {
			id, ok := ecLabels[sysroot.Read(filepath.Join(r.Hwmon, fmt.Sprintf("temp%d_label", n)))]
			if !ok {
				id = fmt.Sprintf("temp%d", n)
			}
			if v, err := readInt(tp); err == nil {
				r.EC[id] = float64(v) / 1000
			} else {
				r.Errors++
			}
		}
		for _, a := range []struct {
			name string
			dst  map[int]int
		}{{fmt.Sprintf("pwm%d", n), r.PWM}, {fmt.Sprintf("pwm%d_enable", n), r.Enable}} {
			p := filepath.Join(r.Hwmon, a.name)
			if _, err := os.Stat(p); err != nil {
				continue
			}
			if v, err := readInt(p); err == nil {
				a.dst[n] = v
			} else {
				r.Errors++
			}
		}
	}
	return r
}
