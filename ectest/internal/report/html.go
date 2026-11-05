package report

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/logger"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
)

func newChart(title, xlabel, ylabel string) chart {
	n := math.NaN()
	return chart{title: title, xlabel: xlabel, ylabel: ylabel, xmin: n, xmax: n, ymin: n, ymax: n}
}

const css = `
:root{--bg:#ffffff;--fg:#1b1f24;--mut:#5b6470;--line:#d9dee4;--card:#f6f8fa;
--s1:#1f6feb;--s2:#d1242f;--s3:#1a7f37;--s4:#8250df;--s5:#bc4c00;--s6:#0a7d8c;--s7:#6e7781;
--pass:#1a7f37;--fail:#d1242f;--inc:#9a6700;--band:rgba(31,111,235,.10);--band2:rgba(26,127,55,.16);--ph:rgba(127,127,127,.07)}
@media (prefers-color-scheme: dark){:root{--bg:#0d1117;--fg:#e6edf3;--mut:#9aa4af;--line:#30363d;--card:#161b22;
--s1:#58a6ff;--s2:#ff7b72;--s3:#3fb950;--s4:#d2a8ff;--s5:#ffa657;--s6:#56d4dd;--s7:#b1bac4;
--pass:#3fb950;--fail:#ff7b72;--inc:#d29922;--band:rgba(88,166,255,.14);--band2:rgba(63,185,80,.18);--ph:rgba(200,200,200,.06)}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.45 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:1000px;margin:0 auto;padding:20px 16px 48px}
h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:28px 0 8px;border-bottom:1px solid var(--line);padding-bottom:4px}
h3{font-size:15px;margin:18px 0 6px}
.sub{color:var(--mut);margin:0 0 12px}
.warn{background:var(--card);border-left:4px solid var(--inc);padding:8px 12px;margin:10px 0}
.bad{border-left-color:var(--fail)}
table{border-collapse:collapse;width:100%;margin:6px 0 10px}
th,td{text-align:left;vertical-align:top;padding:5px 8px;border-bottom:1px solid var(--line)}
th{color:var(--mut);font-weight:600}
td.k{white-space:nowrap;color:var(--mut);width:1%}
.v{font-weight:700;white-space:nowrap}.v.PASS{color:var(--pass)}.v.FAIL{color:var(--fail)}.v.INCONCLUSIVE{color:var(--inc)}
.v.REPORTED,.v.N\/A{color:var(--mut)}
ul{margin:4px 0 8px 18px;padding:0}li{margin:2px 0}
code,pre{font:12px/1.4 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
pre{background:var(--card);padding:10px;overflow-x:auto;white-space:pre-wrap;word-break:break-word}
details{margin:6px 0}summary{cursor:pointer;color:var(--mut)}
figure.chart{margin:14px 0;padding:0}figcaption{font-weight:600;margin-bottom:2px}
.legend{display:flex;flex-wrap:wrap;gap:4px 14px;color:var(--mut);font-size:12px;margin:2px 0 4px}
.legend span{display:inline-flex;align-items:center;gap:5px}
.sw{display:inline-block;width:16px;height:0;border-top:2px solid}
figure.chart>svg{width:100%;height:auto;display:block}
.grid{stroke:var(--line);stroke-width:1}.frame{fill:none;stroke:var(--line)}
.tick,.axis,.hl{fill:var(--mut);font-size:11px}
.ln{fill:none;stroke-width:1.6;stroke-linejoin:round}.dash{stroke-dasharray:5 4}
.s1{stroke:var(--s1);border-color:var(--s1)}.s2{stroke:var(--s2);border-color:var(--s2)}.s3{stroke:var(--s3);border-color:var(--s3)}
.s4{stroke:var(--s4);border-color:var(--s4)}.s5{stroke:var(--s5);border-color:var(--s5)}.s6{stroke:var(--s6);border-color:var(--s6)}
.s7{stroke:var(--s7);border-color:var(--s7)}.sw.dash{border-top-style:dashed}
.w{stroke:var(--fg);border-color:var(--fg)}
.lim{stroke:var(--fail);stroke-dasharray:6 4;stroke-width:1.2}.thr{stroke:var(--mut);stroke-dasharray:3 3;stroke-width:1}
.vl{stroke:var(--mut);stroke-dasharray:2 3}
.band{fill:var(--band)}.steady{fill:var(--band2)}.ph{fill:var(--ph)}.ph2{fill:rgba(0,0,0,0)}
`

func esc(s string) string { return html.EscapeString(s) }

func verdictCell(v spec.Verdict) string {
	return fmt.Sprintf(`<span class="v %s">%s</span>`, esc(string(v)), esc(string(v)))
}

// HTML renders the report of a recomputed bundle.
func HTML(l *bundle.Loaded, r *Result) string {
	var b strings.Builder
	m := r.Meta
	title := "n5-ectest report"
	b.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	fmt.Fprintf(&b, "<title>%s</title><style>%s</style></head><body><main>", esc(title), css)
	fmt.Fprintf(&b, "<h1>%s</h1><p class=\"sub\">%s · %s", esc(title), esc(filepath.Base(l.Dir)), esc(orText(m.Command, "?")))
	if m.Channel > 0 {
		fmt.Fprintf(&b, " pwm%d", m.Channel)
	}
	fmt.Fprintf(&b, " · TESTSPEC %s · %s</p>", esc(orText(m.SpecVersion, "?")), esc(orText(m.Started, "")))
	if strings.HasPrefix(m.Mode, "dry-run") {
		b.WriteString(`<div class="warn">Dry run against the simulated EC: not a result under TESTSPEC (§1 rule 7).</div>`)
	}
	if tr, _ := m.Provenance["test_root"].(bool); tr {
		b.WriteString(`<div class="warn">Run against a fake host tree (N5ECTEST_ROOT): not a result under TESTSPEC.</div>`)
	}
	if len(l.SumProblems) > 0 {
		fmt.Fprintf(&b, `<div class="warn bad">SHA256SUMS: %s</div>`, esc(strings.Join(l.SumProblems, "; ")))
	} else {
		b.WriteString(`<p class="sub">SHA256SUMS verified: every file of the bundle matches.</p>`)
	}
	if len(r.Mismatches) > 0 {
		var ms []string
		for _, x := range r.Mismatches {
			ms = append(ms, fmt.Sprintf("%s: %s %s, recomputed %s", x.ID, x.Source(), x.Stored, x.Recomputed))
		}
		fmt.Fprintf(&b, `<div class="warn bad">Recomputed results differ from the stored ones: %s</div>`, esc(strings.Join(ms, "; ")))
	} else if len(r.Tests) > 0 {
		b.WriteString(`<p class="sub">Every verdict (with its runs and sub-runs) recomputed from samples.csv, events.log and meta.json equals verdict.json; G3/G4 recomputed from the gate window equal meta.json.</p>`)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, `<div class="warn">%s</div>`, esc(n))
	}
	if m.Aborted != "" {
		fmt.Fprintf(&b, `<div class="warn bad">Run aborted: %s</div>`, esc(m.Aborted))
	}
	if len(m.RestoreFailed) > 0 {
		fmt.Fprintf(&b, `<div class="warn bad">Restore failed: %s</div>`, esc(strings.Join(m.RestoreFailed, "; ")))
	}

	// verdicts
	if len(r.Tests) > 0 || len(r.Stored.Tests) > 0 {
		b.WriteString("<h2>Verdicts</h2><table><tr><th>Test</th><th>Verdict</th><th>Reasons and numbers</th></tr>")
		tests := r.Tests
		if len(tests) == 0 {
			tests = r.Stored.Tests
		}
		for _, t := range tests {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>", esc(t.ID), verdictCell(t.Verdict))
			if len(t.Reasons) > 0 {
				// every reason: T2/T3 list one per sub-run (3 runs x H/M)
				b.WriteString("<ul>")
				for _, x := range t.Reasons {
					fmt.Fprintf(&b, "<li>%s</li>", esc(x))
				}
				b.WriteString("</ul>")
			}
			if t.ID == "T1" {
				t1Table(&b, t, l.Samples)
			}
			for _, c := range t.Criteria {
				met := "undecided"
				if c.Met != nil {
					met = map[bool]string{true: "met", false: "not met"}[*c.Met]
				}
				fmt.Fprintf(&b, "<div><b>%s</b>: %s", esc(c.Name), esc(met))
				if c.Detail != "" {
					fmt.Fprintf(&b, " — %s", esc(c.Detail))
				}
				b.WriteString("</div>")
			}
			if t.ID == "T2" && t.Values != nil {
				if raw, err := json.Marshal(t.Values); err == nil {
					fmt.Fprintf(&b, "<div><code>%s</code></div>", esc(string(raw)))
				}
			}
			b.WriteString("</td></tr>")
		}
		b.WriteString("</table>")
	}

	// gates
	if len(m.Gates) > 0 {
		b.WriteString("<h2>Gates</h2><table><tr><th>Gate</th><th>Result</th><th>Detail</th></tr>")
		for _, g := range m.Gates {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>", esc(g.ID), verdictCell(spec.Verdict(g.Result)), esc(orText(g.Detail, g.Condition)))
		}
		b.WriteString("</table>")
	}

	charts(&b, l, r)
	logSection(&b, r)

	// provenance
	b.WriteString("<h2>Provenance</h2><table>")
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "<tr><td class=\"k\">%s</td><td>%s</td></tr>", esc(k), esc(v))
		}
	}
	p := m.Provenance
	str := func(path ...string) string {
		var cur any = p
		for _, k := range path {
			mm, ok := cur.(map[string]any)
			if !ok {
				return ""
			}
			cur = mm[k]
		}
		switch x := cur.(type) {
		case string:
			return x
		case float64:
			return fmtNum(x)
		case nil:
			return ""
		}
		raw, _ := json.Marshal(cur)
		return string(raw)
	}
	row("tool", strings.TrimSpace(str("tool")+" "+str("version")+" commit "+str("commit")+" "+str("go_version")))
	row("driver", strings.TrimSpace(str("driver", "module")+" "+str("driver", "version")+", srcversion "+str("driver", "srcversion")+", taint "+str("driver", "taint")))
	row("driver parameters", str("driver", "parameters"))
	row("DKMS package", str("driver", "dkms_package_version"))
	row("kernel", str("kernel", "release")+" "+str("kernel", "version")+", taint "+str("kernel", "taint"))
	d := m.Identity.DMI
	row("DMI", fmt.Sprintf("%s / %s (board version %s), vendor %s", d.Product, d.Board, d.BoardVersion, d.SysVendor))
	row("BIOS / EC", fmt.Sprintf("BIOS %s (%s, release %s), EC firmware %s", d.BIOS, d.BIOSDate, d.BIOSRelease, d.EC))
	row("OS", str("os", "id")+" "+str("os", "version_id"))
	row("lockdown / container", orText(str("lockdown"), "?")+" / "+orText(str("container"), "none"))
	row("boot", orText(str("boot_type"), "not stated")+", uptime "+str("uptime_s")+" s, boot id "+str("boot_id"))
	row("mode", m.Mode)
	row("hardware status", m.Identity.HWStatus.Describe())
	if len(m.RestoreValues) > 0 {
		var rv []string
		for _, k := range SortedKeys(m.RestoreValues) {
			rv = append(rv, "pwm"+k+"="+m.RestoreValues[k])
		}
		row("restore values", strings.Join(rv, ", "))
	}
	row("restore warnings", strings.Join(m.RestoreWarn, "; "))
	b.WriteString("</table>")
	b.WriteString("<h3>spec_deviations</h3>")
	if len(m.SpecDeviations) == 0 {
		b.WriteString("<p class=\"sub\">none</p>")
	} else {
		b.WriteString("<ul>")
		for _, x := range m.SpecDeviations {
			fmt.Fprintf(&b, "<li>%s</li>", esc(x))
		}
		b.WriteString("</ul>")
	}

	// events
	fmt.Fprintf(&b, "<h2>Events</h2><details><summary>%d line(s) of events.log</summary><pre>", len(l.Events))
	for _, e := range l.Events {
		b.WriteString(esc(e) + "\n")
	}
	b.WriteString("</pre></details>")
	b.WriteString("<p class=\"sub\">Generated by n5-ectest report from the bundle files; the verdicts above are recomputed, not copied.</p>")
	b.WriteString("</main></body></html>\n")
	return b.String()
}

// t1Table shows the numbers behind T1, also for a PASS: per step the
// written duty, the DCR range and the mean of the own tach (last 3
// samples), and the tach noise at rest.
func t1Table(b *strings.Builder, t spec.Result, smp []spec.Sample) {
	steps, ok := t.Values.([]spec.T1Step)
	if !ok || len(steps) == 0 {
		return
	}
	b.WriteString("<table><tr><th>Step (written)</th><th>DCR min–max (judged samples)</th><th>own tach, mean of the last 3 samples</th><th>not judged (settle time)</th></tr>")
	for _, st := range steps {
		lo, hi := -1, -1
		judged := func(i int) bool { return i >= len(st.T) || spec.Judged(t.Channel, st.T[i]) }
		var vals []int
		for i, v := range st.DCR {
			if judged(i) {
				vals = append(vals, v)
			}
		}
		for i, v := range st.DCR4 {
			if judged(i) {
				vals = append(vals, v)
			}
		}
		for _, v := range vals {
			if v < 0 {
				continue
			}
			if lo < 0 || v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		dcr := "–"
		if lo >= 0 {
			dcr = fmt.Sprintf("%d–%d", lo, hi)
		}
		own := "–"
		if t.Channel >= 1 && t.Channel <= 3 && st.OwnRPM > 0 {
			own = fmt.Sprintf("%.0f rpm", st.OwnRPM)
		}
		fmt.Fprintf(b, "<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td></tr>", st.Duty, dcr, own, esc(orText(strings.Join(st.NotJudged, "; "), "–")))
	}
	b.WriteString("</table>")
	var rest []string
	for f := 1; f <= 3; f++ {
		lo, hi, sum, cnt := -1, -1, 0, 0
		for _, s := range smp {
			if s.Phase != spec.PhaseT1Rest || s.Fan[f-1] < 0 {
				continue
			}
			v := s.Fan[f-1]
			if lo < 0 || v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
			sum += v
			cnt++
		}
		if cnt > 0 {
			rest = append(rest, fmt.Sprintf("fan%d mean %d rpm, spread %d rpm", f, sum/cnt, hi-lo))
		}
	}
	if len(rest) > 0 {
		fmt.Fprintf(b, "<div>at rest (%s): %s</div>", esc(spec.PhaseT1Rest), esc(strings.Join(rest, "; ")))
	}
}

func orText(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// phaseBands shades alternate phases of a run.
func phaseBands(smp []spec.Sample) []band {
	var out []band
	alt := false
	n := math.NaN()
	for i := 0; i < len(smp); {
		j := i
		for j+1 < len(smp) && smp[j+1].Phase == smp[i].Phase {
			j++
		}
		end := smp[j].TS
		if j+1 < len(smp) {
			end = smp[j+1].TS
		}
		cls := "ph2"
		if alt {
			cls = "ph"
		}
		out = append(out, band{x0: smp[i].TS, x1: end, y0: n, y1: n, class: cls, title: smp[i].Phase})
		alt = !alt
		i = j + 1
	}
	return out
}

// timeAxis picks the x unit of a run: seconds, hours for runs over 2 h,
// days for runs over 3 days (log runs).
func timeAxis(smp []spec.Sample) (div float64, unit, label string) {
	span := smp[len(smp)-1].TS - smp[0].TS
	switch {
	case span > 3*86400:
		return 86400, "d", "t (days)"
	case span > 2*3600:
		return 3600, "h", "t (hours)"
	}
	return 1, "s", "t (s)"
}

func charts(b *strings.Builder, l *bundle.Loaded, r *Result) {
	smp := l.Samples
	if len(smp) < 2 {
		return
	}
	div, unit, xl := timeAxis(smp)
	unitText := map[string]string{"s": "seconds", "h": "hours", "d": "days"}[unit]
	fmt.Fprintf(b, "<h2>Run</h2><p class=\"sub\">x: %s since the start of the run; shaded: phases (hover for the name). Long series are reduced to min/max per pixel column.</p>", unitText)
	bands := phaseBands(smp)
	for i := range bands {
		bands[i].x0, bands[i].x1 = bands[i].x0/div, bands[i].x1/div
	}
	n := math.NaN()
	c := newChart("Duty registers DCR1–5 (I2EC 0x1803–0x1807) and written value", xl, "duty (0–255)")
	c.ymin, c.ymax = 0, 260
	c.bands = bands
	names := []string{"DCR1 CPU", "DCR2 SSD", "DCR3 HDD", "DCR4 HDD", "DCR5 PCIe"}
	for i := 0; i < 5; i++ {
		var pts []pt
		for _, s := range smp {
			y := n
			if s.DCR[i] >= 0 {
				y = float64(s.DCR[i])
			}
			pts = append(pts, pt{s.TS / div, y})
		}
		// DCR4 follows DCR3 on the HDD group: dashed, so DCR3 stays visible
		c.lines = append(c.lines, line{name: names[i], class: fmt.Sprintf("s%d", i+1), pts: pts, dash: i == 3})
	}
	var wpts []pt
	for _, s := range smp {
		y := n
		if s.Written >= 0 {
			y = float64(s.Written)
		}
		wpts = append(wpts, pt{s.TS / div, y})
	}
	c.lines = append(c.lines, line{name: "written", class: "w", pts: wpts, dash: true, step: true})
	b.WriteString(c.svg())

	f := newChart("Tachs fan1–3", xl, "rpm")
	f.ymin = 0
	f.bands = bands
	for i, nm := range []string{"fan1 CPU", "fan2 SSD", "fan3 HDD"} {
		var pts []pt
		for _, s := range smp {
			y := n
			if s.Fan[i] >= 0 {
				y = float64(s.Fan[i])
			}
			pts = append(pts, pt{s.TS / div, y})
		}
		f.lines = append(f.lines, line{name: nm, class: fmt.Sprintf("s%d", i+1), pts: pts})
	}
	b.WriteString(f.svg())

	t := newChart("Temperatures with the limits of TESTSPEC §4", xl, "°C")
	t.bands = bands
	for i, s := range []struct {
		name string
		get  func(spec.Sample) float64
	}{{"Tctl", func(x spec.Sample) float64 { return x.Tctl }}, {"NVMe max", func(x spec.Sample) float64 { return x.NVMeMax }},
		{"HDD max", func(x spec.Sample) float64 { return x.HDDMax }}, {"EC CPU 0x09", func(x spec.Sample) float64 { return x.ECCPU }},
		{"EC system 0x04", func(x spec.Sample) float64 { return x.ECSys }}, {"EC board 0x05", func(x spec.Sample) float64 { return x.ECBoard }},
		{"EC ambient 0x06", func(x spec.Sample) float64 { return x.ECAmb }}} {
		var pts []pt
		for _, x := range smp {
			pts = append(pts, pt{x.TS / div, s.get(x)})
		}
		t.lines = append(t.lines, line{name: s.name, class: fmt.Sprintf("s%d", i+1), pts: pts})
	}
	t.hlines = []hline{{spec.LimitTctlC, "Tctl 85", "lim"}, {spec.LimitNVMeC, "NVMe 70", "lim"}, {spec.LimitHDDC, "HDD 50", "lim"}}
	b.WriteString(t.svg())

	if len(r.SubRuns) > 0 {
		b.WriteString("<h2>T2/T3 sub-runs</h2><p class=\"sub\">x: seconds after the auto command. Bands: written value ±2 (T2 FAIL band, shaded) and ±16 (T2 leave threshold, dashed); A: the auto value per register (|W−A| &lt; 16 makes a sub-run INCONCLUSIVE); marks at 60 s (T2) and at the T3 limit; green: first steady 120-s window (T3).</p>")
		for _, sr := range r.SubRuns {
			title := fmt.Sprintf("run %d %s: written %d — T2 %s, T3 %s", sr.Rep, sr.Label, sr.Written, sr.Eval.T2, sr.Eval.T3)
			sc := newChart(title, "s after auto", "duty")
			limit := spec.T3LimitS(r.Meta.Channel)
			sc.xmin, sc.xmax = 0, float64(limit+spec.SettleHoldS)
			w := float64(sr.Written)
			sc.bands = append(sc.bands, band{x0: 0, x1: sc.xmax, y0: w - spec.HoldCounts, y1: w + spec.HoldCounts, class: "band", title: "written ±2"})
			if sr.Eval.SettleAtS != nil && sr.Eval.FinalValue != nil {
				sc.bands = append(sc.bands, band{x0: *sr.Eval.SettleAtS, x1: *sr.Eval.SettleAtS + spec.SettleHoldS, y0: n, y1: n, class: "steady", title: fmt.Sprintf("steady at %d", *sr.Eval.FinalValue)})
			}
			sc.hlines = []hline{{w + spec.LeaveCounts, "W+16", "thr"}, {w - spec.LeaveCounts, "W−16", "thr"}}
			sc.vlines = []vline{{spec.LeaveWithinS, "60 s"}, {float64(limit), fmt.Sprintf("%d s", limit)}}
			var regs []int
			for k := range sr.Regs {
				regs = append(regs, k)
			}
			sort.Ints(regs)
			lo, hi := w, w
			for i, reg := range regs {
				var pts []pt
				for _, x := range sr.Regs[reg] {
					pts = append(pts, pt{x.T, float64(x.DCR)})
					lo, hi = math.Min(lo, float64(x.DCR)), math.Max(hi, float64(x.DCR))
				}
				sc.lines = append(sc.lines, line{name: fmt.Sprintf("DCR%d", reg), class: fmt.Sprintf("s%d", i+1), pts: pts, dash: reg == 4})
				if a, ok := sr.Auto[reg]; ok {
					sc.hlines = append(sc.hlines, hline{float64(a), fmt.Sprintf("A DCR%d=%d", reg, a), "thr"})
					lo, hi = math.Min(lo, float64(a)), math.Max(hi, float64(a))
				}
			}
			sc.ymin, sc.ymax = math.Max(0, lo-24), math.Min(270, hi+24)
			b.WriteString(sc.svg())
		}
	}
}

func logSection(b *strings.Builder, r *Result) {
	s := r.Log
	if s == nil {
		return
	}
	fmt.Fprintf(b, "<h2>Long-term log (F8b K1–K7)</h2><p class=\"sub\">%d samples, coverage %.2f %% (planned downtime excluded), %.1f MB, logger restarts %d. Explained = annotated by the operator (K1/K7 per event only). The tool computes no F8b verdict.</p>", s.Samples, s.CoveragePct, s.DataMB, s.Restarts)
	b.WriteString("<table><tr><th>Criterion</th><th>Total</th><th>Explained</th><th>Unexplained</th><th></th></tr>")
	for _, k := range logger.Keys {
		c := s.Keys[k]
		fmt.Fprintf(b, "<tr><td>%s</td><td>%d</td><td>%d</td><td>%d</td><td>%s</td></tr>", k, c["total"], c["explained"], c["unexplained"], esc(logger.KeyText[k]))
	}
	b.WriteString("</table>")
	if len(s.OpenEpisodes) > 0 {
		fmt.Fprintf(b, "<p>Open episodes at the end: %s</p>", esc(strings.Join(s.OpenEpisodes, ", ")))
	}
	if len(r.LogOpen) > 0 {
		fmt.Fprintf(b, "<details open><summary>%d unexplained event(s)</summary><pre>", len(r.LogOpen))
		for i, e := range r.LogOpen {
			if i == 200 {
				b.WriteString("…\n")
				break
			}
			b.WriteString(esc(e.Line) + "\n")
		}
		b.WriteString("</pre></details>")
	}
}
