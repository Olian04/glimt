// Package analysis keeps a memory-capped cache of input rows and derives
// statistics from them.
//
// Two kinds of numbers come out of it:
//
//   - Counters accumulate over every row seen since the last replay and
//     survive eviction: lines, bytes, rate, records, per-key count, presence,
//     types, min/max/mean/std, distinct and top values, levels, patterns.
//   - Window stats are computed from the rows still cached: percentiles,
//     histograms, graph series and the input view itself.
//
// Changing the format or the filter replays the cached rows, so after a
// replay the counters cover the cache window (Result.From says where it
// starts).
package analysis

import (
	"strings"
	"sync"
	"time"

	"github.com/Olian04/glimt/term"
)

// DefaultCap is the default memory budget for cached rows.
const DefaultCap = 256 << 20

// rowOverhead approximates the bookkeeping cost of a cached row beyond its
// text: string header, entry struct, slice slot, allocator rounding.
const rowOverhead = 48

// maxSeconds bounds the per-second arrival history.
const maxSeconds = 3600

const flagUnparsed = 1

type entry struct {
	line  string
	cost  int64
	flags uint8
}

// Store is the row cache: append-only, evicting the oldest rows once the
// approximate memory used by rows (and data charged to them) exceeds Cap.
// Line numbers are absolute and never reused.
type Store struct {
	mu       sync.RWMutex
	ents     []entry
	head     int // ents[head:] are live
	first    int // absolute number of ents[head]
	cap      int64
	used     int64
	bytes    int64 // input bytes ever
	evicted  int
	start    time.Time
	last     time.Time
	now      func() time.Time
	secs     []int // lines per second, secs[0] is second secBase
	secBase  int
	eof      bool
	notify   chan struct{}
	pin      int        // rows >= pin are not analysed yet and can't be evicted
	room     *sync.Cond // signalled when the pin moves
	unpinned bool
}

// NewStore creates an empty store with a memory cap (bytes, <=0 = default)
// and a clock (nil = time.Now).
func NewStore(capBytes int64, now func() time.Time) *Store {
	if capBytes <= 0 {
		capBytes = DefaultCap
	}
	if now == nil {
		now = time.Now
	}
	s := &Store{cap: capBytes, now: now, start: now(), notify: make(chan struct{}, 1)}
	s.room = sync.NewCond(&s.mu)
	return s
}

// Append adds one raw line (escape codes, CRs and invalid UTF-8 are cleaned).
func (s *Store) Append(raw string) {
	line := term.ExpandTabs(term.Strip(strings.ToValidUTF8(strings.TrimRight(raw, "\r\n"), "�")))
	now := s.now()
	s.mu.Lock()
	cost := int64(len(line)) + rowOverhead
	s.ents = append(s.ents, entry{line: line, cost: cost})
	s.used += cost
	s.bytes += int64(len(raw))
	sec := int(now.Sub(s.start) / time.Second)
	for s.secBase+len(s.secs) <= sec {
		s.secs = append(s.secs, 0)
	}
	if sec >= s.secBase {
		s.secs[sec-s.secBase]++
	}
	if len(s.secs) > maxSeconds {
		drop := len(s.secs) - maxSeconds
		s.secs = append(s.secs[:0:0], s.secs[drop:]...)
		s.secBase += drop
	}
	s.last = now
	s.evict()
	s.mu.Unlock()
	s.poke()
	// Backpressure: if the cache is full of rows the analyzer hasn't seen,
	// wait for it rather than drop them unanalysed.
	s.mu.Lock()
	for s.used > s.cap && s.first >= s.pin && len(s.ents)-s.head > 1 && !s.unpinned {
		s.room.Wait()
	}
	s.mu.Unlock()
}

// SetPin tells the store that rows before no have been analysed.
func (s *Store) SetPin(no int) {
	s.mu.Lock()
	s.pin = no
	s.evict()
	s.mu.Unlock()
	s.room.Broadcast()
}

// Unpin lifts backpressure for good (the analyzer stopped).
func (s *Store) Unpin() {
	s.mu.Lock()
	s.unpinned = true
	s.mu.Unlock()
	s.room.Broadcast()
}

// Charge attributes n more bytes of derived data to row no, so the cap
// covers it and it is released when the row is evicted.
func (s *Store) Charge(no int, n int64) {
	s.mu.Lock()
	if i := no - s.first; i >= 0 && s.head+i < len(s.ents) {
		s.ents[s.head+i].cost += n
		s.used += n
		s.evict()
	}
	s.mu.Unlock()
}

func (s *Store) evict() {
	for s.used > s.cap && len(s.ents)-s.head > 1 && (s.first < s.pin || s.unpinned) {
		e := &s.ents[s.head]
		s.used -= e.cost
		*e = entry{}
		s.head++
		s.first++
		s.evicted++
	}
	if s.head > 4096 && s.head > len(s.ents)/2 {
		s.ents = append(s.ents[:0:0], s.ents[s.head:]...)
		s.head = 0
	}
}

func (s *Store) poke() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Close marks end of input.
func (s *Store) Close() {
	s.mu.Lock()
	s.eof = true
	s.mu.Unlock()
	s.poke()
}

// First is the oldest cached line number.
func (s *Store) First() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.first
}

// Len is the number of lines ever appended (one past the newest line number).
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.first + len(s.ents) - s.head
}

// Line returns line no, or "" if it is not cached.
func (s *Store) Line(no int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i := no - s.first; i >= 0 && s.head+i < len(s.ents) {
		return s.ents[s.head+i].line
	}
	return ""
}

// Lines copies the cached part of lines [from, to).
func (s *Store) Lines(from, to int) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	from = max(from, s.first)
	to = min(to, s.first+len(s.ents)-s.head)
	if from >= to {
		return nil
	}
	out := make([]string, to-from)
	for i := range out {
		out[i] = s.ents[s.head+from-s.first+i].line
	}
	return out
}

func (s *Store) setFlag(no int, f uint8) {
	s.mu.Lock()
	if i := no - s.first; i >= 0 && s.head+i < len(s.ents) {
		s.ents[s.head+i].flags |= f
	}
	s.mu.Unlock()
}

// resetDerived clears flags and charges of all cached rows (before a replay).
func (s *Store) resetDerived() {
	s.mu.Lock()
	for i := s.head; i < len(s.ents); i++ {
		e := &s.ents[i]
		base := int64(len(e.line)) + rowOverhead
		s.used -= e.cost - base
		e.cost, e.flags = base, 0
	}
	s.mu.Unlock()
}

func (s *Store) flag(no int, f uint8) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := no - s.first
	return i >= 0 && s.head+i < len(s.ents) && s.ents[s.head+i].flags&f != 0
}

// Info is a snapshot of stream-level counters that don't depend on format.
type Info struct {
	Lines   int   // lines ever
	Cached  int   // lines in the cache
	Evicted int   // lines dropped from the cache
	Bytes   int64 // input bytes ever
	Used    int64 // approximate cache memory
	Cap     int64
	EOF     bool
	Elapsed time.Duration // start to last line (or to now while live)
	Rate    float64       // lines/s over the last full second (live) or average
	PerSec  []int         // recent per-second line counts (≤ 1h)
}

// Info returns the stream snapshot.
func (s *Store) Info() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cached := len(s.ents) - s.head
	in := Info{Lines: s.first + cached, Cached: cached, Evicted: s.evicted, Bytes: s.bytes,
		Used: s.used, Cap: s.cap, EOF: s.eof}
	end := s.now()
	if s.eof {
		end = s.last
		if end.IsZero() {
			end = s.start
		}
	}
	in.Elapsed = end.Sub(s.start)
	in.PerSec = append([]int(nil), s.secs...)
	if !s.eof {
		// extend with idle seconds so the sparkline keeps moving
		sec := int(end.Sub(s.start)/time.Second) - s.secBase
		for len(in.PerSec) <= sec {
			in.PerSec = append(in.PerSec, 0)
		}
		if n := len(in.PerSec); n >= 2 {
			in.Rate = float64(in.PerSec[n-2])
		}
	} else if in.Elapsed > time.Second {
		in.Rate = float64(in.Lines) / in.Elapsed.Seconds()
	} else {
		in.Rate = float64(in.Lines)
	}
	return in
}

// Notify fires (coalesced) when lines arrive or input ends.
func (s *Store) Notify() <-chan struct{} { return s.notify }
