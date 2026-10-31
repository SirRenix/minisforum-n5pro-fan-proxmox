package bundle

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
)

func TestWriteLoadRoundTrip(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(t.TempDir(), DirName(start, "F8NAA", "1.05", "validate-pwm3"))
	if !strings.HasSuffix(dir, "20261009-1200-F8NAA-1.05-validate-pwm3") {
		t.Fatalf("dir %s", dir)
	}
	if DirName(start, "", "a b/c", "x") != "20261009-1200-unknown-a_b_c-x" {
		t.Fatal("unsafe characters")
	}
	red := identity.NewRedactor("", "secrethost")
	w, err := Create(dir, red)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, red); err == nil {
		t.Fatal("an existing bundle directory must not be reused")
	}
	s := spec.NewSample(start, 0.25, "gate", 3, -1)
	s.DCR = [5]int{85, 74, 87, 87, 140}
	s.Fan = [3]int{2003, -1, 1656}
	s.Tctl, s.Load1 = 41.5, 0.2
	w.Sample(s)
	w.Event("2026-10-09T12:00:00Z [t_s=0.000] start on secrethost from 192.0.2.20, eth0 02:00:00:00:00:01")
	if err := w.WriteJSON(Meta, map[string]any{"host": "secrethost", "command": "map"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFile("../x", []byte("no")); err == nil {
		t.Fatal("path in file name accepted")
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	w.Event("after finish") // ignored
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.SumProblems) != 0 {
		t.Fatalf("sums: %v", l.SumProblems)
	}
	if len(l.Samples) != 1 || l.Samples[0].DCR != s.DCR || l.Samples[0].Fan != s.Fan || l.Samples[0].Tctl != 41.5 || !math.IsNaN(l.Samples[0].NVMeMax) || l.Samples[0].TS != 0.25 {
		t.Fatalf("samples %+v", l.Samples)
	}
	all := strings.Join(l.Events, "\n") + string(l.MetaRaw)
	for _, leak := range []string{"secrethost", "192.0.2.20", "02:00:00:00:00:01"} {
		if strings.Contains(all, leak) {
			t.Fatalf("%q not redacted: %s", leak, all)
		}
	}
	if len(l.Events) != 1 || l.Meta["host"] != identity.HostPlaceholder {
		t.Fatalf("events %v meta %v", l.Events, l.Meta)
	}
	csv, _ := os.ReadFile(filepath.Join(dir, Samples))
	if !strings.HasPrefix(string(csv), strings.Join(spec.CSVHeader, ",")+"\n") || !strings.Contains(string(csv), ",gate,3,,85,74,87,87,140,2003,,1656,") {
		t.Fatalf("csv: %s", csv)
	}
	// a changed file is found
	if err := os.WriteFile(filepath.Join(dir, Events), []byte("forged\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if p := VerifySums(dir); len(p) != 1 || !strings.Contains(p[0], "events.log: checksum differs") {
		t.Fatalf("verify: %v", p)
	}
}

// SHA256SUMS covers files in subdirectories too (TESTSPEC §8: all files);
// hidden temporary files and symlinks are not listed; the directory name
// is in UTC like every time of a bundle.
func TestSumsSubdirsAndUTCName(t *testing.T) {
	cet := time.FixedZone("CET", 3600)
	if got := DirName(time.Date(2026, 10, 9, 13, 30, 0, 0, cet), "F8NAA", "1.05", "x"); got != "20261009-1230-F8NAA-1.05-x" {
		t.Fatalf("dir name %s", got)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o640)
	os.MkdirAll(filepath.Join(dir, "sub", "deeper"), 0o750)
	os.WriteFile(filepath.Join(dir, "sub", "deeper", "b.txt"), []byte("b"), 0o640)
	os.WriteFile(filepath.Join(dir, ".tmp-x"), []byte("t"), 0o640)
	os.Symlink("/etc/hostname", filepath.Join(dir, "link"))
	if err := WriteSums(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, Sums))
	if !strings.Contains(string(b), "  sub/deeper/b.txt\n") || !strings.Contains(string(b), "  a.txt\n") || strings.Contains(string(b), ".tmp-x") || strings.Contains(string(b), "link") {
		t.Fatalf("sums:\n%s", b)
	}
	if p := VerifySums(dir); len(p) != 0 {
		t.Fatalf("verify: %v", p)
	}
	os.WriteFile(filepath.Join(dir, "sub", "deeper", "b.txt"), []byte("B"), 0o640)
	os.WriteFile(filepath.Join(dir, "sub", "new.txt"), []byte("n"), 0o640)
	if p := strings.Join(VerifySums(dir), "; "); !strings.Contains(p, "sub/deeper/b.txt: checksum differs") || !strings.Contains(p, "sub/new.txt: not in SHA256SUMS") {
		t.Fatalf("verify: %s", p)
	}
	// a forged list cannot make the check read outside the bundle
	os.WriteFile(filepath.Join(dir, Sums), []byte("00  ../../etc/passwd\n"), 0o640)
	if p := strings.Join(VerifySums(dir), "; "); !strings.Contains(p, "../../etc/passwd: path outside the bundle") {
		t.Fatalf("verify: %s", p)
	}
}
