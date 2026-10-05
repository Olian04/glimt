package format

import (
	"encoding/csv"
	"strconv"
	"strings"
)

// CSV covers comma, tab, semicolon and pipe separated values. Detection picks
// the separator, whether the first row is a header, and column widths for
// aligned display.
type CSV struct {
	Sep    rune
	Header []string // column names (from header row, or c1..cn)
	HasHdr bool
	hdrRaw string
	widths []int
}

func (c CSV) Name() string {
	switch c.Sep {
	case '\t':
		return "tsv"
	case ',':
		return "csv"
	}
	return "csv(" + string(c.Sep) + ")"
}
func (CSV) Structured() bool   { return true }
func (CSV) GraphField() string { return "" }

func splitSep(line string, sep rune) ([]string, bool) {
	r := csv.NewReader(strings.NewReader(line))
	r.Comma = sep
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	rec, err := r.Read()
	if err != nil {
		return nil, false
	}
	return rec, true
}

func (c CSV) NewParser() Parser { return &csvParser{c: c} }

type csvParser struct {
	c    CSV
	seen bool
}

func (p *csvParser) Parse(no int, line string) ([]Record, bool) {
	cells, ok := splitSep(line, p.c.Sep)
	if !ok || len(cells) < 2 {
		return nil, false
	}
	if p.c.HasHdr && !p.seen && line == p.c.hdrRaw {
		p.seen = true
		return nil, true // header row
	}
	rec := Record{Line: no, End: no}
	for i, cell := range cells {
		name := "c" + strconv.Itoa(i+1)
		if i < len(p.c.Header) {
			name = p.c.Header[i]
		}
		rec.Fields = append(rec.Fields, Field{Key: name, Val: Infer(cell)})
	}
	return []Record{rec}, len(cells) == len(p.c.Header)
}

func (c CSV) Pretty(line string, _ int) []string {
	cells, ok := splitSep(line, c.Sep)
	if !ok {
		return nil
	}
	hdr := c.HasHdr && line == c.hdrRaw
	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString(C(CPunct, " │ "))
		}
		w := 12
		if i < len(c.widths) {
			w = c.widths[i]
		}
		pad := w - len([]rune(cell))
		key := ""
		if i < len(c.Header) {
			key = c.Header[i]
		}
		txt := cell
		if hdr {
			txt = Bold(C(CKey, cell))
		} else {
			txt = ColorValue(key, Infer(cell), cell)
		}
		v := Infer(cell)
		if v.Kind == Num && !hdr && pad > 0 { // right-align numbers
			b.WriteString(strings.Repeat(" ", pad) + txt)
		} else {
			b.WriteString(txt)
			if pad > 0 && i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
	}
	return []string{b.String()}
}

func detectCSV(sample []string) (float64, Format) {
	if len(sample) < 2 {
		return 0, nil
	}
	best, bestF := 0.0, Format(nil)
	for _, sep := range []rune{',', '\t', ';', '|'} {
		counts := map[int]int{}
		rows := make([][]string, 0, len(sample))
		for _, l := range sample {
			if !strings.ContainsRune(l, sep) {
				counts[0]++
				rows = append(rows, nil)
				continue
			}
			cells, ok := splitSep(l, sep)
			if !ok {
				counts[0]++
				rows = append(rows, nil)
				continue
			}
			counts[len(cells)]++
			rows = append(rows, cells)
		}
		mode, mc := 0, 0
		for n, c := range counts {
			if n >= 2 && (c > mc || (c == mc && n > mode)) {
				mode, mc = n, c
			}
		}
		if mode < 2 {
			continue
		}
		consistency := float64(mc) / float64(len(sample))
		if consistency < 0.85 {
			continue
		}
		// prose with commas rarely has a consistent count AND short cells
		long := 0
		for _, r := range rows {
			for _, c := range r {
				if len(c) > 120 {
					long++
				}
			}
		}
		score := consistency * 0.95
		if long > len(rows) {
			score *= 0.5
		}
		if sep == ',' || sep == '\t' {
			score += 0.01
		}
		if score <= best {
			continue
		}
		c := CSV{Sep: sep}
		first := rows[0]
		if len(first) == mode && looksHeader(first, rows[1:]) {
			c.HasHdr, c.Header, c.hdrRaw = true, first, sample[0]
		} else {
			for i := 0; i < mode; i++ {
				c.Header = append(c.Header, "c"+strconv.Itoa(i+1))
			}
		}
		c.widths = make([]int, mode)
		for _, r := range rows {
			for i, cell := range r {
				if i < mode {
					c.widths[i] = max(c.widths[i], min(len([]rune(cell)), 40))
				}
			}
		}
		best, bestF = score, c
	}
	return best, bestF
}

func looksHeader(first []string, rest [][]string) bool {
	seen := map[string]bool{}
	for _, c := range first {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] || Infer(c).Kind == Num {
			return false
		}
		seen[c] = true
	}
	// header if some column is numeric below it, or the names differ from
	// every value in their column
	for i := range first {
		for _, r := range rest {
			if i < len(r) && strings.TrimSpace(r[i]) == strings.TrimSpace(first[i]) {
				return false
			}
		}
	}
	return true
}

// ---- whitespace aligned tables (kubectl get, ps, docker ps, df) ----------

// Table is column-aligned output with an upper-case header line.
type Table struct {
	Header []string
	starts []int // column start offsets when columns are 2+ space separated
	hdrRaw string
}

func (Table) Name() string        { return "table" }
func (Table) Structured() bool    { return true }
func (Table) GraphField() string  { return "" }
func (t Table) NewParser() Parser { return &tableParser{t: t} }

type tableParser struct {
	t    Table
	seen bool
}

func (p *tableParser) Parse(no int, line string) ([]Record, bool) {
	if !p.seen && line == p.t.hdrRaw {
		p.seen = true
		return nil, true
	}
	cells := p.t.split(line)
	if len(cells) < len(p.t.Header) {
		return nil, false
	}
	rec := Record{Line: no, End: no}
	for i, c := range cells {
		rec.Fields = append(rec.Fields, Field{Key: p.t.Header[i], Val: Infer(c)})
	}
	return []Record{rec}, true
}

func (t Table) split(line string) []string {
	n := len(t.Header)
	if t.starts != nil && len(line) >= t.starts[n-1] {
		cells := make([]string, n)
		ok := true
		for i := range t.Header {
			s := t.starts[i]
			e := len(line)
			if i+1 < n {
				e = t.starts[i+1]
			}
			// cell boundary must sit on whitespace
			if s > 0 && line[s-1] != ' ' {
				ok = false
				break
			}
			cells[i] = strings.TrimSpace(line[s:e])
		}
		if ok {
			return cells
		}
	}
	f := strings.Fields(line)
	if len(f) < n {
		return f
	}
	last := strings.Join(f[n-1:], " ")
	return append(f[:n-1], last)
}

func (t Table) Pretty(line string, _ int) []string {
	if line == t.hdrRaw {
		return []string{Bold(C(CKey, line))}
	}
	cells := t.split(line)
	if len(cells) < len(t.Header) {
		return []string{Highlight(line)}
	}
	// colour each cell in place, keeping the original alignment
	var b strings.Builder
	pos := 0
	for i, c := range cells {
		at := strings.Index(line[pos:], c)
		if at < 0 || c == "" {
			continue
		}
		b.WriteString(line[pos : pos+at])
		b.WriteString(ColorValue(t.Header[i], Infer(c), c))
		pos += at + len(c)
	}
	b.WriteString(line[pos:])
	return []string{b.String()}
}

func isHeaderWord(w string) bool {
	hasLetter := false
	for _, r := range w {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r == '%' || r == '-' || r == '_' || r == '/' || r == '(' || r == ')' || r == '#' || r == '.' || (r >= '0' && r <= '9'):
		default:
			return false
		}
	}
	return hasLetter
}

func detectTable(sample []string) (float64, Format) {
	if len(sample) < 2 {
		return 0, nil
	}
	hdr := sample[0]
	words := strings.Fields(hdr)
	if len(words) < 2 {
		return 0, nil
	}
	for _, w := range words {
		if !isHeaderWord(w) {
			return 0, nil
		}
	}
	t := Table{hdrRaw: hdr}
	// multi-word headers ("CONTAINER ID", "NOMINATED NODE") are 2+ space separated
	if strings.Contains(strings.TrimSpace(hdr), "  ") {
		for _, part := range splitWide(hdr) {
			t.Header = append(t.Header, part.text)
			t.starts = append(t.starts, part.at)
		}
	} else {
		t.Header = words
	}
	for i, h := range t.Header {
		t.Header[i] = strings.ToLower(strings.ReplaceAll(h, " ", "_"))
	}
	ok := fraction(sample[1:], func(l string) bool { return len(strings.Fields(l)) >= len(t.Header) })
	return ok * 0.85, t
}

type wpart struct {
	text string
	at   int
}

func splitWide(s string) []wpart {
	var out []wpart
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			break
		}
		st := i
		for i < len(s) && !(s[i] == ' ' && (i+1 >= len(s) || s[i+1] == ' ')) {
			i++
		}
		out = append(out, wpart{strings.TrimSpace(s[st:i]), st})
	}
	return out
}
