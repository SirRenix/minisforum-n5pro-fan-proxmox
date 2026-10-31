package regulator_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/hwread"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/regulator"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/writerlock"
)

func stubUnits(t *testing.T, states map[string]string, err error) {
	old := regulator.Systemctl
	regulator.Systemctl = func(u string) (string, error) {
		if err != nil {
			return "", err
		}
		if s, ok := states[u]; ok {
			return s, nil
		}
		return "inactive", nil
	}
	t.Cleanup(func() { regulator.Systemctl = old })
}

func TestUnits(t *testing.T) {
	stubUnits(t, map[string]string{"fancontrol": "active", "n5-fangov": "activating"}, nil)
	run, note := regulator.Units()
	if len(run) != 2 || note != "" || !strings.Contains(strings.Join(run, ","), "unit n5-fangov activating") {
		t.Fatalf("units %v %q", run, note)
	}
	if regulator.FangovState() != "activating" || !regulator.UnitRunning("reloading") || regulator.UnitRunning("failed") {
		t.Fatal("states")
	}
	stubUnits(t, nil, errors.New("exec: systemctl not found"))
	if run, note := regulator.Units(); len(run) != 0 || !strings.Contains(note, "process check only") {
		t.Fatalf("no systemctl: %v %q", run, note)
	}
}

func TestProcesses(t *testing.T) {
	h := fake.New(t, fake.Reference())
	c := &regulator.Checker{ProcRoot: sysroot.Proc(), Self: map[int]bool{4000: true}}
	h.AddProcess(100, "bash", "/bin/bash")
	h.AddProcess(101, "vim", "vim", "/etc/fancontrol.conf") // not a hit: substring only
	h.AddProcess(4000, "fancontrol", "fancontrol")          // own pid: ignored
	if hits := c.Processes(); len(hits) != 0 {
		t.Fatalf("false hits %v", hits)
	}
	h.AddProcess(102, "fancontrol", "/bin/sh", "/usr/sbin/fancontrol")
	h.AddProcess(103, "python3", "/usr/bin/fan2go") // argv[0] basename
	h.AddProcess(104, "sensord", "/usr/sbin/sensord")
	h.AddFD(104, 5, "/sys/devices/platform/x/hwmon/hwmon14/pwm3")
	// the f8a scripts write pwm4/pwm1/pwm2: by shebang (comm) and `bash x.sh`
	h.AddProcess(105, "f8a-pcie.sh", "/bin/bash", "/root/n5pro-ec/tools/f8a/f8a-pcie.sh")
	h.AddProcess(106, "bash", "bash", "tools/f8a/f8a-stress.sh", "load")
	h.AddProcess(107, "bash", "bash", "tools/f8a/f8a-snapshot.sh", "x") // read-only: no hit
	hits := strings.Join(c.Processes(), "; ")
	if strings.Contains(hits, "f8a-snapshot.sh") {
		t.Errorf("read-only script counted: %s", hits)
	}
	for _, want := range []string{"process fancontrol (pid 102)", "process fan2go (pid 103)", "pid 104 (sensord) holds /sys/devices/platform/x/hwmon/hwmon14/pwm3 open",
		"process f8a-pcie.sh (pid 105)", "process bash f8a-stress.sh (pid 106)"} {
		if !strings.Contains(hits, want) {
			t.Errorf("missing %q in %s", want, hits)
		}
	}
}

func TestExpectationAndLock(t *testing.T) {
	stubUnits(t, nil, nil)
	h := fake.New(t, fake.Reference())
	h.Install()
	lock, err := writerlock.Acquire(sysroot.Run(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	e := regulator.NewExpectation(func() string { return hwread.DriverHwmon(sysroot.Sys()) })
	c := &regulator.Checker{ProcRoot: sysroot.Proc(), Lock: lock, Expect: e}
	if hits := c.Check(); len(hits) != 0 {
		t.Fatalf("idle: %v", hits)
	}
	dev, _, _ := pwmw.Resolve(sysroot.Sys())
	w := &pwmw.Writer{SysRoot: sysroot.Sys(), Want: dev, Before: e.Expect, After: e.After}
	// own writes are never foreign: manual (driver sets 255), duty, auto
	for _, f := range []func() error{func() error { return w.EnterManual(3) }, func() error { return w.SetDuty(3, 179) }, func() error { return w.Auto(3) }} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
		if hits := c.Check(); len(hits) != 0 {
			t.Fatalf("own write seen as foreign: %v", hits)
		}
	}
	// `echo 1 > pwm2_enable` by another program
	h.ForeignWrite(2, "pwm2_enable", 1)
	if hits := strings.Join(c.Check(), "; "); !strings.Contains(hits, "pwm2_enable is 1, expected 2") {
		t.Fatalf("foreign write: %s", hits)
	}
	e.Refresh(2)
	// postrm deletes /run/n5-fangov
	if err := os.RemoveAll(sysroot.Run() + "/n5-fangov"); err != nil {
		t.Fatal(err)
	}
	if hits := strings.Join(c.Check(), "; "); !strings.Contains(hits, "removed or replaced") {
		t.Fatalf("lock gone: %s", hits)
	}
}
