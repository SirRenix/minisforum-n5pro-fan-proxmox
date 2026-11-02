package report

import (
	"fmt"
	"html"
	"math"
	"strings"
)

// A minimal SVG line chart: no script, colours through CSS classes so the
// page's light/dark theme applies.

type pt struct{ x, y float64 }

type line struct {
	name  string
	class string // s1..s6, w (written), lim
	pts   []pt   // NaN y = gap
	dash  bool
	step  bool
}

type hline struct {
	y     float64
	label string
	class string
}

type vline struct {
	x     float64
	label string
}

type band struct {
	x0, x1 float64
	y0, y1 float64 // NaN = full height
	class  string
	title  string
}

type chart struct {
	title, xlabel, ylabel  string
	xmin, xmax, ymin, ymax float64
	lines                  []line
	hlines                 []hline
	vlines                 []vline
	bands                  []band
}

const (
	cw, ch           = 920.0, 270.0
	ml, mr, mt, mb   = 58.0, 14.0, 12.0, 38.0
	plotW, plotH     = cw - ml - mr, ch - mt - mb
	maxPointsPerLine = 2 * int(plotW)
)

// niceTicks returns about n round tick values covering [lo, hi].
func niceTicks(lo, hi float64, n int) []float64 {
	if !(hi > lo) {
		return []float64{lo}
	}
	raw := (hi - lo) / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := mag
	for _, f := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= f*mag {
			step = f * mag
			break
		}
	}
	var out []float64
	for v := math.Ceil(lo/step) * step; v <= hi+step*1e-9; v += step {
		out = append(out, v)
	}
	return out
}

func fmtNum(v float64) string {
	if math.Abs(v-math.Round(v)) < 1e-9 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// reduce keeps, per pixel column, the first point and the min and max of
// the column (in their original order) once a line has more points than
// the plot has pixels (design §9: min/max per pixel column).
func reduce(pts []pt, xmin, xmax float64) []pt {
	if len(pts) <= maxPointsPerLine || !(xmax > xmin) {
		return pts
	}
	var out []pt
	col := -1
	var lo, hi, first int
	flush := func() {
		if col < 0 {
			return
		}
		idx := []int{first, lo, hi}
		seen := map[int]bool{}
		for _, a := range []int{0, 1, 2} {
			for b := a + 1; b < 3; b++ {
				if idx[b] < idx[a] {
					idx[a], idx[b] = idx[b], idx[a]
				}
			}
		}
		for _, i := range idx {
			if !seen[i] {
				seen[i] = true
				out = append(out, pts[i])
			}
		}
	}
	for i, p := range pts {
		c := int((p.x - xmin) / (xmax - xmin) * plotW)
		if math.IsNaN(p.y) {
			flush()
			col = -1
			out = append(out, p)
			continue
		}
		if c != col {
			flush()
			col, lo, hi, first = c, i, i, i
			continue
		}
		if p.y < pts[lo].y {
			lo = i
		}
		if p.y > pts[hi].y {
			hi = i
		}
	}
	flush()
	return out
}

func (c *chart) autoRange() {
	if !math.IsNaN(c.xmin) && !math.IsNaN(c.xmax) && !math.IsNaN(c.ymin) && !math.IsNaN(c.ymax) {
		return
	}
	xl, xh, yl, yh := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, l := range c.lines {
		for _, p := range l.pts {
			if math.IsNaN(p.y) {
				continue
			}
			xl, xh = math.Min(xl, p.x), math.Max(xh, p.x)
			yl, yh = math.Min(yl, p.y), math.Max(yh, p.y)
		}
	}
	for _, h := range c.hlines {
		yl, yh = math.Min(yl, h.y), math.Max(yh, h.y)
	}
	if math.IsInf(xl, 1) {
		xl, xh, yl, yh = 0, 1, 0, 1
	}
	if math.IsNaN(c.xmin) {
		c.xmin = xl
	}
	if math.IsNaN(c.xmax) {
		c.xmax = xh
	}
	if c.xmax <= c.xmin {
		c.xmax = c.xmin + 1
	}
	if math.IsNaN(c.ymin) {
		c.ymin = yl - (yh-yl)*0.05
	}
	if math.IsNaN(c.ymax) {
		c.ymax = yh + (yh-yl)*0.05
	}
	if c.ymax <= c.ymin {
		c.ymin, c.ymax = c.ymin-1, c.ymax+1
	}
}

func (c *chart) px(x float64) float64 { return ml + (x-c.xmin)/(c.xmax-c.xmin)*plotW }
func (c *chart) py(y float64) float64 { return mt + plotH - (y-c.ymin)/(c.ymax-c.ymin)*plotH }

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// svg renders the chart (with an HTML legend above it).
func (c chart) svg() string {
	c.autoRange()
	var b strings.Builder
	fmt.Fprintf(&b, `<figure class="chart"><figcaption>%s</figcaption><div class="legend">`, html.EscapeString(c.title))
	for _, l := range c.lines {
		if l.name == "" {
			continue
		}
		d := ""
		if l.dash {
			d = " dash"
		}
		fmt.Fprintf(&b, `<span><i class="sw %s%s"></i>%s</span>`, l.class, d, html.EscapeString(l.name))
	}
	b.WriteString(`</div>`)
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s" preserveAspectRatio="xMidYMid meet">`, cw, ch, html.EscapeString(c.title))
	for _, bd := range c.bands {
		x0, x1 := clamp(c.px(bd.x0), ml, ml+plotW), clamp(c.px(bd.x1), ml, ml+plotW)
		y0, y1 := mt, mt+plotH
		if !math.IsNaN(bd.y0) && !math.IsNaN(bd.y1) {
			y0, y1 = clamp(c.py(bd.y1), mt, mt+plotH), clamp(c.py(bd.y0), mt, mt+plotH)
		}
		if x1 <= x0 || y1 <= y0 {
			continue
		}
		fmt.Fprintf(&b, `<rect class="%s" x="%.1f" y="%.1f" width="%.1f" height="%.1f"><title>%s</title></rect>`, bd.class, x0, y0, x1-x0, y1-y0, html.EscapeString(bd.title))
	}
	for _, t := range niceTicks(c.ymin, c.ymax, 5) {
		y := c.py(t)
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/><text class="tick" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, ml, y, ml+plotW, y, ml-6, y+4, fmtNum(t))
	}
	for _, t := range niceTicks(c.xmin, c.xmax, 8) {
		x := c.px(t)
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/><text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x, mt, x, mt+plotH, x, mt+plotH+16, fmtNum(t))
	}
	fmt.Fprintf(&b, `<rect class="frame" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, ml, mt, plotW, plotH)
	fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, ml+plotW/2, ch-4, html.EscapeString(c.xlabel))
	fmt.Fprintf(&b, `<text class="axis" transform="translate(14 %.1f) rotate(-90)" text-anchor="middle">%s</text>`, mt+plotH/2, html.EscapeString(c.ylabel))
	for _, h := range c.hlines {
		if h.y < c.ymin || h.y > c.ymax {
			continue
		}
		y := c.py(h.y)
		fmt.Fprintf(&b, `<line class="%s" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/><text class="hl" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, h.class, ml, y, ml+plotW, y, ml+plotW-4, y-3, html.EscapeString(h.label))
	}
	for _, v := range c.vlines {
		if v.x < c.xmin || v.x > c.xmax {
			continue
		}
		x := c.px(v.x)
		fmt.Fprintf(&b, `<line class="vl" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/><text class="hl" x="%.1f" y="%.1f">%s</text>`, x, mt, x, mt+plotH, x+3, mt+11, html.EscapeString(v.label))
	}
	// the lines are drawn in a nested viewport of the plot area: an inner
	// svg clips to its viewport, so a line never leaves the frame (x beyond
	// xmax, a value outside the y range); no clip-path reference needed
	fmt.Fprintf(&b, `<svg class="plot" x="%.1f" y="%.1f" width="%.1f" height="%.1f" viewBox="%.1f %.1f %.1f %.1f" overflow="hidden">`, ml, mt, plotW, plotH, ml, mt, plotW, plotH)
	for _, l := range c.lines {
		pts := reduce(l.pts, c.xmin, c.xmax)
		var seg []string
		emit := func() {
			if len(seg) == 0 {
				return
			}
			cls := "ln " + l.class
			if l.dash {
				cls += " dash"
			}
			if len(seg) == 1 {
				seg = append(seg, seg[0])
			}
			fmt.Fprintf(&b, `<polyline class="%s" points="%s"/>`, cls, strings.Join(seg, " "))
			seg = nil
		}
		prevY := math.NaN()
		for _, p := range pts {
			if math.IsNaN(p.y) {
				emit()
				prevY = math.NaN()
				continue
			}
			x, y := c.px(p.x), c.py(clamp(p.y, c.ymin, c.ymax))
			if l.step && !math.IsNaN(prevY) {
				seg = append(seg, fmt.Sprintf("%.1f,%.1f", x, prevY))
			}
			seg = append(seg, fmt.Sprintf("%.1f,%.1f", x, y))
			prevY = y
		}
		emit()
	}
	b.WriteString(`</svg></svg></figure>`)
	return b.String()
}
