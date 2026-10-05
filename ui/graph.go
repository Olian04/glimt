package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/format"
	"github.com/Olian04/glimt/term"
)

const rateKey = "\x00rate"

type series struct {
	key, label, unit string
	pts              []float64
	from, to         int // x-axis range labels (line numbers, or seconds)
	stat             *analysis.FieldStat
}

func (a *App) seriesList(r *analysis.Result, info analysis.Info) []series {
	var out []series
	for _, f := range r.NumericFields() {
		win := f.Window()
		pts := make([]float64, len(win))
		for i, p := range win {
			pts[i] = p.V
		}
		out = append(out, series{key: f.Key, label: f.Key, unit: f.Unit, pts: pts,
			from: win[0].Line + 1, to: win[len(win)-1].Line + 1, stat: f})
	}
	if rateMeaningful(info) {
		pts := make([]float64, len(info.PerSec))
		for i, n := range info.PerSec {
			pts[i] = float64(n)
		}
		if !info.EOF && len(pts) > 1 {
			pts = pts[:len(pts)-1] // current second is incomplete
		}
		out = append(out, series{key: rateKey, label: "lines/s", pts: pts, from: 1, to: len(pts)})
	}
	return out
}

func (a *App) graphView(r *analysis.Result, info analysis.Info, h int) []string {
	ss := a.seriesList(r, info)
	if len(ss) == 0 {
		return []string{"", "  " + C(format.CNull, "no numeric fields")}
	}
	sel := -1
	for i, s := range ss {
		if s.key == a.graphKey {
			sel = i
		}
	}
	if sel < 0 {
		sel = defaultSeries(ss, r.Format.GraphField())
	}
	a.graphKey = ss[sel].key
	s := ss[sel]

	// series picker
	var b strings.Builder
	b.WriteString(C(format.CPunct, " ◀ "))
	for i, x := range ss {
		lbl := " " + x.label + " "
		if i == sel {
			b.WriteString("\x1b[1m\x1b[4m" + C(format.CAccent, lbl) + "\x1b[0m")
		} else {
			b.WriteString(C(format.CNull, lbl))
		}
	}
	b.WriteString(C(format.CPunct, " ▶"))
	out := []string{term.Truncate(b.String(), a.W)}

	// headline stats
	var stats string
	if s.stat != nil {
		stats = " " + numSummary(s.stat) + C(format.CNull, fmt.Sprintf("   n=%s", commas(s.stat.NumN)))
		if last := lastNum(s.pts); !math.IsNaN(last) {
			stats += C(format.CNull, "  last ") + C(format.CNum, humanNum(last))
		}
	} else {
		lo, hi, mean := rangeOf(s.pts)
		stats = fmt.Sprintf(" min %s  avg %s  max %s lines/s over %ds", humanNum(lo), humanNum(mean), humanNum(hi), len(s.pts))
	}
	out = append(out, term.Truncate(stats, a.W), "")
	chartH := h - len(out) - 2
	if chartH < 2 {
		return out
	}
	xlabel := "line"
	if s.key == rateKey {
		xlabel = "seconds"
	}
	out = append(out, plot(s.pts, a.W, chartH, s.from, s.to, xlabel)...)
	return out
}

// defaultSeries: the format's choice, else the most varied field that isn't
// a monotonic counter (sequence numbers, ids and timestamps graph as a
// straight line and say nothing).
func defaultSeries(ss []series, pref string) int {
	best, bestScore := 0, -1
	for i, s := range ss {
		if s.stat == nil {
			continue
		}
		if s.key == pref {
			return i
		}
		score := s.stat.Distinct()
		if monotonic(s.pts) {
			score = 0
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

func monotonic(p []float64) bool {
	for i := 1; i < len(p); i++ {
		if p[i] < p[i-1] {
			return false
		}
	}
	return len(p) > 2
}

func lastNum(p []float64) float64 {
	if len(p) == 0 {
		return math.NaN()
	}
	return p[len(p)-1]
}

func rangeOf(p []float64) (lo, hi, mean float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	n := 0
	for _, v := range p {
		if math.IsNaN(v) {
			continue
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
		mean += v
		n++
	}
	if n == 0 {
		return math.NaN(), math.NaN(), math.NaN()
	}
	return lo, hi, mean / float64(n)
}

// plot draws a braille line chart: a y-axis, the plot area, and an x-axis
// labelled from..to. More points than pixels are drawn as a mean line over a
// dim min/max envelope.
func plot(pts []float64, w, h, from, to int, xlabel string) []string {
	const labelW = 9
	cw := max(4, w-labelW-1)
	ch := max(1, h-1)
	pw, ph := cw*2, ch*4
	lo, hi, _ := rangeOf(pts)
	if math.IsNaN(lo) {
		lo, hi = 0, 1
	}
	if hi == lo {
		lo, hi = lo-1, hi+1
	}
	pad := (hi - lo) * 0.05
	lo, hi = lo-pad, hi+pad
	if lo < 0 && rangeMin(pts) >= 0 {
		lo = 0
	}
	grid := make([][]uint8, ch)
	env := make([][]uint8, ch) // min/max envelope, drawn dim
	for i := range grid {
		grid[i] = make([]uint8, cw)
		env[i] = make([]uint8, cw)
	}
	dot := func(g [][]uint8, x, y int) {
		if x < 0 || x >= pw || y < 0 || y >= ph {
			return
		}
		// y=0 is the bottom
		row := ch - 1 - y/4
		dy := 3 - y%4
		bits := [2][4]uint8{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}
		g[row][x/2] |= bits[x%2][dy]
	}
	set := func(x, y int) { dot(grid, x, y) }
	ypx := func(v float64) int { return int(math.Round((v - lo) / (hi - lo) * float64(ph-1))) }
	line := func(x0, y0, x1, y1 int) {
		dx, dy := abs(x1-x0), -abs(y1-y0)
		sx, sy := sign(x1-x0), sign(y1-y0)
		e := dx + dy
		for {
			set(x0, y0)
			if x0 == x1 && y0 == y1 {
				return
			}
			e2 := 2 * e
			if e2 >= dy {
				e += dy
				x0 += sx
			}
			if e2 <= dx {
				e += dx
				y0 += sy
			}
		}
	}
	n := len(pts)
	if n <= pw {
		px, py := -1, -1
		for i, v := range pts {
			x := 0
			if n > 1 {
				x = i * (pw - 1) / (n - 1)
			}
			if math.IsNaN(v) {
				px = -1
				continue
			}
			y := ypx(v)
			if px >= 0 {
				line(px, py, x, y)
			} else {
				set(x, y)
			}
			px, py = x, y
		}
	} else {
		// more points than pixels: mean line on top of a dim min/max envelope
		px, py := -1, -1
		for x := 0; x < pw; x++ {
			a, b := x*n/pw, (x+1)*n/pw
			bl, bh, m := rangeOf(pts[a:b])
			if math.IsNaN(bl) {
				px = -1
				continue
			}
			for y := ypx(bl); y <= ypx(bh); y++ {
				dot(env, x, y)
			}
			y := ypx(m)
			if px >= 0 {
				line(px, py, x, y)
			} else {
				set(x, y)
			}
			px, py = x, y
		}
	}
	out := make([]string, 0, h+1)
	for i, row := range grid {
		label := ""
		switch i {
		case 0:
			label = humanNum(hi)
		case ch - 1:
			label = humanNum(lo)
		case ch / 2:
			if ch > 4 {
				label = humanNum((hi + lo) / 2)
			}
		}
		var b strings.Builder
		for j, c := range row {
			switch {
			case c != 0:
				b.WriteString("\x1b[38;5;75m" + string(rune(0x2800+int(c|env[i][j]))))
			case env[i][j] != 0:
				b.WriteString("\x1b[38;5;24m" + string(rune(0x2800+int(env[i][j]))))
			default:
				b.WriteRune(0x2800)
			}
		}
		out = append(out, C(format.CNull, fmt.Sprintf("%*s ", labelW-1, label))+C(format.CPunct, "┤")+b.String()+"\x1b[0m")
	}
	out = append(out, strings.Repeat(" ", labelW)+C(format.CPunct, "└"+strings.Repeat("─", cw)))
	left, right, mid := commas(from), commas(to), xlabel
	gap := cw - len(left) - len(right) - term.Width(mid)
	axis := strings.Repeat(" ", labelW+1) + C(format.CNull, left)
	if gap > 2 {
		axis += strings.Repeat(" ", gap/2) + C(format.CNull, mid) + strings.Repeat(" ", gap-gap/2)
	} else {
		axis += strings.Repeat(" ", max(1, cw-len(left)-len(right)))
	}
	out = append(out, axis+C(format.CNull, right))
	return out
}

func rangeMin(p []float64) float64 { lo, _, _ := rangeOf(p); return lo }

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func sign(x int) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return 0
}

func (a *App) keyGraph(k term.Key) {
	d := 0
	switch {
	case k.Kind == term.KLeft || k.Kind == term.KUp || (k.Kind == term.KRune && (k.Rune == 'h' || k.Rune == 'k')):
		d = -1
	case k.Kind == term.KRight || k.Kind == term.KDown || (k.Kind == term.KRune && (k.Rune == 'l' || k.Rune == 'j')):
		d = 1
	}
	if d == 0 {
		return
	}
	st := a.an.Store()
	info := st.Info()
	a.an.Read(func(r *analysis.Result) {
		ss := a.seriesList(r, info)
		for i, s := range ss {
			if s.key == a.graphKey {
				a.graphKey = ss[(i+d+len(ss))%len(ss)].key
				return
			}
		}
	})
}
