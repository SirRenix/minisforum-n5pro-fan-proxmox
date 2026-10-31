package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/fake"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/sysroot"
)

// map pwm3 on the reference board, fake driver: T1 PASS, the exact write
// set, restore to the default (auto, no n5-fangov config), bundle complete
// and recomputable.
func TestMapPWM3(t *testing.T) {
	e := newEnv(t, fake.Reference())
	if rc := e.hw("map", "pwm3", "--boot", "cold"); rc != exitOK {
		t.Fatalf("rc %d\n%s\n%s", rc, e.stdout, e.stderr)
	}
	if got := e.pwmWrites(); got != "pwm3_enable=1 pwm3=255 pwm3=217 pwm3=179 pwm3=140 pwm3_enable=2" {
		t.Fatalf("writes %q", got)
	}
	dir := e.bundle()
	if !strings.HasSuffix(dir, "-F8NAA-1.05-validate-pwm3") {
		t.Fatalf("dir %s", dir)
	}
	v := readVerdict(t, dir)
	if v["T0"].Verdict != spec.Pass || v["T1"].Verdict != spec.Pass {
		t.Fatalf("T0 %s T1 %s %v", v["T0"].Verdict, v["T1"].Verdict, v["T1"].Reasons)
	}
	m := readMeta(t, dir)
	dev, _ := m["spec_deviations"].([]any)
	joined := ""
	for _, d := range dev {
		joined += d.(string) + "\n"
	}
	// TESTSPEC 0.4: only real deviations (open conditions of rule 4); the
	// 0.3 ones are normed now, #13 and G6 through ecram are implemented
	if len(dev) != 1 || !strings.Contains(joined, "conditions of TESTSPEC 0.4 §1 rule 4 open") {
		t.Errorf("spec_deviations:\n%s", joined)
	}
	for _, gone := range []string{"in place of n5-fangov validate", "restore value without n5-fangov config", "redaction beyond host name", "T4/T5 not evaluated", "#13", "ecram"} {
		if strings.Contains(joined, gone) {
			t.Errorf("spec_deviations still name %q:\n%s", gone, joined)
		}
	}
	if m["spec_version"] != "0.4" || m["settle_s"] != 10.0 {
		t.Errorf("spec_version %v settle_s %v", m["spec_version"], m["settle_s"])
	}
	if rr, _ := m["redaction"].([]any); len(rr) == 0 {
		t.Errorf("meta.json lacks the redaction rule set")
	}
	if rv := m["restore_values"].(map[string]any); rv["3"] != "auto" {
		t.Fatalf("restore values %v", rv)
	}
	ev, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	for _, want := range []string{"operator confirmation typed", "guardian state written", "write pwm3=217", "T1 complete", "restore pwm3 after T1 ok", "gate G1 PASS", "gate G7 PASS", "driver minisforum_n5_it5571 0.2.1+sr1~rc1"} {
		if !strings.Contains(string(ev), want) {
			t.Errorf("events lack %q", want)
		}
	}
	for _, f := range []string{"meta.json", "samples.csv", "events.log", "verdict.json", "restore.json", "SHA256SUMS"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("bundle lacks %s", f)
		}
	}
	// nothing in the bundle names the host or a serial
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			for _, leak := range []string{"fakehost-secret", "SERIAL-SECRET"} {
				if strings.Contains(string(b), leak) {
					t.Errorf("%s contains %s", p, leak)
				}
			}
		}
		return nil
	})
	st, err := readState(statePath())
	if err != nil || st.State != stateRestored {
		t.Fatalf("state %+v %v", st, err)
	}
	recomputeEqual(t, dir)
}

// handback pwm1 on the reference board with the n5-fangov config: T2/T3
// PASS over 3 runs, restore value from the config.
func TestHandbackPWM1(t *testing.T) {
	e := newEnv(t, fake.Reference())
	e.h.Put("etc/n5-fangov/config.toml", "[[channel]]\nname = \"cpu\"\npwm = 1\nstop = \"auto\"\n")
	if rc := e.hw("handback", "pwm1", "--boot", "warm"); rc != exitOK {
		t.Fatalf("rc %d\n%s\n%s", rc, e.stdout, e.stderr)
	}
	want := "pwm1_enable=2" + strings.Repeat(" pwm1_enable=1 pwm1=255 pwm1_enable=2 pwm1_enable=1 pwm1=133 pwm1_enable=2", 3) + " pwm1_enable=2"
	if got := e.pwmWrites(); got != want {
		t.Fatalf("writes\n got %q\nwant %q", got, want)
	}
	dir := e.bundle()
	v := readVerdict(t, dir)
	if v["T2"].Verdict != spec.Pass || v["T3"].Verdict != spec.Pass || len(v["T2"].Runs) != 3 {
		t.Fatalf("T2 %s T3 %s: %v", v["T2"].Verdict, v["T3"].Verdict, v["T2"].Reasons)
	}
	if v["T4"].Verdict != spec.Inconclusive || v["T5"].Verdict != spec.Inconclusive {
		t.Fatalf("T4/T5 %s %s", v["T4"].Verdict, v["T5"].Verdict)
	}
	m := readMeta(t, dir)
	if src := m["restore_value_sources"].(map[string]any)["1"]; src != "n5-fangov config" {
		t.Fatalf("restore source %v", src)
	}
	if d := fmtAny(m["spec_deviations"]); !strings.Contains(d, "T4/T5 not evaluated by the tool") {
		t.Fatalf("spec_deviations lack T4/T5: %s", d)
	}
	if hd := fmtAny(readVerdict(t, dir)["T2"].Values); !strings.Contains(hd, "hold_deviations") {
		t.Fatalf("T2 values lack hold_deviations: %s", hd)
	}
	r := recomputeEqual(t, dir)
	if len(r.SubRuns) != 6 {
		t.Fatalf("sub-runs %d", len(r.SubRuns))
	}
}

// pwm4 handback: T2/T3 N/A, nothing written, no confirmation needed.
func TestHandbackPWM4WritesNothing(t *testing.T) {
	e := newEnv(t, fake.Reference())
	e.sess = sessionPiped()
	if rc := e.hw("handback", "pwm4", "--boot", "cold"); rc != exitOK {
		t.Fatalf("rc %d\n%s%s", rc, e.stdout, e.stderr)
	}
	v := readVerdict(t, e.bundle())
	if v["T2"].Verdict != spec.NotApplicable || v["T3"].Verdict != spec.NotApplicable || len(e.h.Writes) != 0 {
		t.Fatalf("T2 %s T3 %s writes %v", v["T2"].Verdict, v["T3"].Verdict, e.h.Writes)
	}
	recomputeEqual(t, e.bundle())
}

// Gate failures of design §4: a bundle with T0 and the writing tests
// INCONCLUSIVE ("not run: gate ..."), nothing written.
func TestGateFailuresWriteNothing(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*fake.Options)
		want string
	}{
		{"F8NAB", func(o *fake.Options) { o.Board = "F8NAB" }, "not run: gate G7 (board/vendor/driver): DMI \"N5 PRO\"/\"F8NAB\" is read-only"},
		{"upstream driver", func(o *fake.Options) { o.DriverVersion = "0.2.0" }, "not a 0.2.1+sr1 build"},
		{"force", func(o *fake.Options) { o.Force = "Y" }, "force=Y"},
		{"no dcr", func(o *fake.Options) { o.DebugRegs = false }, "duty registers not readable"},
		{"pwm hidden", func(o *fake.Options) { o.PWMExposed = false }, "not run: gate pwm not exposed: pwm3/pwm3_enable not present"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := fake.Reference()
			c.mod(&o)
			e := newEnv(t, o)
			if rc := e.hw("map", "pwm3", "--boot", "cold"); rc != exitFail {
				t.Fatalf("rc %d", rc)
			}
			v := readVerdict(t, e.bundle())
			if v["T1"].Verdict != spec.Inconclusive || !strings.Contains(strings.Join(v["T1"].Reasons, ";"), c.want) {
				t.Fatalf("T1 %s %v", v["T1"].Verdict, v["T1"].Reasons)
			}
			if len(e.h.AllWrites) != 0 || !strings.Contains(e.stdout.String(), "Nothing will be written") {
				t.Fatalf("writes %v\n%s", e.h.AllWrites, e.stdout)
			}
			if _, err := os.Stat(statePath()); err == nil {
				t.Fatal("state file written without a write")
			}
			m := readMeta(t, e.bundle())
			for _, g := range m["gates"].([]any) {
				g := g.(map[string]any)
				// TESTSPEC 0.4 §3 (#12): no gate window after a failed G7
				if (g["id"] == "G3" || g["id"] == "G4") && c.name != "no dcr" && c.name != "pwm hidden" && g["result"] != "SKIPPED" {
					t.Errorf("%s %v after a failed G7", g["id"], g["result"])
				}
			}
			recomputeEqual(t, e.bundle())
		})
	}
	// container
	e := newEnv(t, fake.Reference())
	e.h.Put("run/systemd/container", "lxc\n")
	if rc := e.hw("map", "pwm1"); rc != exitFail || len(e.h.AllWrites) != 0 {
		t.Fatalf("container: rc %d writes %v", rc, e.h.AllWrites)
	}
	if v := readVerdict(t, e.bundle()); !strings.Contains(strings.Join(v["T1"].Reasons, ";"), "running in a container (lxc") {
		t.Fatalf("container: %v", v["T1"].Reasons)
	}
}

func sessionPiped() (s sessionT) { return sessionT{Stdin: ""} }

func fmtAny(v any) string { return fmt.Sprint(v) }

// The refusals before the run (TESTSPEC 0.4 §3) leave no bundle; the third (lock held, restore open) is covered by TestDeviceChangedMidRun and guardian_test.go.
func TestRefusalsLeaveNoBundle(t *testing.T) {
	e := newEnv(t, fake.Reference())
	e.units["n5-fangov"] = "activating"
	if rc := e.hw("map", "pwm3"); rc != exitFail || len(e.bundles()) != 0 || !strings.Contains(e.stderr.String(), "n5-fangov daemon is activating") {
		t.Fatalf("n5-fangov: rc %d %v %s", rc, e.bundles(), e.stderr)
	}
	e = newEnv(t, fake.Reference())
	e.sess = sessionPiped()
	if rc := e.hw("map", "pwm3"); rc != exitFail || len(e.bundles()) != 0 || !strings.Contains(e.stderr.String(), "not a terminal") {
		t.Fatalf("piped: rc %d %s", rc, e.stderr)
	}
	e = newEnv(t, fake.Reference())
	e.sess = sessionT{Stdin: "pts", Remote: []string{"ancestor process sshd-session (pid 9)"}}
	if rc := e.hw("map", "pwm3"); rc != exitFail || !strings.Contains(e.stderr.String(), "remote") {
		t.Fatalf("remote: rc %d %s", rc, e.stderr)
	}
	e = newEnv(t, fake.Reference())
	e.sess = sessionT{Stdin: "pts", Remote: []string{"SSH_CONNECTION set"}}
	stdin = strings.NewReader("yes\n")
	if rc := e.hw("map", "pwm3", "--i-am-at-the-console"); rc != exitFail || len(e.bundles()) != 0 || len(e.h.AllWrites) != 0 {
		t.Fatalf("wrong confirmation: rc %d %v %v", rc, e.bundles(), e.h.AllWrites)
	}
}

// abortCase runs map pwm3 with something happening at sleep n.
func abortCase(t *testing.T, at int, act func(e *env, ctx context.Context) bool) (*env, string) {
	e := newEnv(t, fake.Reference())
	e.clock.hook = func(n int, ctx context.Context) bool {
		if n == at {
			return act(e, ctx)
		}
		return true
	}
	if rc := e.hw("map", "pwm3", "--boot", "cold"); rc != exitFail {
		t.Fatalf("rc %d\n%s", rc, e.stdout)
	}
	dir := e.bundle()
	return e, dir
}

// Every abort path: limit, G1 (process, pwm cache, lock), signal. The
// channel is restored, T1 INCONCLUSIVE "aborted", the bundle recomputable.
func TestAbortPaths(t *testing.T) {
	// gate window: sleeps 1-60, T1 rest 61-72, step 255 from 73
	cases := []struct {
		name string
		act  func(e *env, ctx context.Context) bool
		want string
	}{
		{"Tctl limit", func(e *env, _ context.Context) bool { e.h.SetTemp("tctl", "86000"); return true }, "CPU Tctl 86.0 C >= 85 C"},
		{"HDD sensor lost", func(e *env, _ context.Context) bool { e.h.SetTemp("hdd", ""); return true }, "limit sensor hdd unreadable in 3 consecutive samples"},
		{"regulator process", func(e *env, _ context.Context) bool {
			e.h.AddProcess(777, "fancontrol", "/usr/sbin/fancontrol")
			return true
		}, "process fancontrol (pid 777)"},
		{"regulator unit", func(e *env, _ context.Context) bool { e.units["fan2go"] = "active"; return true }, "unit fan2go active"},
		{"foreign echo", func(e *env, _ context.Context) bool { e.h.ForeignWrite(2, "pwm2_enable", 1); return true }, "pwm2_enable is 1, expected 2: written by another program"},
		{"lock path deleted", func(e *env, _ context.Context) bool {
			os.RemoveAll(filepath.Join(sysroot.Run(), "n5-fangov"))
			return true
		}, "writer lock file removed or replaced"},
		{"SIGINT", func(e *env, ctx context.Context) bool {
			syscall.Kill(os.Getpid(), syscall.SIGINT)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Error("SIGINT did not cancel the run")
			}
			return false
		}, "cancelled (signal)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, dir := abortCase(t, 80, c.act)
			m := readMeta(t, dir)
			if !strings.Contains(m["aborted"].(string), c.want) {
				t.Fatalf("aborted %q, want %q", m["aborted"], c.want)
			}
			if e.h.EnableValue(3) != 2 || !strings.HasSuffix(e.pwmWrites(), "pwm3_enable=2") {
				t.Fatalf("not restored: enable %d, writes %s", e.h.EnableValue(3), e.pwmWrites())
			}
			v := readVerdict(t, dir)
			if v["T1"].Verdict != spec.Inconclusive || !strings.HasPrefix(v["T1"].Reasons[0], "aborted: ") {
				t.Fatalf("T1 %+v", v["T1"])
			}
			if st, err := readState(statePath()); err != nil || st.State != stateRestored {
				t.Fatalf("state %+v %v", st, err)
			}
			recomputeEqual(t, dir)
		})
	}
}

// TESTSPEC 0.4 §4 (#13): a Tctl abort during map pwm3 sets pwm1 to 255 and
// leaves it there, pwm3 gets its restore value; meta.json and the guardian
// state file name 255 for pwm1, spec_deviations no longer list #13.
func TestTemperatureAbortHoldsSensorChannel(t *testing.T) {
	e, dir := abortCase(t, 80, func(e *env, _ context.Context) bool { e.h.SetTemp("tctl", "86000"); return true })
	if e.h.EnableValue(1) != 1 || e.h.EnableValue(3) != 2 || !strings.HasSuffix(e.pwmWrites(), "pwm1_enable=1 pwm1=255 pwm3_enable=2") {
		t.Fatalf("enable pwm1 %d pwm3 %d, writes %s", e.h.EnableValue(1), e.h.EnableValue(3), e.pwmWrites())
	}
	m := readMeta(t, dir)
	rv := m["restore_values"].(map[string]any)
	src := m["restore_value_sources"].(map[string]any)
	if rv["1"] != "255" || !strings.Contains(fmt.Sprint(src["1"]), "temperature abort: CPU Tctl 86.0 C") || rv["3"] != "auto" {
		t.Fatalf("restore values %v sources %v", rv, src)
	}
	if strings.Contains(fmtAny(m["spec_deviations"]), "#13") {
		t.Fatalf("spec_deviations still list #13: %v", m["spec_deviations"])
	}
	st, err := readState(statePath())
	if err != nil || st.State != stateRestored || st.Channels["1"] != "255" {
		t.Fatalf("state %+v %v", st, err)
	}
	recomputeEqual(t, dir)
}

// G6 through the driver's ecram (TESTSPEC 0.4 §3, #9): PASS with the
// curve tables in meta.json; a broken dump FAIL (not blocking); without
// ecram PARTIAL.
func TestG6ECRAM(t *testing.T) {
	g6 := func(m map[string]any) map[string]any {
		for _, g := range m["gates"].([]any) {
			if g := g.(map[string]any); g["id"] == "G6" {
				return g
			}
		}
		t.Fatal("no G6")
		return nil
	}
	var b strings.Builder
	for row := 0; row < 256; row += 16 {
		fmt.Fprintf(&b, "%02x:", row)
		for i := 0; i < 16; i++ {
			fmt.Fprintf(&b, " %02x", row+i)
		}
		b.WriteString("\n")
	}
	e := newEnv(t, fake.Reference())
	e.h.Put("sys/kernel/debug/minisforum_n5_it5571/ecram", b.String())
	if rc := e.hw("map", "pwm3", "--boot", "cold"); rc != exitOK {
		t.Fatalf("rc %d\n%s", rc, e.stdout)
	}
	m := readMeta(t, e.bundle())
	tab, _ := m["ec_ram_tables"].(map[string]any)
	if g := g6(m); g["result"] != "PASS" || tab["0x30-0x38"] != "30 31 32 33 34 35 36 37 38" || !strings.HasPrefix(fmt.Sprint(tab["0x70-0x81"]), "70 71") {
		t.Fatalf("G6 %v tables %v", g, tab)
	}
	if strings.Contains(fmtAny(m["spec_deviations"]), "ecram") {
		t.Fatalf("spec_deviations still list ecram: %v", m["spec_deviations"])
	}
	e = newEnv(t, fake.Reference())
	e.h.Put("sys/kernel/debug/minisforum_n5_it5571/ecram", "00: 00 01\n")
	if rc := e.hw("map", "pwm3", "--boot", "cold"); rc != exitOK {
		t.Fatalf("broken ecram blocked: rc %d\n%s", rc, e.stdout)
	}
	if g := g6(readMeta(t, e.bundle())); g["result"] != "FAIL" || !strings.Contains(fmt.Sprint(g["detail"]), "incomplete dump") {
		t.Fatalf("broken ecram: %v", g)
	}
	e = newEnv(t, fake.Reference())
	if rc := e.hw("map", "pwm3", "--boot", "cold"); rc != exitOK {
		t.Fatalf("rc %d", rc)
	}
	if g := g6(readMeta(t, e.bundle())); g["result"] != "PARTIAL" {
		t.Fatalf("without ecram: %v", g)
	}
}

// A module reload with another srcversion mid-run: nothing is written to
// the new device, the restore fails loudly, the state stays active and
// `restore` refuses for the same reason.
func TestDeviceChangedMidRun(t *testing.T) {
	e, dir := abortCase(t, 80, func(e *env, _ context.Context) bool { e.h.Reload("NEWSRCVERSION0000"); return true })
	m := readMeta(t, dir)
	if rf, _ := m["restore_failed"].([]any); len(rf) != 1 || !strings.Contains(rf[0].(string), "driver device changed") {
		t.Fatalf("restore_failed %v", m["restore_failed"])
	}
	if !strings.Contains(e.stdout.String(), "!! RESTORE FAILED pwm3") {
		t.Fatalf("not loud:\n%s", e.stdout)
	}
	if got := e.pwmWrites(); got != "pwm3_enable=1 pwm3=255" {
		t.Fatalf("written after the reload: %s", got)
	}
	st, err := readState(statePath())
	if err != nil || st.State != stateActive {
		t.Fatalf("state %+v %v", st, err)
	}
	n := len(e.h.AllWrites)
	if rc := cmdRestore(nil); rc != exitFail || len(e.h.AllWrites) != n {
		t.Fatalf("restore: rc %d, writes %v", rc, e.h.AllWrites[n:])
	}
	// the guardian refuses a new run until the channel is restored by hand
	if rc := guardian("map", []string{"pwm1"}); rc != exitFail || !strings.Contains(e.stderr.String(), "Run first: n5-ectest restore") {
		t.Fatalf("guardian: rc %d %s", rc, e.stderr)
	}
}

// --dry-run: the simulator, no sysfs write, bundle marked, recomputable.
func TestDryRun(t *testing.T) {
	e := newEnv(t, fake.Reference())
	for _, test := range []string{"map", "handback"} {
		if rc := cmdValidate(test, []string{"pwm3", "--dry-run", "--boot", "cold", "--out", e.out}); rc != exitOK {
			t.Fatalf("%s: rc %d\n%s%s", test, rc, e.stdout, e.stderr)
		}
	}
	if len(e.h.AllWrites) != 0 {
		t.Fatalf("dry-run wrote %v", e.h.AllWrites)
	}
	for _, dir := range e.bundles() {
		if !strings.HasSuffix(dir, "-dryrun") {
			t.Fatalf("dir %s", dir)
		}
		if m := readMeta(t, dir); !strings.HasPrefix(m["mode"].(string), "dry-run") {
			t.Fatalf("mode %v", m["mode"])
		}
		recomputeEqual(t, dir)
	}
	if rc := cmdValidate("map", []string{"pwm3", "--hold", "10s"}); rc != exitUsage {
		t.Fatalf("timing flag without --dry-run: rc %d", rc)
	}
	for _, bad := range [][]string{{"pwm5"}, {"fan3"}, {"pwm3", "--boot", "hot"}, {"pwm3", "--restore", "pwm3=50"}, {}} {
		if rc := cmdValidate("map", bad); rc != exitUsage {
			t.Errorf("%v: rc %d", bad, rc)
		}
	}
}
