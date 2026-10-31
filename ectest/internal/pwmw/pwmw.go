// Package pwmw is the only path through which n5-ectest writes a pwm
// attribute: every write of every command, of the abort path, of the
// guardian and of `restore` goes through Writer.Write (design §7).
//
//   - pwmN below 100 panics before the system call (TESTSPEC §4: a duty
//     below 100 is never written); pwmN_enable only 1 (manual) or 2 (EC
//     automatic), anything else panics.
//   - Before every write the driver's hwmon device is resolved again and
//     compared with the identity recorded at the start (hwmon name, real
//     device path, module srcversion): amdgpu has its own pwm1, and the hwmon
//     number changes on a module reload. A different device writes nothing.
package pwmw

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// MinDuty is the lowest duty ever written (TESTSPEC §4).
const MinDuty = 100

// WriteTimeout bounds one sysfs write (an EC that does not answer).
var WriteTimeout = 15 * time.Second

// Attr is the attribute of a channel.
type Attr int

const (
	Duty   Attr = iota // pwmN
	Enable             // pwmN_enable
)

// Name is the sysfs file name of attribute a of channel ch.
func (a Attr) Name(ch int) string {
	if a == Enable {
		return fmt.Sprintf("pwm%d_enable", ch)
	}
	return fmt.Sprintf("pwm%d", ch)
}

// Device is the identity of the driver's hwmon device.
type Device struct {
	Name       string `json:"hwmon_name"`
	DevicePath string `json:"device_path"` // real path of the device, not hwmonN
	SrcVersion string `json:"srcversion"`
}

func (d Device) String() string {
	return fmt.Sprintf("%s at %s (srcversion %s)", d.Name, d.DevicePath, orDash(d.SrcVersion))
}

// Errors of Resolve and Write.
var (
	ErrNoDevice      = errors.New("no hwmon device of " + n5driver.ModuleName)
	ErrAmbiguous     = errors.New("more than one hwmon device of " + n5driver.ModuleName)
	ErrDeviceChanged = errors.New("driver device changed since the start (module reloaded or another device): nothing written")
	ErrNoAttr        = errors.New("pwm attribute not exposed by the driver")
)

// Resolve finds the driver's hwmon device below sysRoot and returns its
// identity and its current hwmonN directory (the real path below
// /sys/devices, so a write goes to the device that was checked).
func Resolve(sysRoot string) (Device, string, error) {
	cls := filepath.Join(sysRoot, "class", "hwmon")
	ents, err := os.ReadDir(cls)
	if err != nil {
		return Device{}, "", fmt.Errorf("%w: %v", ErrNoDevice, err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var found []string
	for _, n := range names {
		d := filepath.Join(cls, n)
		if s, _ := sysroot.ReadTimeout(filepath.Join(d, "name"), sysroot.DefaultTimeout); s == n5driver.ModuleName {
			found = append(found, d)
		}
	}
	switch len(found) {
	case 0:
		return Device{}, "", ErrNoDevice
	case 1:
	default:
		return Device{}, "", fmt.Errorf("%w: %s", ErrAmbiguous, strings.Join(found, ", "))
	}
	real, err := filepath.EvalSymlinks(found[0])
	if err != nil {
		return Device{}, "", fmt.Errorf("%w: %v", ErrNoDevice, err)
	}
	dev := real
	if filepath.Base(filepath.Dir(real)) == "hwmon" {
		dev = filepath.Dir(filepath.Dir(real))
	}
	src, _ := sysroot.ReadTimeout(filepath.Join(sysRoot, "module", n5driver.ModuleName, "srcversion"), sysroot.DefaultTimeout)
	return Device{Name: n5driver.ModuleName, DevicePath: dev, SrcVersion: src}, real, nil
}

// Recorder, when set (tests), is called after every successful write with
// the absolute path and the value.
var Recorder func(path, value string)

// Writer writes pwm attributes of one driver device.
type Writer struct {
	SysRoot string
	Want    Device
	// Before is called with the value before the write (the regulator check
	// sets its expected value there, so the own write is never foreign).
	Before func(ch int, a Attr, v int)
	// After is called after the write with its error.
	After func(ch int, a Attr, v int, err error)
}

// Write writes v to attribute a of channel ch.
func (w *Writer) Write(ch int, a Attr, v int) error {
	if ch < 1 || ch > 4 {
		panic(fmt.Sprintf("pwmw: channel pwm%d out of range", ch))
	}
	switch a {
	case Duty:
		if v < MinDuty {
			panic(fmt.Sprintf("pwmw: refusing pwm%d=%d: a duty below %d is never written (TESTSPEC §4)", ch, v, MinDuty))
		}
		if v > 255 {
			return fmt.Errorf("pwmw: pwm%d=%d out of range", ch, v)
		}
	case Enable:
		if v != 1 && v != 2 {
			panic(fmt.Sprintf("pwmw: refusing pwm%d_enable=%d: only 1 (manual) or 2 (EC automatic)", ch, v))
		}
	default:
		panic("pwmw: unknown attribute")
	}
	dev, dir, err := Resolve(w.SysRoot)
	if err != nil {
		return err
	}
	if dev != w.Want {
		return fmt.Errorf("%w: now %s, at the start %s", ErrDeviceChanged, dev, w.Want)
	}
	p := filepath.Join(dir, a.Name(ch))
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("%w: %s", ErrNoAttr, p)
	}
	if w.Before != nil {
		w.Before(ch, a, v)
	}
	err = writeAttr(p, strconv.Itoa(v))
	if w.After != nil {
		w.After(ch, a, v, err)
	}
	if err == nil && Recorder != nil {
		Recorder(p, strconv.Itoa(v))
	}
	return err
}

func writeAttr(p, val string) error {
	done := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			done <- err
			return
		}
		_, err = f.WriteString(val + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		done <- err
	}()
	t := time.NewTimer(WriteTimeout)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return fmt.Errorf("write %s=%s: no answer within %s", p, val, WriteTimeout)
	}
}

// EnterManual sets pwmN_enable=1 (the driver writes 255 first).
func (w *Writer) EnterManual(ch int) error { return w.Write(ch, Enable, 1) }

// SetDuty writes pwmN (the channel must be manual).
func (w *Writer) SetDuty(ch, d int) error { return w.Write(ch, Duty, d) }

// Auto sends the EC automatic command (pwmN_enable=2).
func (w *Writer) Auto(ch int) error { return w.Write(ch, Enable, 2) }

// RestoreTo puts channel ch into value: "auto" or a duty (>= 100).
func (w *Writer) RestoreTo(ch int, value string) error {
	if value == "auto" {
		return w.Auto(ch)
	}
	d, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("pwmw: restore value %q is not auto or a duty", value)
	}
	if d < MinDuty || d > 255 {
		return fmt.Errorf("pwmw: restore value %d outside %d..255: not written", d, MinDuty)
	}
	if err := w.EnterManual(ch); err != nil {
		return err
	}
	return w.SetDuty(ch, d)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
