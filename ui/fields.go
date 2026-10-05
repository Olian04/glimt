package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/format"
	"github.com/Olian04/glimt/term"
)

func numSummary(f *analysis.FieldStat) string {
	q := f.Quantiles(0.5, 0.95)
	s := fmt.Sprintf("min %s  p50 %s  avg %s  p95 %s  max %s", humanNum(f.Min), humanNum(q[0]), humanNum(f.Mean()), humanNum(q[1]), humanNum(f.Max))
	if f.Unit != "" {
		s += " " + f.Unit
	}
	return s
}

func topSummary(f *analysis.FieldStat, n int) string {
	if allDistinct(f) {
		ex := f.Top(1)
		e := ""
		if len(ex) > 0 {
			e = term.Truncate(ex[0].Value, 40)
		}
		label := "all distinct · e.g. "
		if f.Overflow {
			label = "high cardinality · e.g. "
		}
		return C(format.CNull, label) + e
	}
	var parts []string
	for _, vc := range f.Top(n) {
		v := vc.Value
		if v == "" {
			v = `""`
		}
		parts = append(parts, fmt.Sprintf("%s %s", term.Truncate(v, 24), C(format.CNull, pct(vc.Count, f.Count))))
	}
	return strings.Join(parts, C(format.CPunct, " · "))
}

func pct(n, d int) string {
	if d == 0 {
		return "0%"
	}
	p := 100 * float64(n) / float64(d)
	if p >= 10 || p == 0 {
		return fmt.Sprintf("%.0f%%", p)
	}
	return fmt.Sprintf("%.1f%%", p)
}

func (a *App) fieldsView(r *analysis.Result, h int) []string {
	fs := r.SortedFields()
	if len(fs) == 0 {
		return []string{"", "  " + C(format.CNull, "no fields yet")}
	}
	a.fieldSel = clamp(a.fieldSel, 0, len(fs)-1)
	keyW := 3
	for _, f := range fs {
		keyW = max(keyW, term.Width(f.Key))
	}
	keyW = min(keyW, 32)
	tableH := min(len(fs)+1, max(4, h/2))
	if len(fs)+1 < h/2 {
		tableH = len(fs) + 1
	}
	rowsH := tableH - 1
	if a.fieldSel < a.fieldTop {
		a.fieldTop = a.fieldSel
	}
	if a.fieldSel >= a.fieldTop+rowsH {
		a.fieldTop = a.fieldSel - rowsH + 1
	}
	head := fmt.Sprintf(" %-*s  %-6s  %7s  %8s  %s", keyW, "KEY", "TYPE", "PRESENT", "DISTINCT", "VALUES")
	out := []string{Bold(C(format.CPunct, head))}
	for i := a.fieldTop; i < len(fs) && i < a.fieldTop+rowsH; i++ {
		f := fs[i]
		distinct := commas(f.Distinct())
		if f.Overflow {
			distinct += "+"
		}
		sum := ""
		if f.Numeric() && f.Distinct() > 12 {
			sum = numSummary(f)
		} else {
			sum = topSummary(f, 4)
		}
		row := fmt.Sprintf(" %s  %-6s  %7s  %8s  ", term.Pad(term.Truncate(f.Key, keyW), keyW), f.Type(), pct(f.Records, r.Matched), distinct)
		if i == a.fieldSel {
			out = append(out, "\x1b[48;5;238m"+strings.ReplaceAll(term.Pad(C(format.CKey, row)+sum, a.W), "\x1b[0m", "\x1b[0m\x1b[48;5;238m")+"\x1b[0m")
		} else {
			out = append(out, C(format.CKey, row)+sum)
		}
	}
	if r.KeysOver {
		out = append(out, C(format.CWarn, fmt.Sprintf(" more than %d distinct keys; extra keys not tracked", len(fs))))
	}
	// detail of the selected field
	f := fs[a.fieldSel]
	detailH := h - len(out) - 1
	if detailH < 3 {
		return out
	}
	title := fmt.Sprintf("── %s ", f.Key)
	out = append(out, C(format.CAccent, title)+C(format.CPunct, strings.Repeat("─", max(0, a.W-term.Width(title)))))
	leftW := a.W
	var right []string
	if f.Numeric() {
		leftW = a.W / 2
		right = numDetail(f, a.W-leftW-2, detailH)
	}
	left := topValues(f, leftW-1, detailH)
	for i := 0; i < detailH; i++ {
		l, rr := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			rr = right[i]
		}
		if right != nil {
			out = append(out, term.Pad(l, leftW)+C(format.CPunct, "│ ")+rr)
		} else {
			out = append(out, l)
		}
	}
	return out
}

// allDistinct: every occurrence has its own value (ids, timestamps).
func allDistinct(f *analysis.FieldStat) bool {
	return f.Count >= 10 && (f.Distinct() == f.Count || f.Overflow)
}

func topValues(f *analysis.FieldStat, w, h int) []string {
	if allDistinct(f) {
		head := fmt.Sprintf(" all %s values distinct; e.g.", commas(f.Distinct()))
		if f.Overflow {
			head = fmt.Sprintf(" more than %s distinct values; e.g.", commas(f.Distinct()))
		}
		out := []string{C(format.CNull, head)}
		for _, t := range f.Top(min(5, h-1)) {
			out = append(out, "  "+term.Truncate(t.Value, w-3))
		}
		return out
	}
	top := f.Top(h - 1)
	out := []string{C(format.CNull, fmt.Sprintf(" top values (%s distinct%s)", commas(f.Distinct()), map[bool]string{true: "+", false: ""}[f.Overflow]))}
	if len(top) == 0 {
		return out
	}
	vw := 4
	for _, t := range top {
		vw = max(vw, term.Width(t.Value))
	}
	vw = min(vw, w/2)
	barW := max(4, w-vw-20)
	mx := top[0].Count
	for _, t := range top {
		v := t.Value
		if v == "" {
			v = `""`
		}
		n := int(math.Round(float64(t.Count) / float64(mx) * float64(barW)))
		bar := C(format.CKey, strings.Repeat("█", n)) + strings.Repeat(" ", barW-n)
		out = append(out, fmt.Sprintf(" %s %s %6s %s", term.Pad(term.Truncate(v, vw), vw), bar, pct(t.Count, f.Count), C(format.CNull, commas(t.Count))))
	}
	return out
}

func numDetail(f *analysis.FieldStat, w, h int) []string {
	q := f.Quantiles(0.5, 0.9, 0.95, 0.99)
	u := ""
	if f.Unit != "" {
		u = " " + f.Unit
	}
	kv := func(k string, v float64) string { return C(format.CNull, k+" ") + C(format.CNum, humanNum(v)) }
	out := []string{
		kv("n", float64(f.NumN)) + "  " + kv("mean", f.Mean()) + "  " + kv("std", f.Std()) + C(format.CNull, u),
		kv("min", f.Min) + "  " + kv("p50", q[0]) + "  " + kv("p90", q[1]) + "  " + kv("p95", q[2]) + "  " + kv("p99", q[3]) + "  " + kv("max", f.Max),
	}
	if n := len(f.Window()); n < f.NumN { // rows evicted: percentiles and histogram are windowed
		out = append(out, C(format.CWarn, fmt.Sprintf("percentiles & histogram over the %s cached values", commas(n))))
	}
	hh := h - len(out) - 1
	if hh >= 2 {
		if s := f.Sorted(); len(s) > 0 {
			out = append(out, histogram(s, s[0], s[len(s)-1], w, hh)...)
		}
	}
	return out
}

// histogram draws vertical bars with block characters, plus an axis row.
func histogram(vals []float64, lo, hi float64, w, h int) []string {
	if len(vals) == 0 || w < 8 {
		return nil
	}
	bins := max(1, min(w, 60))
	integral := true
	for _, v := range vals {
		if v != math.Trunc(v) {
			integral = false
			break
		}
	}
	if integral && hi-lo+1 < float64(bins) {
		bins = int(hi-lo) + 1 // one bar per integer value
		hi = lo + float64(bins)
	} else if hi <= lo {
		hi = lo + 1
	}
	counts := make([]int, bins)
	for _, v := range vals {
		b := int((v - lo) / (hi - lo) * float64(bins))
		counts[clamp(b, 0, bins-1)]++
	}
	mx := 1
	for _, c := range counts {
		mx = max(mx, c)
	}
	rows := h - 1
	out := make([]string, 0, h)
	blocks := []rune(" ▁▂▃▄▅▆▇█")
	for r := rows - 1; r >= 0; r-- {
		var b strings.Builder
		for _, c := range counts {
			level := float64(c) / float64(mx) * float64(rows*8)
			cell := level - float64(r*8)
			switch {
			case cell >= 8:
				b.WriteRune('█')
			case cell <= 0:
				b.WriteRune(' ')
			default:
				b.WriteRune(blocks[int(cell)])
			}
		}
		out = append(out, C(format.CAccent, b.String()))
	}
	l, rr := humanNum(lo), humanNum(hi)
	out = append(out, C(format.CNull, l+strings.Repeat(" ", max(1, bins-len(l)-len(rr)))+rr))
	return out
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}

func (a *App) keyFields(k term.Key) {
	if k.Kind == term.KEnter {
		src := ""
		a.an.Read(func(r *analysis.Result) {
			fs := r.SortedFields()
			if a.fieldSel >= len(fs) {
				return
			}
			f := fs[a.fieldSel]
			if f.Numeric() && f.NumN > 1 {
				a.graphKey, a.Tab = f.Key, TabGraph
				return
			}
			if top := f.Top(1); len(top) > 0 {
				expr := f.Key + "=" + quoteIfNeeded(top[0].Value)
				if !strings.Contains(" "+r.Filter.Src+" ", " "+expr+" ") {
					src = strings.TrimSpace(r.Filter.Src + " " + expr)
				}
			}
		})
		if src != "" {
			a.applyFilter(src)
		}
		return
	}
	delta, toTop, toEnd, ok := a.scrollKey(k)
	if !ok {
		return
	}
	switch {
	case toTop:
		a.fieldSel = 0
	case toEnd:
		a.fieldSel = 1 << 30
	default:
		if k.Kind == term.KPgDn || k.Kind == term.KPgUp {
			delta /= 2
		}
		a.fieldSel = max(0, a.fieldSel+delta)
	}
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " \t\"") || s == "" {
		return `"` + strings.ReplaceAll(s, `"`, ``) + `"`
	}
	return s
}

func (a *App) applyFilter(src string) {
	f, err := analysis.ParseFilter(src)
	if err != nil {
		a.flash(err.Error())
		return
	}
	a.an.SetFilter(f)
	a.flash("filter: " + src)
}
