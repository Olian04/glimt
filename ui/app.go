// Package ui renders an Analyzer's Result. Every screen is
// stats bar + tab bar + one view + footer; a frame is a pure function of
// (Result, App state, size), which is also how --render snapshots work.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/format"
	"github.com/Olian04/glimt/term"
)

// Tab identifies a view.
type Tab int

const (
	TabInput Tab = iota
	TabFields
	TabGraph
	TabPatterns
)

var tabNames = map[Tab]string{TabInput: "Input", TabFields: "Fields", TabGraph: "Graph", TabPatterns: "Patterns"}

// App is the interactive state.
type App struct {
	an   *analysis.Analyzer
	W, H int

	Tab       Tab // wanted view; shown when the data supports it
	cur       Tab // view actually shown this frame
	pretty    bool
	prettySet bool // user chose explicitly; stop following the format
	wrap      bool
	lineNums  bool

	// input view position
	follow            bool
	top, sub          int
	followTop, folSub int
	hscroll           int
	atBottom          bool
	cache             map[int][]string
	cacheSig          string

	fieldSel, fieldTop int
	graphKey           string
	patSel, patTop     int

	prompt    *string
	promptErr string
	msg       string
	msgUntil  time.Time
	help      bool
	formatIdx int
	tabs      []Tab
	viewH     int
}

// New creates the app.
func New(an *analysis.Analyzer) *App {
	return &App{an: an, follow: true, lineNums: true, cache: map[int][]string{}, W: 100, H: 30}
}

// Run drives the terminal until quit.
func (a *App) Run(t *term.Terminal) {
	keys := t.Keys()
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	a.W, a.H = t.Size()
	t.Draw(a.Frame(), true)
	for {
		force := false
		select {
		case k, ok := <-keys:
			if !ok || !a.Handle(k) {
				return
			}
			// drain queued keys before redrawing (fast scrolling)
		drain:
			for {
				select {
				case k, ok := <-keys:
					if !ok || !a.Handle(k) {
						return
					}
				default:
					break drain
				}
			}
		case <-t.Resize:
			a.W, a.H = t.Size()
			force = true
		case <-tick.C:
		}
		t.Draw(a.Frame(), force)
	}
}

func (a *App) flash(s string) {
	a.msg, a.msgUntil = s, time.Now().Add(3*time.Second)
}

// Frame renders the whole screen.
func (a *App) Frame() []string {
	frame := make([]string, 0, a.H)
	st := a.an.Store()
	info := st.Info()
	a.an.Read(func(r *analysis.Result) {
		if !a.prettySet {
			a.pretty = r.Format.Name() != "text"
		}
		a.tabs = a.availableTabs(r, info)
		a.cur = a.Tab
		if !hasTab(a.tabs, a.cur) {
			a.cur = TabInput // e.g. fields vanish briefly while a filter replays
		}
		frame = append(frame, a.statsBar(r, info), a.tabBar())
		a.viewH = max(1, a.H-3)
		var body []string
		switch a.cur {
		case TabInput:
			body = a.inputView(r, st, a.viewH)
		case TabFields:
			body = a.fieldsView(r, a.viewH)
		case TabGraph:
			body = a.graphView(r, info, a.viewH)
		case TabPatterns:
			body = a.patternsView(r, a.viewH)
		}
		for i := 0; i < a.viewH; i++ {
			l := ""
			if i < len(body) {
				l = body[i]
			}
			frame = append(frame, term.Pad(l, a.W))
		}
		frame = append(frame, a.footer(r))
	})
	if a.help {
		a.overlayHelp(frame)
	}
	return frame[:min(len(frame), a.H)]
}

func hasTab(ts []Tab, t Tab) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

// availableTabs: which views the data supports.
func (a *App) availableTabs(r *analysis.Result, info analysis.Info) []Tab {
	tabs := []Tab{TabInput}
	if len(r.Fields) > 0 {
		tabs = append(tabs, TabFields)
	}
	if len(r.NumericFields()) > 0 || rateMeaningful(info) {
		tabs = append(tabs, TabGraph)
	}
	if len(r.Patterns) > 1 {
		tabs = append(tabs, TabPatterns)
	}
	return tabs
}

func rateMeaningful(info analysis.Info) bool { return len(info.PerSec) >= 3 }

// ---- bars -----------------------------------------------------------------

const barBG = "\x1b[48;5;236m"

type seg struct {
	text string
	prio int // higher survives longer on narrow screens
}

func (a *App) statsBar(r *analysis.Result, info analysis.Info) string {
	var segs []seg
	add := func(p int, s string) { segs = append(segs, seg{s, p}) }

	fname := r.Format.Name()
	switch {
	case r.Forced:
		fname += C(format.CNull, " forced")
	case fname == "text" || info.Lines == 0:
	default:
		fname += C(format.CNull, fmt.Sprintf(" %.0f%%", r.Score*100))
	}
	add(10, Bold(C(format.CAccent, "◆ "))+Bold(fname))
	add(9, Bold(commas(info.Lines))+" lines")
	add(3, humanBytes(info.Bytes))
	if r.Format.Structured() && r.Lines > 0 {
		pct := 100 * float64(r.Parsed) / float64(r.Lines)
		s := fmt.Sprintf("parsed %.0f%%", pct)
		if pct < 95 {
			s = C(format.CWarn, s)
		}
		add(4, s)
		add(5, commas(r.Records)+" rec")
	}
	if len(info.PerSec) > 1 {
		add(2, fmt.Sprintf("%s/s ", humanNum(info.Rate))+C(format.CKey, sparkline(info.PerSec, 12)))
	}
	if lv := levelSummary(r.Levels); lv != "" {
		add(6, lv)
	}
	if info.Evicted > 0 || info.Used > info.Cap/2 {
		// the cache is (nearly) full: say what the window covers
		s := C(format.CNull, "mem ") + humanBytes(info.Used) + C(format.CNull, "/"+humanBytes(info.Cap))
		if info.Evicted > 0 {
			s += C(format.CWarn, " window ") + commas(info.Cached) + C(format.CNull, " rows")
		}
		add(5, s)
	}
	if r.From > 0 || r.Skipped > 0 {
		add(6, C(format.CWarn, "counters since line "+commas(r.From+r.Skipped+1)))
	}
	if r.Filtering() {
		add(8, C(format.CWarn, "⧩ ")+commas(r.Matched)+" match")
	}
	state := C(format.CInfo, "● live")
	if info.EOF {
		state = C(format.CNull, "■ done")
	}
	if r.Cursor < info.Lines {
		state = C(format.CWarn, fmt.Sprintf("◌ %d%%", 100*(r.Cursor-r.From)/max(1, info.Lines-r.From)))
	}
	add(7, state+" "+C(format.CNull, shortDur(info.Elapsed)))

	sep := C(format.CPunct, " │ ")
	for {
		total := 1
		for i, s := range segs {
			total += term.Width(s.text)
			if i > 0 {
				total += 3
			}
		}
		if total <= a.W || len(segs) <= 1 {
			break
		}
		lo := 0
		for i, s := range segs {
			if s.prio < segs[lo].prio {
				lo = i
			}
		}
		segs = append(segs[:lo], segs[lo+1:]...)
	}
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = s.text
	}
	return withBG(" "+strings.Join(parts, sep), a.W)
}

// withBG pads s to width with a persistent background (re-applied after resets).
func withBG(s string, w int) string {
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+barBG)
	return barBG + term.Pad(s, w) + "\x1b[0m"
}

func levelSummary(lv map[string]int) string {
	var parts []string
	for _, l := range []struct {
		k string
		c int
		s string
	}{{"error", format.CError, "E"}, {"warn", format.CWarn, "W"}, {"info", format.CInfo, "I"}, {"debug", format.CDebug, "D"}} {
		if n := lv[l.k]; n > 0 {
			parts = append(parts, C(l.c, l.s+" "+commas(n)))
		}
	}
	return strings.Join(parts, " ")
}

func (a *App) tabBar() string {
	var b strings.Builder
	for i, t := range a.tabs {
		label := fmt.Sprintf(" %d %s ", i+1, tabNames[t])
		if t == a.cur {
			b.WriteString("\x1b[48;5;141m\x1b[38;5;235m\x1b[1m" + label + "\x1b[0m")
		} else {
			b.WriteString(C(format.CPunct, label))
		}
		b.WriteString(" ")
	}
	flag := func(on bool, s string) string {
		if on {
			return C(format.CInfo, s)
		}
		return C(format.CPunct, s)
	}
	right := flag(a.pretty, "pretty") + " " + flag(a.wrap, "wrap") + " " + flag(a.follow, "follow") + " " + C(format.CPunct, "? help")
	left := b.String()
	gap := a.W - term.Width(left) - term.Width(right) - 1
	if gap < 1 {
		return term.Pad(left, a.W)
	}
	return left + strings.Repeat(" ", gap) + right + " "
}

func (a *App) footer(r *analysis.Result) string {
	if a.prompt != nil {
		s := C(format.CAccent, " / ") + *a.prompt + "\x1b[7m \x1b[0m"
		if a.promptErr != "" {
			s += "  " + C(format.CError, a.promptErr)
		}
		return term.Pad(s, a.W)
	}
	if a.msg != "" && time.Now().Before(a.msgUntil) {
		return term.Pad(" "+C(format.CWarn, a.msg), a.W)
	}
	var hints string
	switch a.cur {
	case TabInput:
		hints = "j/k scroll  g/G top/end  ←/→ pan  p pretty  w wrap  n numbers"
	case TabFields:
		hints = "j/k select  enter graph/filter"
	case TabGraph:
		hints = "←/→ series"
	case TabPatterns:
		hints = "j/k select  enter show in input"
	}
	filter := ""
	if r.Filtering() {
		filter = C(format.CWarn, "filter: ") + r.Filter.Src + C(format.CPunct, "  (esc clears)  ")
	}
	return term.Pad(" "+filter+C(format.CPunct, hints+"  / filter  F format  tab views  q quit"), a.W)
}

// ---- keys -----------------------------------------------------------------

// Handle applies a key; false means quit.
func (a *App) Handle(k term.Key) bool {
	if a.prompt != nil {
		return a.handlePrompt(k)
	}
	if a.help {
		a.help = false
		return k.Kind != term.KCtrlC
	}
	switch k.Kind {
	case term.KCtrlC:
		return false
	case term.KTab:
		a.cycleTab(1)
		return true
	case term.KShiftTab:
		a.cycleTab(-1)
		return true
	case term.KEsc:
		filtering := false
		a.an.Read(func(r *analysis.Result) { filtering = r.Filtering() })
		if filtering {
			a.an.SetFilter(analysis.Filter{})
			a.flash("filter cleared")
		}
		return true
	case term.KRune:
		switch k.Rune {
		case 'q':
			return false
		case '?':
			a.help = true
			return true
		case '/':
			s := ""
			a.an.Read(func(r *analysis.Result) { s = r.Filter.Src })
			a.prompt, a.promptErr = &s, ""
			return true
		case 'p':
			a.pretty, a.prettySet = !a.pretty, true
			return true
		case 'w':
			a.wrap = !a.wrap
			a.hscroll = 0
			return true
		case 'n':
			a.lineNums = !a.lineNums
			return true
		case 'F':
			names := a.an.Formats()
			if a.formatIdx == 0 { // start cycling after the detected format
				a.an.Read(func(r *analysis.Result) {
					for i, n := range names {
						if n == r.Format.Name() && !r.Forced {
							a.formatIdx = i
						}
					}
				})
			}
			a.formatIdx = (a.formatIdx + 1) % len(names)
			if err := a.an.SetFormat(names[a.formatIdx]); err != nil {
				a.flash(err.Error())
			} else {
				a.flash("format: " + names[a.formatIdx] + "   (F again to cycle)")
			}
			a.prettySet = false
			return true
		}
		if k.Rune >= '1' && k.Rune <= '9' {
			if i := int(k.Rune - '1'); i < len(a.tabs) {
				a.Tab = a.tabs[i]
			}
			return true
		}
	}
	switch a.cur {
	case TabInput:
		a.keyInput(k)
	case TabFields:
		a.keyFields(k)
	case TabGraph:
		a.keyGraph(k)
	case TabPatterns:
		a.keyPatterns(k)
	}
	return true
}

func (a *App) cycleTab(d int) {
	for i, t := range a.tabs {
		if t == a.cur {
			a.Tab = a.tabs[(i+d+len(a.tabs))%len(a.tabs)]
			return
		}
	}
}

func (a *App) handlePrompt(k term.Key) bool {
	p := a.prompt
	switch k.Kind {
	case term.KCtrlC:
		return false
	case term.KEsc:
		a.prompt = nil
	case term.KEnter:
		f, err := analysis.ParseFilter(*p)
		if err != nil {
			a.promptErr = err.Error()
			return true
		}
		a.prompt = nil
		a.an.SetFilter(f)
		a.follow, a.top, a.sub = true, 0, 0
		a.fieldSel, a.patSel = 0, 0
	case term.KBackspace:
		if len(*p) > 0 {
			r := []rune(*p)
			*p = string(r[:len(r)-1])
		}
	case term.KCtrlU:
		*p = ""
	case term.KCtrlW:
		s := strings.TrimRight(*p, " ")
		if i := strings.LastIndexByte(s, ' '); i >= 0 {
			*p = s[:i+1]
		} else {
			*p = ""
		}
	case term.KRune:
		*p += string(k.Rune)
	}
	return true
}

// scrollKey maps navigation keys to a delta in rows (page = view height).
func (a *App) scrollKey(k term.Key) (delta int, toTop, toEnd, ok bool) {
	page := max(1, a.viewH-1)
	switch k.Kind {
	case term.KUp:
		return -1, false, false, true
	case term.KDown:
		return 1, false, false, true
	case term.KWheelUp:
		return -3, false, false, true
	case term.KWheelDown:
		return 3, false, false, true
	case term.KPgUp, term.KCtrlU:
		return -page, false, false, true
	case term.KPgDn, term.KCtrlD:
		return page, false, false, true
	case term.KHome:
		return 0, true, false, true
	case term.KEnd:
		return 0, false, true, true
	case term.KRune:
		switch k.Rune {
		case 'k':
			return -1, false, false, true
		case 'j':
			return 1, false, false, true
		case 'g':
			return 0, true, false, true
		case 'G':
			return 0, false, true, true
		case ' ':
			return page, false, false, true
		case 'b':
			return -page, false, false, true
		}
	}
	return 0, false, false, false
}

// ---- small helpers ----------------------------------------------------------

// C re-exports the format palette helper.
func C(code int, s string) string { return format.C(code, s) }

// Bold re-exports bold.
func Bold(s string) string { return format.Bold(s) }

func commas(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return "-" + commas(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func humanBytes(b int64) string {
	f := float64(b)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if f < 1024 || u == "GB" {
			if u == "B" {
				return fmt.Sprintf("%d B", b)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return ""
}

func humanNum(f float64) string {
	switch {
	case f != f:
		return "–"
	case f >= 1e9 || f <= -1e9:
		return fmt.Sprintf("%.1fG", f/1e9)
	case f >= 1e6 || f <= -1e6:
		return fmt.Sprintf("%.1fM", f/1e6)
	case f >= 1e4 || f <= -1e4:
		return fmt.Sprintf("%.1fk", f/1e3)
	case f == float64(int64(f)):
		return fmt.Sprintf("%d", int64(f))
	case f >= 100 || f <= -100:
		return fmt.Sprintf("%.0f", f)
	case f >= 1 || f <= -1:
		return fmt.Sprintf("%.2f", f)
	}
	return fmt.Sprintf("%.3g", f)
}

func shortDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

var sparks = []rune("▁▂▃▄▅▆▇█")

func sparkline(v []int, n int) string {
	if len(v) > n {
		v = v[len(v)-n:]
	}
	mx := 1
	for _, x := range v {
		mx = max(mx, x)
	}
	var b strings.Builder
	for _, x := range v {
		if x == 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(sparks[min(len(sparks)-1, x*(len(sparks)-1)/mx)])
	}
	return b.String()
}

// SelectTab picks a view by name (for --render).
func (a *App) SelectTab(name string) error {
	for _, t := range a.tabs {
		if strings.EqualFold(tabNames[t], name) {
			a.Tab = t
			return nil
		}
	}
	return fmt.Errorf("view %q not available for this input (have: %s)", name, strings.Join(a.TabNames(), ", "))
}

// TabNames lists the views the data supports, as SelectTab spells them.
// Valid after the first Frame.
func (a *App) TabNames() []string {
	var names []string
	for _, t := range a.tabs {
		names = append(names, strings.ToLower(tabNames[t]))
	}
	return names
}
