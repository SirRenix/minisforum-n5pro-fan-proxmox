package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

func put(t *testing.T, p, v string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollect(t *testing.T) {
	root := t.TempDir()
	r := Roots{Sys: filepath.Join(root, "sys"), Proc: filepath.Join(root, "proc"), Etc: filepath.Join(root, "etc"), Run: filepath.Join(root, "run")}
	for k, v := range map[string]string{
		"product_name": "N5 PRO", "board_name": "F8NAA", "board_version": "1.0", "sys_vendor": "Micro Computer (HK) Tech Limited",
		"bios_version": "1.05", "bios_date": "03/31/2026", "bios_release": "1.5", "ec_firmware_release": "0.14",
		"product_serial": "SECRET123", "product_uuid": "11111111-2222-3333-4444-555555555555",
	} {
		put(t, filepath.Join(r.Sys, "class", "dmi", "id", k), v+"\n")
	}
	put(t, filepath.Join(r.Proc, "sys", "kernel", "osrelease"), "7.0.14-20-pve\n")
	put(t, filepath.Join(r.Proc, "sys", "kernel", "version"), "#1 SMP PREEMPT_DYNAMIC PMX 7.0.14-20\n")
	put(t, filepath.Join(r.Proc, "sys", "kernel", "tainted"), "12288\n")
	put(t, filepath.Join(r.Proc, "sys", "kernel", "random", "boot_id"), "0f0e0d0c-0b0a-0908-0706-050403020100\n")
	put(t, filepath.Join(r.Proc, "uptime"), "1234.56 9999.00\n")
	put(t, filepath.Join(r.Proc, "1", "environ"), "PATH=/bin\x00HOME=/\x00")
	put(t, filepath.Join(r.Etc, "os-release"), "PRETTY_NAME=\"Debian\"\nID=debian\nVERSION_ID=\"13\"\n")
	put(t, filepath.Join(r.Sys, "kernel", "security", "lockdown"), "[none] integrity confidentiality\n")
	mod := filepath.Join(r.Sys, "module", n5driver.ModuleName)
	put(t, filepath.Join(mod, "version"), "0.2.1+sr1~rc1\n")
	put(t, filepath.Join(mod, "srcversion"), "ABC\n")
	put(t, filepath.Join(mod, "parameters", "debug_regs"), "Y\n")
	put(t, n5driver.DCRPath(r.Sys), "1800: 00 ff 00 55 4a 57 57 8c 00 00 00 06 00 00 00 00\n")
	id := Collect(r, Options{DpkgQuery: func(p string) string {
		if p != n5driver.PackageName {
			t.Errorf("dpkg-query for %q", p)
		}
		return "0.2.1-sr1~rc1"
	}})
	if id.DMI.Product != "N5 PRO" || id.DMI.BIOSRelease != "1.5" || id.Kernel != "7.0.14-20-pve" || !strings.Contains(id.KernelVersion, "PMX") {
		t.Fatalf("identity %+v", id)
	}
	if id.UptimeS != 1234.56 || id.BootID == "" || id.KernelTaint != "12288" || id.DKMSPackage != "0.2.1-sr1~rc1" {
		t.Fatalf("identity %+v", id)
	}
	if id.OS.ID != "debian" || id.OS.VersionID != "13" || id.Lockdown != "none" || id.Container != "" {
		t.Fatalf("os/lockdown/container %+v %q %q", id.OS, id.Lockdown, id.Container)
	}
	if id.DCR.Channels["pwm3"] != 0x57 || id.HWStatus.Entry == nil {
		t.Fatalf("dcr %+v status %+v", id.DCR, id.HWStatus)
	}
	// serials and the UUID are never read
	b := strings.Join([]string{id.DMI.Product, id.DMI.Board, id.DMI.BoardVersion, id.DMI.SysVendor}, " ")
	if strings.Contains(b, "SECRET") {
		t.Fatal("serial read")
	}
	// container markers
	put(t, filepath.Join(r.Proc, "1", "environ"), "container=lxc\x00")
	if c, _ := DetectContainer(r.Proc, r.Run); c != "lxc" {
		t.Fatalf("lxc: %q", c)
	}
	put(t, filepath.Join(r.Run, "systemd", "container"), "docker\n")
	if c, _ := DetectContainer(r.Proc, r.Run); c != "docker" {
		t.Fatalf("run file: %q", c)
	}
	if c, ev := DetectContainer(filepath.Join(root, "none"), filepath.Join(root, "none")); c != "unknown" || ev == "" {
		t.Fatalf("unreadable environ: %q %q", c, ev)
	}
	put(t, filepath.Join(r.Sys, "kernel", "security", "lockdown"), "none [integrity] confidentiality\n")
	if Lockdown(r.Sys) != "integrity" {
		t.Fatal("lockdown integrity")
	}
}

func TestRedact(t *testing.T) {
	r := NewRedactor("", "node1.example.lan")
	cases := map[string]string{
		"r8169 0000:02:00.0 eth0: RTL8125B, 02:00:00:00:00:01, XID 641, IRQ 61":  "r8169 0000:02:00.0 eth0: RTL8125B, [mac], XID 641, IRQ 61",
		"vmbr0: port 1 entered forwarding state 192.0.2.20 via 10.0.0.1":         "vmbr0: port 1 entered forwarding state [ipv4] via [ipv4]",
		"link-local fe80::5a47:caff:fe12:3456%vmbr0 and 2001:db8:0:0:0:0:0:1 ok": "link-local [ipv6] and [ipv6] ok",
		"host node1.example.lan and node1 said hello":                            "host n5host and n5host said hello",
		"nvme0: Serial Number: S6XYZ123456 model":                                "nvme0: Serial Number: [serial] model",
		"log in /home/user/n5/out.txt":                                           "log in /home/[user]/n5/out.txt",
		"mail via /usr/sbin/sendmail to admin.x+n5@mail.example.org":             "mail via /usr/sbin/sendmail to [mail]",
		"getty@tty1.service and n5-ectest-log@run1.service failed":               "getty@tty1.service and n5-ectest-log@run1.service failed",
		// must stay: times, hex rows, PCI addresses, versions, a boot id
		"17:20:31 1800: 00 ff 00 55 0000:02:00.0 7.0.14-20-pve 0f0e0d0c-0b0a-0908-0706-050403020100": "17:20:31 1800: 00 ff 00 55 0000:02:00.0 7.0.14-20-pve 0f0e0d0c-0b0a-0908-0706-050403020100",
	}
	for in, want := range cases {
		if got := r.String(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
	// a Proxmox host named "pve": the kernel release keeps its suffix
	p := NewRedactor("", "pve")
	p.ProtectIdentity(Identity{Kernel: "7.0.14-20-pve", KernelVersion: "#1 SMP PMX 7.0.14-20"})
	if got := p.String(`"kernel": "7.0.14-20-pve", "host": "pve", log of pve`); got != `"kernel": "7.0.14-20-pve", "host": "n5host", log of n5host` {
		t.Fatalf("protected: %q", got)
	}
}

// Without root debugfs and /proc/1/environ cannot be read: the identity
// says "run as root" instead of "no dcr" / a bare errno. Runs only as a
// normal user (root reads everything).
func TestNotRootMessages(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root: permission errors cannot occur")
	}
	sys, proc := t.TempDir(), t.TempDir()
	debug := filepath.Join(sys, "kernel", "debug")
	if err := os.MkdirAll(filepath.Join(debug, n5driver.ModuleName), 0o755); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(debug, n5driver.ModuleName, "dcr"), "1800: 00 ff 55 4a 8c 8c 8c 00\n")
	if err := os.Chmod(debug, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(debug, 0o755)
	if d := ReadDCRInfo(sys); d.Dump != "" || !strings.Contains(d.Error, "without root") {
		t.Fatalf("dcr: %+v", d)
	}
	put(t, filepath.Join(proc, "1", "environ"), "container=lxc\x00")
	if err := os.Chmod(filepath.Join(proc, "1", "environ"), 0); err != nil {
		t.Fatal(err)
	}
	if name, ev := DetectContainer(proc, t.TempDir()); name != "unknown" || !strings.Contains(ev, "run as root") {
		t.Fatalf("container %q %q", name, ev)
	}
}
