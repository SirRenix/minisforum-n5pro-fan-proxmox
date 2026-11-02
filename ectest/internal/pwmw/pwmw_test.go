package pwmw_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
)

func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: no panic", what)
		}
	}()
	f()
}

func TestWriteRules(t *testing.T) {
	h := fake.New(t, fake.Reference())
	h.Install()
	dev, dir, err := pwmw.Resolve(sysroot.Sys())
	if err != nil || !strings.HasSuffix(dir, "hwmon14") || !strings.HasSuffix(dev.DevicePath, fake.SysDevice) || dev.SrcVersion == "" {
		t.Fatalf("resolve: %+v %s %v", dev, dir, err)
	}
	w := &pwmw.Writer{SysRoot: sysroot.Sys(), Want: dev}
	var before []string
	w.Before = func(ch int, a pwmw.Attr, v int) { before = append(before, a.Name(ch)) }
	// below 100 / outside {1,2}: panic before anything is written
	mustPanic(t, "pwm3=99", func() { _ = w.SetDuty(3, 99) })
	mustPanic(t, "pwm3=0", func() { _ = w.SetDuty(3, 0) })
	mustPanic(t, "enable=0", func() { _ = w.Write(3, pwmw.Enable, 0) })
	mustPanic(t, "enable=3", func() { _ = w.Write(3, pwmw.Enable, 3) })
	mustPanic(t, "pwm5", func() { _ = w.Auto(5) })
	if len(h.AllWrites) != 0 || len(before) != 0 {
		t.Fatalf("written before the panic: %v %v", h.AllWrites, before)
	}
	if err := w.SetDuty(3, 256); err == nil {
		t.Fatal("256 accepted")
	}
	if err := w.RestoreTo(3, "140"); err != nil {
		t.Fatal(err)
	}
	if err := w.RestoreTo(3, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := w.RestoreTo(3, "60"); err == nil {
		t.Fatal("restore to 60 accepted")
	}
	if got := strings.Join(h.Writes, " "); got != "pwm3_enable=1 pwm3=140 pwm3_enable=2" {
		t.Fatalf("writes %q", got)
	}
	if len(before) != 3 {
		t.Fatalf("Before calls %v", before)
	}
}

// amdgpu has its own pwm1; a reload renumbers the hwmon device (written
// again at the new number); a rebuilt module (other srcversion) is never
// written.
func TestDeviceIdentity(t *testing.T) {
	h := fake.New(t, fake.Reference())
	h.Install()
	dev, _, err := pwmw.Resolve(sysroot.Sys())
	if err != nil {
		t.Fatal(err)
	}
	w := &pwmw.Writer{SysRoot: sysroot.Sys(), Want: dev}
	if err := w.EnterManual(1); err != nil {
		t.Fatal(err)
	}
	h.Reload("")
	if err := w.SetDuty(1, 200); err != nil {
		t.Fatalf("after renumbering: %v", err)
	}
	if h.Get(h.HwmonRel()+"/pwm1") != "200" {
		t.Fatalf("not written at the new hwmon number %s", h.HwmonRel())
	}
	h.Reload("0000NEWSRCVERSION")
	n := len(h.AllWrites)
	if err := w.Auto(1); !errors.Is(err, pwmw.ErrDeviceChanged) {
		t.Fatalf("other srcversion: %v", err)
	}
	if len(h.AllWrites) != n {
		t.Fatal("written to a changed device")
	}
	for _, x := range h.AllWrites {
		if strings.Contains(x, "c4:00.0") {
			t.Fatalf("amdgpu written: %s", x)
		}
	}
	// no driver device at all
	h2 := fake.New(t, func() fake.Options { o := fake.Reference(); o.NoDriver = true; return o }())
	_ = h2
	if _, _, err := pwmw.Resolve(sysroot.Sys()); !errors.Is(err, pwmw.ErrNoDevice) {
		t.Fatalf("no driver: %v", err)
	}
}
