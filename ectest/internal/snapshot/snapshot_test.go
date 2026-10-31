package snapshot_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/snapshot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// ECRAM20260914 is the EC RAM image of testdata/ecram-20260914.txt.
func ECRAM20260914(t testing.TB) [256]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ecram-20260914.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var d [256]string
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) != 17 || !strings.HasSuffix(f[0], ":") {
			continue
		}
		base, err := strconv.ParseUint(strings.TrimSuffix(f[0], ":"), 16, 8)
		if err != nil {
			t.Fatal(err)
		}
		for i, v := range f[1:] {
			d[int(base)+i] = strings.ToLower(v)
			n++
		}
	}
	if n != 256 {
		t.Fatalf("image has %d bytes", n)
	}
	return d
}

// modeA renders a mode-A file the way f8a-snapshot.sh lays it out (EC RAM
// twice, I2EC 0x1800-0x182F, TACH lines, RESULT).
func modeA(ec [256]string, i2ec []byte, result string) string {
	var b strings.Builder
	b.WriteString("12:00:00 f8a-snapshot 0.2.0 label=x mode=A\n\n=== identity ===\ntool: f8a 0.2.0\n\n")
	b.WriteString(snapshot.ECRAMSection(1, ec))
	b.WriteString("\n")
	b.WriteString(snapshot.ECRAMSection(2, ec))
	b.WriteString("\n=== BEGIN I2EC ===\n")
	b.WriteString(snapshot.I2ECRows(0x1800, i2ec))
	b.WriteString("=== END I2EC ===\n\n=== TACH (n5_fan rpm) ===\nTACH1: 2003 RPM\nTACH2: 2236 RPM\nTACH3: 1656 RPM\n")
	b.WriteString(result + "\n")
	return b.String()
}

func i2ec20260914() []byte {
	d := make([]byte, 48)
	copy(d, []byte{0x00, 0xff, 0x00, 0x55, 0x4a, 0x57, 0x57, 0x8c, 0, 0, 0, 0x06})
	d[0x1e], d[0x1f], d[0x20], d[0x21], d[0x23], d[0x27], d[0x2b] = 0x38, 0x04, 0xef, 0x03, 0x02, 0x02, 0x48
	return d
}

// FormatFixtures writes the snapshot pairs the CI format job feeds to
// f8a-snapshot.sh --diff (N5ECTEST_FORMAT_OUT) with the expected exit codes.
func TestFormatFixtures(t *testing.T) {
	ec := ECRAM20260914(t)
	changed := ec
	changed[0x34] = "a7" // curve table B
	tempOnly := ec
	tempOnly[0x09] = "40"
	dir := os.Getenv("N5ECTEST_FORMAT_OUT")
	if dir == "" {
		dir = t.TempDir()
	}
	base := modeA(ec, i2ec20260914(), "RESULT: complete")
	files := map[string]string{
		"a.txt":        base,
		"same.txt":     modeA(tempOnly, i2ec20260914(), "RESULT: complete"),
		"changed.txt":  modeA(changed, i2ec20260914(), "RESULT: complete"),
		"modeB.txt":    "=== provenance ===\nmode: B\n=== BEGIN I2EC ===\n1800: 00 ff 00 55 4a 57 57 8c 00 00 00 06 00 00 00 00\n=== END I2EC ===\nRESULT: complete\n",
		"expected.txt": "a.txt same.txt 0\na.txt changed.txt 1\na.txt modeB.txt 2\n",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		a, b string
		rc   int
		want string
	}{
		{"a.txt", "same.txt", 0, "C1 curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81: identical"},
		{"a.txt", "changed.txt", 1, "0x30-0x38: CHANGED at 0x34"},
		{"a.txt", "modeB.txt", 2, "no complete EC RAM dump 1"},
	} {
		fa, _ := snapshot.ParsePath(filepath.Join(dir, c.a))
		fb, _ := snapshot.ParsePath(filepath.Join(dir, c.b))
		text, rc := snapshot.Diff(fa, fb)
		if rc != c.rc || !strings.Contains(text, c.want) {
			t.Errorf("%s vs %s: rc %d\n%s", c.a, c.b, rc, text)
		}
	}
	// n5-ectest only: an INCOMPLETE file or "??" in a curve table is exit 2
	inc := snapshot.Parse("inc", modeA(ec, i2ec20260914(), "RESULT: INCOMPLETE (n5_fan rpm failed)"))
	if _, rc := snapshot.Diff(snapshot.Parse("a", base), inc); rc != 2 {
		t.Fatalf("INCOMPLETE: rc %d", rc)
	}
	q := ec
	q[0x20] = "??"
	if txt, rc := snapshot.Diff(snapshot.Parse("a", base), snapshot.Parse("q", modeA(q, i2ec20260914(), "RESULT: complete"))); rc != 2 || !strings.Contains(txt, "??") {
		t.Fatalf("??: rc %d\n%s", rc, txt)
	}
}

// DCR golden test: DCR1..DCR5 are the bytes at I2EC 0x1803..0x1807, the
// same as f8a_dcr_byte LINE 3..7 (field index+1 of the "1800:" line).
func TestDCRGolden(t *testing.T) {
	line := "1800: 00 ff 00 55 4a 57 57 8c 00 00 00 06 00 00 00 00"
	d, err := n5driver.ParseDCR(line)
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(line)
	for n := 1; n <= 5; n++ {
		idx := n + 2
		want, _ := strconv.ParseUint(f[idx+1], 16, 8)
		if d.Reg(n) != int(want) {
			t.Errorf("DCR%d = %#x, f8a_dcr_byte %d = %s", n, d.Reg(n), idx, f[idx+1])
		}
	}
	if d.Reg(1) != 0x55 || d.Reg(2) != 0x4a || d.Reg(3) != 0x57 || d.Reg(4) != 0x57 || d.Reg(5) != 0x8c {
		t.Fatalf("DCR1-5 %s", d)
	}
	p := snapshot.Parse("x", "=== BEGIN I2EC ===\n"+snapshot.I2ECRows(0x1800, d[:])+"=== END I2EC ===\n")
	for n := 1; n <= 5; n++ {
		if p.I2EC[0x1802+n] != fmt.Sprintf("%02x", d.Reg(n)) {
			t.Errorf("I2EC 0x%04x = %s", 0x1802+n, p.I2EC[0x1802+n])
		}
	}
}

func TestModeB(t *testing.T) {
	h := fake.New(t, fake.Reference())
	id := identity.Collect(sysroot.Roots(), identity.Options{DpkgQuery: func(string) string { return "0.2.1-sr1~rc1" }})
	m := snapshot.TakeModeB(snapshot.Header{Tool: "n5-ectest", Version: "t", SpecVersion: "0.3", Label: "cold", Mode: "B", Boot: "cold", DateUTC: "x", FangovState: "inactive"},
		id, sysroot.Sys(), sysroot.Proc(), sysroot.Etc())
	if len(m.Incomplete) != 0 {
		t.Fatalf("incomplete: %v\n%s", m.Incomplete, m.Text)
	}
	for _, want := range []string{"=== BEGIN I2EC ===\n1800: 00 ff 00 55 4a 64 64 8c", "DCR1(CPU)=55 DCR2(SSD)=4a DCR3/4(HDD)=64/64 DCR5(PCIe)=8c",
		"TACH3: ", "fan1_input=", "(CPU Fan)", "pwm3=255 pwm3_enable=2 (driver cache, not EC state)", "ec_ambient=22.0", "dmi.ec_firmware_release: 0.14",
		"Tctl=40.0 NVMe_max=45.0 HDD_max=38.0", "RESULT: complete", "minisforum_n5_it5571 24576"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("snapshot lacks %q", want)
		}
	}
	if strings.Contains(m.Text, "SERIAL-SECRET") || strings.Contains(m.Text, "11111111-2222") {
		t.Fatal("serial/uuid in the snapshot")
	}
	f := snapshot.Parse("b", m.Text)
	if f.I2EC[0x1803] != "55" || len(f.ECRAM) != 0 {
		t.Fatalf("parse: %+v", f.I2EC)
	}
	// a fan read error makes the snapshot INCOMPLETE
	h.Put(h.HwmonRel()+"/fan2_input", "garbage\n")
	m = snapshot.TakeModeB(snapshot.Header{}, id, sysroot.Sys(), sysroot.Proc(), sysroot.Etc())
	if len(m.Incomplete) != 1 || !strings.Contains(m.Text, "RESULT: INCOMPLETE (fan2_input unreadable)") {
		t.Fatalf("incomplete: %v", m.Incomplete)
	}
}
