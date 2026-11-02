package report

import (
	"math"
	"strings"
	"testing"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
)

// Long series are reduced to min/max per pixel column; the extremes stay.
func TestReduceKeepsExtremes(t *testing.T) {
	var pts []pt
	for i := 0; i < 20000; i++ {
		y := 100.0
		if i == 7777 {
			y = 250
		}
		if i == 12345 {
			y = 3
		}
		pts = append(pts, pt{float64(i), y})
	}
	r := reduce(pts, 0, 19999)
	if len(r) > 3*int(plotW)+3 || len(r) < int(plotW) {
		t.Fatalf("reduced to %d points", len(r))
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, p := range r {
		lo, hi = math.Min(lo, p.y), math.Max(hi, p.y)
	}
	if lo != 3 || hi != 250 {
		t.Fatalf("extremes lost: %v %v", lo, hi)
	}
	for i := 1; i < len(r); i++ {
		if r[i].x < r[i-1].x {
			t.Fatal("order lost")
		}
	}
	short := pts[:10]
	if got := reduce(short, 0, 9); len(got) != 10 {
		t.Fatal("short series reduced")
	}
}

func TestChartSVG(t *testing.T) {
	c := newChart("A & B <x>", "t", "y")
	c.lines = []line{{name: "s", class: "s1", pts: []pt{{0, 1}, {1, math.NaN()}, {2, 3}, {3, 4}}}}
	c.hlines = []hline{{2, "lim", "lim"}}
	c.vlines = []vline{{1.5, "mark"}}
	s := c.svg()
	if strings.Count(s, "<polyline") != 2 || !strings.Contains(s, "A &amp; B &lt;x&gt;") || strings.Contains(s, "<script") {
		t.Fatalf("svg:\n%s", s)
	}
}

// Lines are drawn in a clipping inner viewport (points beyond the x range
// stay inside the frame); long runs get hours or days on the x axis.
func TestChartClipAndTimeAxis(t *testing.T) {
	c := newChart("sub-run", "s", "duty")
	c.xmin, c.xmax = 0, 1020
	c.lines = []line{{name: "DCR3", class: "s1", pts: []pt{{0, 140}, {1200, 87}}}}
	s := c.svg()
	if !strings.Contains(s, `<svg class="plot"`) || !strings.Contains(s, `overflow="hidden"`) || strings.Count(s, "</svg>") != 2 {
		t.Fatalf("no clipping viewport:\n%s", s)
	}
	mk := func(span float64) []spec.Sample {
		return []spec.Sample{{TS: 0}, {TS: span}}
	}
	for span, want := range map[float64]string{600: "t (s)", 3 * 3600: "t (hours)", 7 * 86400: "t (days)"} {
		if _, _, l := timeAxis(mk(span)); l != want {
			t.Errorf("span %.0f s: %s, want %s", span, l, want)
		}
	}
}
