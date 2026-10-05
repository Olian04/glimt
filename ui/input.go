package ui

import (
	"fmt"
	"strings"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/format"
	"github.com/Olian04/glimt/term"
)

func digits(n int) int { return len(fmt.Sprint(max(n, 1))) }

func (a *App) gutterW(storeLen int) int {
	if !a.lineNums {
		return 0
	}
	return digits(storeLen) + 2
}

// rows renders visible line k into display rows.
func (a *App) rows(r *analysis.Result, st *analysis.Store, k int) []string {
	no := r.VisibleLine(k)
	sig := fmt.Sprint(r.Gen, a.W, a.pretty, a.wrap, a.hscroll, a.lineNums, st.Len() > 0 && digits(st.Len()) > 0, digits(st.Len()))
	if sig != a.cacheSig || len(a.cache) > 4000 {
		a.cache, a.cacheSig = map[int][]string{}, sig
	}
	if rs, ok := a.cache[no]; ok {
		return rs
	}
	gw := a.gutterW(st.Len())
	textW := max(10, a.W-gw)
	line := st.Line(no)
	unparsed := r.Format.Structured() && r.Unparsed(no)
	var disp []string
	if a.pretty {
		if !unparsed {
			disp = r.Format.Pretty(line, textW)
		}
		if disp == nil {
			disp = format.BestEffort(line, textW)
		}
	} else {
		disp = []string{line}
	}
	var out []string
	for _, d := range disp {
		if a.wrap {
			out = append(out, term.Wrap(d, textW)...)
		} else {
			out = append(out, term.Slice(d, a.hscroll, textW, false))
		}
	}
	if gw > 0 {
		for i := range out {
			g := strings.Repeat(" ", gw-1)
			bar := C(format.CPunct, "│")
			if i == 0 {
				g = fmt.Sprintf("%*d", gw-1, no+1)
				g = C(format.CNull, g)
			}
			if unparsed {
				bar = C(format.CError, "┃")
			}
			out[i] = g + bar + out[i]
		}
	}
	if no < r.Cursor {
		a.cache[no] = out
	}
	return out
}

func (a *App) inputView(r *analysis.Result, st *analysis.Store, h int) []string {
	v := r.VisibleCount()
	if v == 0 {
		msg := "waiting for input…"
		if r.Filtering() {
			msg = "no lines match " + r.Filter.Src
		}
		return []string{"", "  " + C(format.CNull, msg)}
	}
	if !a.follow {
		top := min(r.VisibleIndex(a.top), v-1) // a.top is a line number; rows may have been evicted
		a.top = r.VisibleLine(top)
		var out []string
		k, sub := top, a.sub
		for k < v && len(out) < h {
			rs := a.rows(r, st, k)
			if sub >= len(rs) {
				sub = len(rs) - 1
			}
			out = append(out, rs[sub:]...)
			k, sub = k+1, 0
		}
		if len(out) >= h || top == 0 && a.sub == 0 {
			return out[:min(h, len(out))]
		}
		a.follow = true // scrolled past the end: resume following
	}
	var acc [][]string
	n := 0
	k := v - 1
	for ; k >= 0 && n < h; k-- {
		rs := a.rows(r, st, k)
		acc = append(acc, rs)
		n += len(rs)
	}
	out := make([]string, 0, n)
	for i := len(acc) - 1; i >= 0; i-- {
		out = append(out, acc[i]...)
	}
	skip := max(0, len(out)-h)
	// remember where the top of the screen is, for when scrolling starts
	a.followTop, a.folSub = r.VisibleLine(k+1), skip
	if first := len(acc[len(acc)-1]); skip >= first && len(acc) > 1 {
		// skip spans only the first block in practice; clamp
		a.folSub = first - 1
	}
	return out[skip:]
}

func (a *App) keyInput(k term.Key) {
	switch {
	case k.Kind == term.KLeft || (k.Kind == term.KRune && k.Rune == 'h'):
		if !a.wrap {
			a.hscroll = max(0, a.hscroll-8)
		}
		return
	case k.Kind == term.KRune && k.Rune == 'f':
		a.follow = !a.follow
		if !a.follow {
			a.top, a.sub = a.followTop, a.folSub
		}
		return
	case k.Kind == term.KRight || (k.Kind == term.KRune && k.Rune == 'l'):
		if !a.wrap {
			a.hscroll += 8
		}
		return
	}
	delta, toTop, toEnd, ok := a.scrollKey(k)
	if !ok {
		return
	}
	switch {
	case toTop:
		a.follow, a.top, a.sub = false, 0, 0
	case toEnd:
		a.follow = true
	default:
		a.an.Read(func(r *analysis.Result) { a.scroll(r, delta) })
	}
}

// scroll moves the view by delta display rows. Positions are kept as line
// numbers (a.top) so they survive eviction; k is the view index.
func (a *App) scroll(r *analysis.Result, delta int) {
	st := a.an.Store()
	v := r.VisibleCount()
	if v == 0 {
		return
	}
	if a.follow {
		if delta > 0 {
			return
		}
		a.follow = false
		a.top, a.sub = a.followTop, a.folSub
	}
	k := min(r.VisibleIndex(a.top), v-1)
	defer func() { a.top = r.VisibleLine(k) }()
	for ; delta > 0; delta-- {
		a.sub++
		if a.sub >= len(a.rows(r, st, k)) {
			if k+1 >= v {
				a.sub--
				a.follow = true
				return
			}
			k, a.sub = k+1, 0
		}
	}
	for ; delta < 0; delta++ {
		a.sub--
		if a.sub < 0 {
			if k == 0 {
				a.sub = 0
				return
			}
			k--
			a.sub = len(a.rows(r, st, k)) - 1
		}
	}
}

// jumpTo shows line no at the top of the input view.
func (a *App) jumpTo(r *analysis.Result, no int) {
	a.Tab, a.follow, a.top, a.sub = TabInput, false, no, 0
}
