package boards_test

import (
	"strings"
	"testing"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/boards"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/hwstatus"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
)

func collect() identity.Identity {
	return identity.Collect(sysroot.Roots(), identity.Options{DpkgQuery: func(string) string { return "" }})
}

func TestWriteGate(t *testing.T) {
	fake.New(t, fake.Reference())
	if b, p := boards.WriteGate(collect(), 3); b != "" || p != "" {
		t.Fatalf("reference: %q %q", b, p)
	}
	cases := []struct {
		name string
		mod  func(*fake.Options)
		want string
		pwm  bool
	}{
		{"F8NAB", func(o *fake.Options) { o.Board = "F8NAB" }, "read-only for n5-ectest", false},
		{"N5", func(o *fake.Options) { o.Product = "N5" }, "read-only for n5-ectest", false},
		{"other", func(o *fake.Options) { o.Product, o.Board = "X1", "Y" }, "outside the TESTSPEC scope", false},
		{"vendor", func(o *fake.Options) { o.Vendor = "Other Vendor" }, "sys_vendor", false},
		{"upstream", func(o *fake.Options) { o.DriverVersion = "0.2.0" }, "not a 0.2.1+sr1 build", false},
		{"force", func(o *fake.Options) { o.Force = "Y" }, "force=Y", false},
		{"no dcr", func(o *fake.Options) { o.DebugRegs = false }, "duty registers not readable", false},
		{"pwm hidden", func(o *fake.Options) { o.PWMExposed = false }, "", true},
		{"no driver", func(o *fake.Options) { o.NoDriver = true }, "not loaded", true},
	}
	for _, c := range cases {
		o := fake.Reference()
		c.mod(&o)
		h := fake.New(t, o)
		_ = h
		b, p := boards.WriteGate(collect(), 3)
		if c.want != "" && !strings.Contains(b, c.want) {
			t.Errorf("%s: board %q", c.name, b)
		}
		if c.pwm != (p != "") {
			t.Errorf("%s: pwm %q", c.name, p)
		}
	}
	// container
	h := fake.New(t, fake.Reference())
	h.Put("proc/1/environ", "container=lxc\x00")
	if b, _ := boards.WriteGate(collect(), 1); !strings.Contains(b, "running in a container (lxc") {
		t.Fatalf("container: %q", b)
	}
}

func TestReadOnly(t *testing.T) {
	for _, c := range []struct {
		p, b string
		ok   bool
	}{{"N5 PRO", "F8NAA", true}, {"N5 PRO", "F8NAB", true}, {"N5", "F8NAA", true}, {"N5A", "F8NAB", true}, {"N5 AIR", "F8NAB", true}, {"N5", "F8NAB", false}, {"MS-01", "X", false}} {
		if ok, _ := boards.ReadOnly(hwstatus.Identity{Product: c.p, Board: c.b}); ok != c.ok {
			t.Errorf("%s/%s: %v", c.p, c.b, ok)
		}
	}
}
