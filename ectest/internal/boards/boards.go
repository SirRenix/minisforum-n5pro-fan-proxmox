// Package boards holds n5-ectest's own board and driver tables (design
// §4). The tool does not rely on the driver's allowlist alone: upstream
// 0.2.1 with force=1 experimental_write=1 opens PMC2 writes on any board,
// and docs/DMI-PATCH.md lets users add boards to the driver.
package boards

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/hwstatus"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// ReadOnly reports whether read-only commands that talk to the driver
// (snapshot mode B, log) run on this board: the N5 family. Anything else
// gets identify, diff and report only.
func ReadOnly(d hwstatus.Identity) (bool, string) {
	switch {
	case d.Product == "N5 PRO" && (d.Board == "F8NAA" || d.Board == "F8NAB"):
	case d.Product == "N5" && d.Board == "F8NAA":
	case (d.Product == "N5A" || d.Product == "N5 AIR") && d.Board == "F8NAB":
	default:
		return false, fmt.Sprintf("DMI %q/%q is not an N5-family board (N5 PRO F8NAA/F8NAB, N5 F8NAA, N5A/N5 AIR F8NAB): only identify, diff and report run here", d.Product, d.Board)
	}
	return true, ""
}

// WriteGate checks the write table of design §4 for channel ch; every
// condition must hold. It returns "" when writing is allowed, else the
// reason (gate failure: the bundle records it, nothing is written). The
// pwm-not-exposed case is returned separately (pwmReason).
func WriteGate(id identity.Identity, ch int) (boardReason, pwmReason string) {
	d := id.DMI
	var why []string
	if d.Product != "N5 PRO" || d.Board != "F8NAA" {
		if ok, _ := ReadOnly(d); ok {
			why = append(why, fmt.Sprintf("DMI %q/%q is read-only for n5-ectest (writing tests only on N5 PRO/F8NAA)", d.Product, d.Board))
		} else {
			why = append(why, fmt.Sprintf("DMI %q/%q is outside the TESTSPEC scope (writing tests only on N5 PRO/F8NAA)", d.Product, d.Board))
		}
	}
	if d.SysVendor != n5driver.SysVendor {
		why = append(why, fmt.Sprintf("DMI sys_vendor %q is not the measured %q", d.SysVendor, n5driver.SysVendor))
	}
	dr := id.Driver
	switch {
	case !dr.Loaded:
		why = append(why, "driver "+n5driver.ModuleName+" not loaded")
	case !strings.Contains(dr.Version, "+sr1"):
		why = append(why, fmt.Sprintf("driver version %q is not a 0.2.1+sr1 build", dr.Version))
	}
	switch f := dr.Param("force"); {
	case dr.Loaded && f == "":
		why = append(why, "module parameter force not readable")
	case dr.Forced():
		why = append(why, "module parameter force=Y (a force-loaded driver never runs writing tests)")
	}
	if id.DCR.Dump == "" {
		why = append(why, "duty registers not readable ("+id.DCR.Error+")")
	}
	switch id.Container {
	case "":
	case "unknown":
		why = append(why, "container state unknown ("+id.ContainerEvidence+")")
	default:
		why = append(why, "running in a container ("+id.Container+", "+id.ContainerEvidence+")")
	}
	boardReason = strings.Join(why, "; ")
	if !PWMExposed(dr, ch) {
		pwmReason = fmt.Sprintf("pwm%d/pwm%d_enable not present", ch, ch)
		if f := dr.Findings(); len(f) > 0 {
			pwmReason += " (" + strings.Join(f, "; ") + ")"
		} else if dr.HwmonPath == "" {
			pwmReason += " (no hwmon device of " + n5driver.ModuleName + ")"
		}
	}
	return boardReason, pwmReason
}

// PWMExposed reports pwmN and pwmN_enable in the driver's hwmon device.
func PWMExposed(dr n5driver.State, ch int) bool {
	if dr.HwmonPath == "" {
		return false
	}
	for _, a := range []string{fmt.Sprintf("pwm%d", ch), fmt.Sprintf("pwm%d_enable", ch)} {
		if _, err := os.Stat(filepath.Join(dr.HwmonPath, a)); err != nil {
			return false
		}
	}
	return true
}
