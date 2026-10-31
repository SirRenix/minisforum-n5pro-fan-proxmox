package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/logger"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/pwmw"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/snapshot"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
)

func mustResolve(t *testing.T) pwmw.Device {
	t.Helper()
	d, _, err := pwmw.Resolve(sysroot.Sys())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestIdentify(t *testing.T) {
	e := newEnv(t, fake.Reference())
	if rc := cmdIdentify(nil); rc != exitOK {
		t.Fatalf("rc %d", rc)
	}
	out := e.stdout.String()
	for _, want := range []string{"product         N5 PRO (vendor Micro Computer (HK) Tech Limited)", "EC firmware     0.14", "map/handback    pwm3: allowed", "debug_regs=Y", "OS              debian 13, lockdown none, container none"} {
		if !strings.Contains(out, want) {
			t.Errorf("identify lacks %q:\n%s", want, out)
		}
	}
	e.stdout.Reset()
	if rc := cmdIdentify([]string{"--json"}); rc != exitOK {
		t.Fatal(rc)
	}
	var v map[string]any
	if err := json.Unmarshal(e.stdout.Bytes(), &v); err != nil {
		t.Fatalf("json: %v\n%s", err, e.stdout)
	}
	if strings.Contains(e.stdout.String(), "fakehost-secret") || strings.Contains(e.stdout.String(), "SERIAL-SECRET") {
		t.Fatal("host name or serial in the output")
	}
	// another board: read-only commands refused
	o := fake.Reference()
	o.Product, o.Board = "MS-01", "X"
	e = newEnv(t, o)
	cmdIdentify(nil)
	if !strings.Contains(e.stdout.String(), "identify, diff and report only") {
		t.Fatalf("other board:\n%s", e.stdout)
	}
	if rc := cmdSnapshot([]string{"x", "--out", e.out}); rc != exitFail {
		t.Fatalf("snapshot on another board: rc %d", rc)
	}
}

func TestSnapshotCommand(t *testing.T) {
	e := newEnv(t, fake.Reference())
	if rc := cmdSnapshot([]string{"cold", "--boot", "cold", "--out", e.out}); rc != exitOK {
		t.Fatalf("rc %d %s", rc, e.stderr)
	}
	dir := e.bundle()
	if !strings.HasSuffix(dir, "-F8NAA-1.05-snapshot-cold") {
		t.Fatalf("dir %s", dir)
	}
	for _, f := range []string{"snapshot.txt", "meta.json", "verdict.json", "samples.csv", "events.log", "SHA256SUMS"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	v := readVerdict(t, dir)
	if v["T6"].Verdict != "REPORTED" {
		t.Fatalf("T6 %+v", v["T6"])
	}
	if len(e.h.AllWrites) != 0 {
		t.Fatal("snapshot wrote")
	}
	recomputeEqual(t, dir)
	// diff of two mode-B snapshots: no EC RAM -> incomplete input (2)
	if rc := cmdSnapshot([]string{"warm", "--out", e.out}); rc != exitOK {
		t.Fatal(rc)
	}
	b := e.bundles()
	if rc := cmdDiff([]string{b[0], b[1]}); rc != snapshot.DiffIncomplete {
		t.Fatalf("diff mode B: rc %d", rc)
	}
	for _, bad := range [][]string{{}, {"a b"}, {"x", "--mode", "C"}, {"x", "--boot", "hot"}} {
		if rc := cmdSnapshot(append(bad, "--out", e.out)); rc != exitUsage {
			t.Errorf("%v: rc %d", bad, rc)
		}
	}
	if rc := cmdSnapshot([]string{"x", "--mode", "A", "--out", e.out}); rc != exitFail || !strings.Contains(e.stderr.String(), "not built yet") {
		t.Fatalf("mode A: rc %d", rc)
	}
	if rc := cmdDiff([]string{"one"}); rc != exitUsage {
		t.Fatalf("diff usage: rc %d", rc)
	}
	// a Proxmox host named "pve": host redacted, kernel release intact
	e = newEnv(t, fake.Reference())
	e.h.Put("proc/sys/kernel/hostname", "pve\n")
	if rc := cmdSnapshot([]string{"pvehost", "--out", e.out}); rc != exitOK {
		t.Fatal(rc)
	}
	m := readMeta(t, e.bundle())
	if k := m["identity"].(map[string]any)["kernel"]; k != "7.0.14-20-pve" {
		t.Fatalf("kernel mangled: %v", k)
	}
	if txt, _ := os.ReadFile(filepath.Join(e.bundle(), "snapshot.txt")); !strings.Contains(string(txt), "kernel: 7.0.14-20-pve") {
		t.Fatalf("snapshot.txt:\n%s", txt)
	}
	// module unloaded: mode B impossible
	o := fake.Reference()
	o.NoDriver = true
	e = newEnv(t, o)
	if rc := cmdSnapshot([]string{"x", "--out", e.out}); rc != exitFail || !strings.Contains(e.stderr.String(), "f8a-snapshot.sh") {
		t.Fatalf("no driver: rc %d %s", rc, e.stderr)
	}
}

// The report recomputes, flags a forged verdict and writes a page that
// opens offline: no script, no URL, inline SVG and CSS only.
func TestReportCommand(t *testing.T) {
	e := newEnv(t, fake.Reference())
	if rc := e.hw("handback", "pwm3", "--boot", "cold"); rc != exitOK {
		t.Fatalf("rc %d\n%s", rc, e.stdout)
	}
	dir := e.bundle()
	e.stdout.Reset()
	if rc := cmdReport([]string{dir}); rc != exitOK {
		t.Fatalf("report rc %d\n%s", rc, e.stdout)
	}
	page, err := os.ReadFile(dir + ".report.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	for _, bad := range []string{"<script", "http://", "https://", "src=", "@import", "url("} {
		if strings.Contains(strings.ToLower(s), bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	for _, want := range []string{"<svg", "<style>", "T2/T3 sub-runs", "run 3 M: written", "Duty registers DCR1", "spec_deviations", "SHA256SUMS verified", "Every verdict (with its runs and sub-runs) recomputed", "900 s", "W+16", "Tctl 85", "EC board 0x05", "A DCR3=", "A DCR4="} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if regexp.MustCompile(`(?i)prefers-color-scheme: dark`).FindString(s) == "" {
		t.Error("no dark theme")
	}
	// forge verdict.json (and its checksum): the recomputation disagrees
	vb, _ := os.ReadFile(filepath.Join(dir, bundle.Verdict))
	var vj map[string]any
	if err := json.Unmarshal(vb, &vj); err != nil {
		t.Fatal(err)
	}
	for _, x := range vj["tests"].([]any) {
		if m := x.(map[string]any); m["id"] == "T2" {
			if m["verdict"] != "PASS" {
				t.Fatalf("T2 %v", m["verdict"])
			}
			m["verdict"] = "FAIL"
		}
	}
	forged, _ := json.MarshalIndent(vj, "", "  ")
	os.WriteFile(filepath.Join(dir, bundle.Verdict), forged, 0o640)
	bundle.WriteSums(dir)
	e.stdout.Reset()
	if rc := cmdReport([]string{dir}); rc != exitFail || !strings.Contains(e.stdout.String(), "MISMATCH T2: verdict.json FAIL, recomputed PASS") {
		t.Fatalf("forged: rc %d\n%s", rc, e.stdout)
	}
	// forged run and sub-run verdicts and a test only in verdict.json are
	// mismatches too, as is a G4 in meta.json the gate window does not give
	forgeJSON(t, dir, bundle.Verdict, vb, func(v map[string]any) {
		tests := v["tests"].([]any)
		for _, x := range tests {
			if m := x.(map[string]any); m["id"] == "T3" {
				run := m["runs"].([]any)[1].(map[string]any)
				run["verdict"] = "FAIL"
				run["sub_runs"].([]any)[0].(map[string]any)["t3"] = "FAIL"
			}
		}
		v["tests"] = append(tests, map[string]any{"id": "T9", "verdict": "PASS"})
	})
	e.stdout.Reset()
	out := ""
	if rc := cmdReport([]string{dir}); rc != exitFail {
		t.Fatalf("forged runs: rc %d\n%s", rc, e.stdout)
	}
	out = e.stdout.String()
	for _, want := range []string{"MISMATCH T3 run 2: verdict.json FAIL, recomputed PASS", "MISMATCH T3 run 2 H T3: verdict.json FAIL, recomputed PASS", "MISMATCH T9: verdict.json PASS, recomputed (not recomputed)"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	forgeJSON(t, dir, bundle.Verdict, vb, nil)
	mb, _ := os.ReadFile(filepath.Join(dir, bundle.Meta))
	forgeJSON(t, dir, bundle.Meta, mb, func(m map[string]any) {
		for _, g := range m["gates"].([]any) {
			if gm := g.(map[string]any); gm["id"] == "G4" {
				gm["result"] = "FAIL"
			}
		}
	})
	e.stdout.Reset()
	if rc := cmdReport([]string{dir}); rc != exitFail || !strings.Contains(e.stdout.String(), "MISMATCH G4: meta.json FAIL, recomputed PASS") {
		t.Fatalf("forged G4: rc %d\n%s", rc, e.stdout)
	}
	forgeJSON(t, dir, bundle.Meta, mb, nil)
	// a changed sample without new checksums
	csv := filepath.Join(dir, bundle.Samples)
	b, _ := os.ReadFile(csv)
	os.WriteFile(csv, append(b, []byte("\n")...), 0o640)
	e.stdout.Reset()
	if rc := cmdReport([]string{dir}); rc != exitFail || !strings.Contains(e.stdout.String(), "samples.csv: checksum differs") {
		t.Fatalf("changed csv: rc %d\n%s", rc, e.stdout)
	}
}

// log end to end: a 2-hour run, check, annotate, report.
func TestLogCommand(t *testing.T) {
	e := newEnv(t, fake.Reference())
	oldF, oldJ, oldFree := logFangov, logJournal, logFree
	defer func() { logFangov, logJournal, logFree = oldF, oldJ, oldFree }()
	logFangov = func() (string, int, error) { return "inactive", 0, nil }
	logJournal = func(string, string, time.Time) ([]logger.JLine, string, error) { return nil, "c", nil }
	logFree = func(string) (uint64, error) { return 50 << 30, nil }
	out := t.TempDir()
	if rc := cmdLog([]string{"--duration", "2h", "--interval", "30s", "--run-id", "x", "--out", out}); rc != exitOK {
		t.Fatalf("rc %d %s", rc, e.stderr)
	}
	ents, _ := os.ReadDir(out)
	// the run directory and, beside it, the outbox (mails carry the address)
	if len(ents) != 2 || !strings.HasSuffix(ents[0].Name(), "-F8NAA-1.05-log-x") || ents[1].Name() != ents[0].Name()+".outbox" {
		t.Fatalf("run dir %v", ents)
	}
	dir := filepath.Join(out, ents[0].Name())
	if !strings.Contains(e.stdout.String(), "K3 #1 n5-fangov.service ActiveState=inactive") || !strings.Contains(e.stdout.String(), "mail queued in "+dir+".outbox/") {
		t.Fatalf("stdout:\n%s", e.stdout)
	}
	if rc := cmdLog([]string{"check", "x", "--out", out}); rc != exitOK {
		t.Fatalf("check of a finished run: rc %d", rc)
	}
	// the unit starting again after the end (boot) exits cleanly
	if rc := cmdLog([]string{"--run-id", "x", "--out", out}); rc != exitOK || !strings.Contains(e.stdout.String(), "nothing to do") {
		t.Fatalf("finished run started again: rc %d", rc)
	}
	if rc := cmdLog([]string{"annotate", "--out", out, "x", "--event", "1", "n5-fangov", "stopped", "for", "the", "test"}); rc != exitOK {
		t.Fatalf("annotate rc %d %s", rc, e.stderr)
	}
	if rc := cmdLog([]string{"annotate", "--out", out, "x", "--event", "99", "no"}); rc != exitFail {
		t.Fatalf("annotate unknown event: rc %d", rc)
	}
	e.stdout.Reset()
	if rc := cmdReport([]string{dir}); rc != exitOK {
		t.Fatalf("report rc %d\n%s", rc, e.stdout)
	}
	page, _ := os.ReadFile(dir + ".report.html")
	if !strings.Contains(string(page), "Long-term log (F8b K1–K7)") || !strings.Contains(string(page), "<td>K3</td><td>1</td><td>1</td><td>0</td>") {
		t.Fatalf("log report:\n%.3000s", page)
	}
	for _, bad := range [][]string{{"--interval", "2s"}, {"--duration", "0d"}, {"--run-id", "a/b"}, {"check"}, {"annotate", "x"}} {
		if rc := cmdLog(append(bad, "--out", out)); rc != exitUsage && rc != exitFail {
			t.Errorf("%v: rc %d", bad, rc)
		}
	}
}

func TestVersionAndUsage(t *testing.T) {
	e := newEnv(t, fake.Reference())
	if rc := run([]string{"version"}); rc != exitOK || !strings.Contains(e.stdout.String(), "n5-ectest "+version) || !strings.Contains(e.stdout.String(), "TESTSPEC 0.4") {
		t.Fatalf("version: %s", e.stdout)
	}
	if rc := run(nil); rc != exitUsage {
		t.Fatal(rc)
	}
	if rc := run([]string{"frobnicate"}); rc != exitUsage {
		t.Fatal(rc)
	}
	if rc := workerMain([]string{"snapshot"}); rc != exitUsage {
		t.Fatal(rc)
	}
	// the command list has a line per command and the README link
	e.stderr.Reset()
	if rc := run([]string{"--help"}); rc != exitOK || !strings.Contains(e.stderr.String(), readmeURL) || !strings.Contains(e.stderr.String(), "T1 channel mapping") {
		t.Fatalf("help: rc %d\n%s", rc, e.stderr)
	}
	// -h of every command is exit 0 and runs nothing; a bad flag is 64
	for name := range commands {
		if rc := run([]string{name, "-h"}); rc != exitOK {
			t.Errorf("%s -h: rc %d", name, rc)
		}
	}
	if len(e.h.AllWrites) != 0 {
		t.Fatal("-h wrote")
	}
	for _, bad := range [][]string{{"diff", "a", "--frob"}, {"report", "--frob", "x"}, {"identify", "--frob"}, {"restore", "--frob"}, {"map", "pwm3", "--frob"}, {"restore", "--from", "x", "--discard"}} {
		if rc := run(bad); rc != exitUsage {
			t.Errorf("%v: rc %d", bad, rc)
		}
	}
}

// Without root the commands say how to run them, and identify does not
// report a gate failure it cannot judge (debugfs and /proc/1/environ are
// root-only).
func TestNotRoot(t *testing.T) {
	e := newEnv(t, fake.Reference())
	old := isRoot
	isRoot = func() bool { return false }
	defer func() { isRoot = old }()
	if rc := run([]string{"restore"}); rc != exitFail || !strings.Contains(e.stderr.String(), "must run as root: sudo n5-ectest restore") {
		t.Fatalf("restore: rc %d %s", rc, e.stderr)
	}
	if rc := run([]string{"identify"}); rc != exitOK || !strings.Contains(e.stdout.String(), "not evaluated without root") || strings.Contains(e.stdout.String(), "gate failure") {
		t.Fatalf("identify:\n%s", e.stdout)
	}
}

// forgeJSON rewrites a JSON file of a bundle from orig (changed by f, nil =
// unchanged) and renews SHA256SUMS.
func forgeJSON(t *testing.T, dir, name string, orig []byte, f func(map[string]any)) {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(orig, &v); err != nil {
		t.Fatal(err)
	}
	if f != nil {
		f(v)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := bundle.WriteSums(dir); err != nil {
		t.Fatal(err)
	}
}

// Every n5-ectest command line in the README names a command and only flags
// its usage lists; map/handback lines also pass the real flag parser (the
// README must not drift from the tool).
func TestReadmeExamples(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	inCode, n := false, 0
	for _, raw := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "```") {
			inCode = !inCode
			continue
		}
		l = strings.TrimPrefix(l, "sudo ")
		if !inCode || !strings.HasPrefix(l, "n5-ectest ") {
			continue
		}
		if i := strings.Index(l, " #"); i >= 0 {
			l = l[:i]
		}
		var args []string
		for _, f := range regexp.MustCompile(`"[^"]*"|\S+`).FindAllString(l, -1) {
			args = append(args, strings.Trim(f, `"`))
		}
		n++
		c, ok := commands[args[1]]
		if !ok {
			t.Errorf("README: unknown command in %q", l)
			continue
		}
		for _, a := range args[2:] {
			if strings.HasPrefix(a, "--") && !strings.Contains(c.usage, strings.SplitN(a, "=", 2)[0]) {
				t.Errorf("README: %s not in the usage of %s (%q)", a, args[1], l)
			}
		}
		if args[1] == "map" || args[1] == "handback" {
			if _, rc := parseRunOpts(args[1], args[2:]); rc != exitOK {
				t.Errorf("README: %q does not parse (rc %d)", l, rc)
			}
		}
	}
	if n < 10 {
		t.Fatalf("only %d example lines found", n)
	}
}
