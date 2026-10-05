package analysis

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Olian04/glimt/format"
)

const maxKeys = 2000

// Result is everything derived from the store under the current format and
// filter. Read it only inside Analyzer.Read.
type Result struct {
	Format   format.Format
	Score    float64 // detection score (1 when forced)
	Forced   bool
	Filter   Filter
	Gen      int // bumps on every replay; views use it to drop caches
	From     int // first line the counters cover (>0 after a replay of an evicted cache)
	Cursor   int // next line to process
	Skipped  int // lines evicted before they could be analysed
	Parsed   int // lines recognised by the format
	Lines    int // lines analysed
	Records  int // records produced
	Matched  int // records passing the filter
	Fields   map[string]*FieldStat
	KeysOver bool
	Levels   map[string]int
	Patterns map[string]*Pattern
	PatOver  int

	store   *Store
	matched []int // visible line numbers when filtering (cached rows only)
}

// Analyzer incrementally applies a format and filter to a Store.
type Analyzer struct {
	mu     sync.Mutex
	store  *Store
	reg    *format.Registry
	forced string
	res    Result
	parser format.Parser

	detectedAt int
	detectSig  string
	wake       chan struct{}

	sink func(line string) // receives visible lines once each, in order (pipe mode)
	sunk int               // next line number the sink may receive
}

// SetSink streams every line that passes the filter to fn, once, in order.
// Replays (format re-detection) never re-send a line.
func (a *Analyzer) SetSink(fn func(line string)) {
	a.mu.Lock()
	a.sink = fn
	a.mu.Unlock()
}

func (a *Analyzer) emit(no int, line string) {
	if a.sink != nil && no >= a.sunk {
		a.sink(line)
		a.sunk = no + 1
	}
}

// New creates an analyzer. forced is a format name or "" for auto-detect.
func New(store *Store, reg *format.Registry, forced string) *Analyzer {
	a := &Analyzer{store: store, reg: reg, forced: forced, wake: make(chan struct{}, 1)}
	a.res.Format = format.Text{}
	a.res.store = store
	a.reset()
	return a
}

// Read runs fn with the result locked.
func (a *Analyzer) Read(fn func(r *Result)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn(&a.res)
}

// Store returns the underlying store.
func (a *Analyzer) Store() *Store { return a.store }

// Formats lists selectable format names ("auto" first).
func (a *Analyzer) Formats() []string { return append([]string{"auto"}, a.reg.Names()...) }

func (a *Analyzer) sample() []string {
	f := a.store.First()
	return a.store.Lines(f, f+200)
}

// SetFormat forces a format by name ("auto" or "" to detect).
func (a *Analyzer) SetFormat(name string) error {
	if name == "auto" {
		name = ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if name != "" {
		if _, ok := a.reg.Force(name, a.sample()); !ok {
			return fmt.Errorf("unknown format %q", name)
		}
	}
	a.forced = name
	a.detectedAt, a.detectSig = 0, ""
	a.reset()
	a.poke()
	return nil
}

// SetFilter replaces the filter and replays.
func (a *Analyzer) SetFilter(f Filter) {
	a.mu.Lock()
	a.res.Filter = f
	a.reset()
	a.mu.Unlock()
	a.poke()
}

func (a *Analyzer) poke() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// reset starts a replay from the oldest cached row.
func (a *Analyzer) reset() {
	r := &a.res
	a.store.resetDerived()
	r.Gen++
	r.From = a.store.First()
	r.Cursor = r.From
	a.store.SetPin(r.From)
	r.Skipped, r.Parsed, r.Lines, r.Records, r.Matched, r.PatOver = 0, 0, 0, 0, 0, 0
	r.Fields = map[string]*FieldStat{}
	r.KeysOver = false
	r.Levels = map[string]int{}
	r.Patterns = map[string]*Pattern{}
	r.matched = nil
	a.parser = r.Format.NewParser()
}

// detect (re)chooses the format while the sample is still growing.
func (a *Analyzer) detect() {
	n := a.store.Len() - a.store.First()
	eof := a.store.Info().EOF
	target := min(n, 200)
	if target == 0 {
		return
	}
	if a.detectedAt > 0 && !(target >= 2*a.detectedAt || (eof && target > a.detectedAt) || (target == 200 && a.detectedAt < 200)) {
		return
	}
	sample := a.sample()
	var f format.Format
	score := 1.0
	if a.forced != "" {
		f, _ = a.reg.Force(a.forced, sample)
	} else {
		f, score = a.reg.Detect(sample)
	}
	a.detectedAt = target
	sig := fmt.Sprintf("%#v", f)
	a.res.Score, a.res.Forced = score, a.forced != ""
	if sig != a.detectSig {
		a.detectSig = sig
		a.res.Format = f
		a.reset()
	}
}

// Run processes input until stop is closed.
func (a *Analyzer) Run(stop <-chan struct{}) {
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		if a.step() {
			continue // more to do; loop without waiting
		}
		select {
		case <-stop:
			return
		case <-a.store.Notify():
		case <-a.wake:
		case <-tick.C:
		}
	}
}

// Drain processes everything currently in the store.
func (a *Analyzer) Drain() {
	for a.step() {
	}
}

// RunToEOF processes input until the store is closed and fully analysed
// (for non-interactive use, with the producer running concurrently). idle,
// if set, runs each time processing catches up with the input.
func (a *Analyzer) RunToEOF(idle func()) {
	for {
		more := a.step()
		if !more && a.store.Info().EOF {
			if !a.step() { // final pass: re-detection at EOF may replay
				break
			}
			continue
		}
		if !more {
			if idle != nil {
				idle()
			}
			<-a.store.Notify()
		}
	}
	if idle != nil {
		idle()
	}
}

// step processes one batch; it reports whether work remains.
func (a *Analyzer) step() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.detect()
	r := &a.res
	if first := a.store.First(); r.Cursor < first {
		r.Skipped += first - r.Cursor
		r.Cursor = first
	}
	n := a.store.Len()
	end := min(n, r.Cursor+4000)
	for i, l := range a.store.Lines(r.Cursor, end) {
		a.process(r.Cursor+i, l)
	}
	r.Cursor = end
	a.store.SetPin(end)
	a.trim()
	return end < n
}

// trim drops window data belonging to evicted rows.
func (a *Analyzer) trim() {
	first := a.store.First()
	for _, f := range a.res.Fields {
		f.trim(first)
	}
	m := a.res.matched
	k := sort.SearchInts(m, first)
	if k > 0 {
		m = m[k:]
		if cap(m) > 2*len(m)+1024 {
			m = append([]int(nil), m...)
		}
		a.res.matched = m
	}
}

var reLevelWord = regexp.MustCompile(`\b(FATAL|PANIC|CRITICAL|ERROR|WARNING|WARN|INFO|NOTICE|DEBUG|TRACE)\b`)

func (a *Analyzer) process(no int, line string) {
	r := &a.res
	r.Lines++
	recs, ok := a.parser.Parse(no, line)
	if ok {
		r.Parsed++
	} else {
		a.store.setFlag(no, flagUnparsed)
	}
	filtering := !r.Filter.Empty()
	if !filtering {
		a.emit(no, line)
	}
	if len(recs) == 0 && filtering && r.Filter.Match(nil, line) {
		a.markLines(no, no)
	}
	for i := range recs {
		rec := &recs[i]
		r.Records++
		text := line
		if rec.Line != no || rec.End != no {
			text = strings.Join(a.store.Lines(rec.Line, rec.End+1), "\n")
		}
		if filtering {
			if !r.Filter.Match(rec, text) {
				continue
			}
			a.markLines(rec.Line, rec.End)
		}
		r.Matched++
		a.accumulate(rec, text)
	}
}

func (a *Analyzer) markLines(from, to int) {
	m := a.res.matched
	if len(m) > 0 && from <= m[len(m)-1] {
		from = m[len(m)-1] + 1
	}
	for l := from; l <= to; l++ {
		m = append(m, l)
		if a.sink != nil {
			a.emit(l, a.store.Line(l))
		}
	}
	a.res.matched = m
}

func (a *Analyzer) accumulate(rec *format.Record, text string) {
	r := &a.res
	id := r.Records
	points := 0
	for _, f := range rec.Fields {
		fs := r.Fields[f.Key]
		if fs == nil {
			if len(r.Fields) >= maxKeys {
				r.KeysOver = true
				continue
			}
			fs = newFieldStat(f.Key, len(r.Fields))
			r.Fields[f.Key] = fs
		}
		if fs.add(id, rec.Line, f.Val) {
			points++
		}
	}
	if points > 0 {
		a.store.Charge(rec.Line, int64(points*pointCost))
	}
	level := ""
	for _, k := range format.LevelKeys {
		if v, ok := rec.Get(k); ok {
			level = format.NormLevel(v.S)
			break
		}
	}
	if level == "" && !r.Format.Structured() {
		if m := reLevelWord.FindString(text); m != "" {
			level = format.NormLevel(m)
		}
	}
	if level != "" {
		r.Levels[level]++
	}
	msg, ok := rec.Message()
	if !ok && !r.Format.Structured() {
		msg, ok = text, true
	}
	if ok && strings.TrimSpace(msg) != "" {
		t := format.Template(msg)
		p := r.Patterns[t]
		if p == nil {
			if len(r.Patterns) >= maxPatterns {
				r.PatOver++
				return
			}
			p = &Pattern{Template: t, Example: msg, First: rec.Line}
			r.Patterns[t] = p
		}
		p.Count++
	}
}

// ---- views over the result (call inside Read) ---------------------------

// Filtering reports whether a non-empty filter is active.
func (r *Result) Filtering() bool { return !r.Filter.Empty() }

// VisibleCount is the number of cached lines the input view can show.
func (r *Result) VisibleCount() int {
	if r.Filtering() {
		return len(r.matched)
	}
	return r.store.Len() - r.store.First()
}

// VisibleLine maps a view index to a line number.
func (r *Result) VisibleLine(k int) int {
	if r.Filtering() {
		return r.matched[k]
	}
	return r.store.First() + k
}

// VisibleIndex is the index of the first visible line >= no.
func (r *Result) VisibleIndex(no int) int {
	if r.Filtering() {
		return sort.SearchInts(r.matched, no)
	}
	return max(0, no-r.store.First())
}

// Unparsed reports whether line no was not recognised by the format.
func (r *Result) Unparsed(no int) bool { return r.store.flag(no, flagUnparsed) }

// SortedFields returns fields by presence (desc), then first-seen order.
func (r *Result) SortedFields() []*FieldStat {
	out := make([]*FieldStat, 0, len(r.Fields))
	for _, f := range r.Fields {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Records != out[j].Records {
			return out[i].Records > out[j].Records
		}
		return out[i].Order < out[j].Order
	})
	return out
}

// NumericFields lists numeric fields that have at least two cached points.
func (r *Result) NumericFields() []*FieldStat {
	var out []*FieldStat
	for _, f := range r.SortedFields() {
		if f.Numeric() && len(f.Window()) > 1 {
			out = append(out, f)
		}
	}
	return out
}

// SortedPatterns returns patterns by count.
func (r *Result) SortedPatterns() []*Pattern {
	out := make([]*Pattern, 0, len(r.Patterns))
	for _, p := range r.Patterns {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].First < out[j].First
	})
	return out
}
