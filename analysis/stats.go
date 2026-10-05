package analysis

import (
	"math"
	"sort"

	"github.com/Olian04/glimt/format"
)

const (
	maxDistinct = 10000
	pointCost   = 16 // bytes charged to a row per cached numeric point
)

// FieldStat is one key. Counter fields cover every record since the last
// replay; the window holds the numeric values of rows still cached.
type FieldStat struct {
	Key     string
	Order   int // first-seen order
	Count   int // occurrences (array keys can occur many times per record)
	Records int // records containing the key
	Kinds   [4]int

	Values   map[string]int // distinct values (capped)
	Overflow bool           // more distinct values than we track

	NumN     int
	Sum      float64
	SumSq    float64
	Min, Max float64
	Unit     string

	win      []Point // numeric values of cached rows, oldest first
	wh       int     // win[wh:] is live
	changes  int     // window adds+drops, for the sorted cache
	sorted   []float64
	sortedAt int

	lastRec int
}

// Point is a numeric value and the line it came from.
type Point struct {
	Line int
	V    float64
}

func newFieldStat(key string, order int) *FieldStat {
	return &FieldStat{Key: key, Order: order, Values: map[string]int{}, lastRec: -1,
		Min: math.Inf(1), Max: math.Inf(-1)}
}

// add counts one occurrence; it reports whether a window point was added.
func (f *FieldStat) add(rec, line int, v format.Value) bool {
	f.Count++
	if rec != f.lastRec {
		f.Records++
		f.lastRec = rec
	}
	f.Kinds[v.Kind]++
	key := v.S
	if len(key) > 200 {
		key = key[:200]
	}
	if _, ok := f.Values[key]; ok || len(f.Values) < maxDistinct {
		f.Values[key]++
	} else {
		f.Overflow = true
	}
	if v.Kind != format.Num {
		return false
	}
	if v.Unit != "" {
		f.Unit = v.Unit
	}
	f.NumN++
	f.Sum += v.N
	f.SumSq += v.N * v.N
	f.Min = math.Min(f.Min, v.N)
	f.Max = math.Max(f.Max, v.N)
	f.win = append(f.win, Point{line, v.N})
	f.changes++
	return true
}

// trim drops window points from evicted rows.
func (f *FieldStat) trim(first int) {
	n := f.wh
	for n < len(f.win) && f.win[n].Line < first {
		n++
	}
	if n == f.wh {
		return
	}
	f.changes += n - f.wh
	f.wh = n
	if f.wh > 1024 && f.wh > len(f.win)/2 {
		f.win = append(f.win[:0:0], f.win[f.wh:]...)
		f.wh = 0
	}
}

// Window returns the cached points, oldest first (shared; read-only).
func (f *FieldStat) Window() []Point { return f.win[f.wh:] }

// Type is the dominant kind, or "mixed".
func (f *FieldStat) Type() string {
	best, bi := 0, 0
	nonNull := 0
	for i, c := range f.Kinds {
		if format.Kind(i) != format.Null {
			nonNull += c
		}
		if c > best && format.Kind(i) != format.Null {
			best, bi = c, i
		}
	}
	if nonNull == 0 {
		return "null"
	}
	if float64(best) < 0.9*float64(nonNull) {
		return "mixed"
	}
	return format.Kind(bi).String()
}

// Numeric reports whether the key is (mostly) numeric.
func (f *FieldStat) Numeric() bool { return f.NumN > 0 && f.Type() == "num" }

// Distinct is the number of distinct values (a lower bound if Overflow).
func (f *FieldStat) Distinct() int { return len(f.Values) }

// Mean of numeric values (counter).
func (f *FieldStat) Mean() float64 {
	if f.NumN == 0 {
		return math.NaN()
	}
	return f.Sum / float64(f.NumN)
}

// Std is the population standard deviation (counter).
func (f *FieldStat) Std() float64 {
	if f.NumN == 0 {
		return math.NaN()
	}
	m := f.Mean()
	return math.Sqrt(math.Max(0, f.SumSq/float64(f.NumN)-m*m))
}

// Sorted returns the window values sorted. The sort is cached and redone
// once the window has changed by 1% (deterministic, amortised O(n log n)).
func (f *FieldStat) Sorted() []float64 {
	n := len(f.win) - f.wh
	if f.sorted == nil || f.changes-f.sortedAt >= max(1, n/100) {
		f.sorted = make([]float64, n)
		for i, p := range f.win[f.wh:] {
			f.sorted[i] = p.V
		}
		sort.Float64s(f.sorted)
		f.sortedAt = f.changes
	}
	return f.sorted
}

// Quantiles returns quantiles of the window.
func (f *FieldStat) Quantiles(qs ...float64) []float64 {
	s := f.Sorted()
	out := make([]float64, len(qs))
	for i, q := range qs {
		if len(s) == 0 {
			out[i] = math.NaN()
			continue
		}
		pos := q * float64(len(s)-1)
		lo := int(pos)
		hi := min(lo+1, len(s)-1)
		out[i] = s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
	}
	return out
}

// ValueCount is a value with its frequency.
type ValueCount struct {
	Value string
	Count int
}

// Top returns the n most frequent values.
func (f *FieldStat) Top(n int) []ValueCount {
	vs := make([]ValueCount, 0, len(f.Values))
	for v, c := range f.Values {
		vs = append(vs, ValueCount{v, c})
	}
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Count != vs[j].Count {
			return vs[i].Count > vs[j].Count
		}
		a, b := format.Infer(vs[i].Value), format.Infer(vs[j].Value)
		if a.Kind == format.Num && b.Kind == format.Num {
			return a.N < b.N
		}
		return vs[i].Value < vs[j].Value
	})
	if len(vs) > n {
		vs = vs[:n]
	}
	return vs
}

// Pattern is a message template with its frequency (counter).
type Pattern struct {
	Template string
	Count    int
	Example  string
	First    int // line number of first occurrence
}

const maxPatterns = 5000
