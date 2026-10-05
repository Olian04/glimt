package ui

import (
	"fmt"
	"strings"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/format"
	"github.com/Olian04/glimt/term"
)

func colorTemplate(t string) string {
	for _, ph := range []string{"<NUM>", "<TS>", "<IP>", "<STR>", "<HEX>", "<UUID>", "<URL>"} {
		t = strings.ReplaceAll(t, ph, C(format.CAccent, ph))
	}
	return t
}

func (a *App) patternsView(r *analysis.Result, h int) []string {
	ps := r.SortedPatterns()
	if len(ps) == 0 {
		return []string{"", "  " + C(format.CNull, "no messages")}
	}
	a.patSel = clamp(a.patSel, 0, len(ps)-1)
	listH := max(1, h-5)
	if a.patSel < a.patTop {
		a.patTop = a.patSel
	}
	if a.patSel >= a.patTop+listH {
		a.patTop = a.patSel - listH + 1
	}
	total := r.Matched
	head := fmt.Sprintf(" %8s  %6s  PATTERN  %s", "COUNT", "%", C(format.CNull, fmt.Sprintf("(%s distinct)", commas(len(ps)))))
	out := []string{Bold(C(format.CPunct, head))}
	barMax := ps[0].Count
	for i := a.patTop; i < len(ps) && i < a.patTop+listH; i++ {
		p := ps[i]
		bar := strings.Repeat("▮", max(1, p.Count*6/barMax))
		row := fmt.Sprintf(" %8s  %6s  ", commas(p.Count), pct(p.Count, total)) + C(format.CKey, fmt.Sprintf("%-6s ", bar)) + colorTemplate(p.Template)
		row = term.Truncate(row, a.W)
		if i == a.patSel {
			row = "\x1b[48;5;238m" + strings.ReplaceAll(term.Pad(row, a.W), "\x1b[0m", "\x1b[0m\x1b[48;5;238m") + "\x1b[0m"
		}
		out = append(out, row)
	}
	if r.PatOver > 0 {
		out = append(out, C(format.CWarn, fmt.Sprintf(" +%s messages in patterns beyond the %d tracked", commas(r.PatOver), len(ps))))
	}
	for len(out) < h-3 {
		out = append(out, "")
	}
	p := ps[a.patSel]
	out = append(out, C(format.CAccent, fmt.Sprintf("── example (first seen line %d) ", p.First+1))+C(format.CPunct, strings.Repeat("─", a.W)))
	ex := format.Highlight(p.Example)
	out = append(out, term.Wrap(ex, a.W)...)
	return out
}

func (a *App) keyPatterns(k term.Key) {
	if k.Kind == term.KEnter {
		a.an.Read(func(r *analysis.Result) {
			ps := r.SortedPatterns()
			if a.patSel < len(ps) {
				a.jumpTo(r, ps[a.patSel].First)
			}
		})
		return
	}
	delta, toTop, toEnd, ok := a.scrollKey(k)
	if !ok {
		return
	}
	switch {
	case toTop:
		a.patSel = 0
	case toEnd:
		a.patSel = 1 << 30
	default:
		a.patSel = max(0, a.patSel+delta)
	}
}

var helpText = []string{
	"glimt — pipe anything in, get structure out",
	"",
	"  tab / 1-4     switch view            q, ctrl-c   quit",
	"  /             filter (enter apply)   esc         clear filter",
	"  F             cycle format (auto → …) ?          this help",
	"",
	"  input   j/k ↑/↓ scroll   space/b page   g/G top/follow   f follow",
	"          ←/→ pan   p pretty   w wrap   n line numbers",
	"  fields  j/k select   enter: graph numeric field / filter top value",
	"  graph   ←/→ choose series",
	"  pattern j/k select   enter: jump to first occurrence",
	"",
	"filter terms (all must match):",
	"  word  -word  /regex/  key=val  key!=val  key=*  key=glob*",
	"  key>n  key>=n  key<n  key<=n  key~regex   e.g.  level=error dur>200",
	"",
	"press any key",
}

func (a *App) overlayHelp(frame []string) {
	w := 0
	for _, l := range helpText {
		w = max(w, term.Width(l))
	}
	w += 4
	h := len(helpText) + 2
	x0 := max(0, (a.W-w)/2)
	y0 := max(0, (a.H-h)/2)
	box := make([]string, 0, h)
	box = append(box, "╭"+strings.Repeat("─", w-2)+"╮")
	for _, l := range helpText {
		box = append(box, "│ "+term.Pad(l, w-4)+" │")
	}
	box = append(box, "╰"+strings.Repeat("─", w-2)+"╯")
	for i, b := range box {
		y := y0 + i
		if y >= len(frame) {
			break
		}
		left := term.Slice(frame[y], 0, x0, true)
		right := term.Slice(frame[y], x0+w, max(0, a.W-x0-w), true)
		frame[y] = left + "\x1b[48;5;235m" + C(format.CAccent, "") + b + "\x1b[0m" + right
	}
}
