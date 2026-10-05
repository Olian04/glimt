package format

import "strings"

// Logfmt is key=value structured logging. Words that are not pairs (a
// leading timestamp or level, say) are kept in the "_text" field.
type Logfmt struct{}

func (Logfmt) Name() string       { return "logfmt" }
func (Logfmt) Structured() bool   { return true }
func (Logfmt) GraphField() string { return "" }
func (Logfmt) NewParser() Parser  { return logfmtParser{} }

type lfTok struct {
	key, val   string // val includes quotes if quoted
	pair       bool
	start, end int
}

func lfTokens(line string) []lfTok {
	var toks []lfTok
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		st := i
		// key: up to '=' or space
		for i < len(line) && line[i] != '=' && line[i] != ' ' && line[i] != '\t' && line[i] != '"' {
			i++
		}
		if i < len(line) && line[i] == '=' && i > st {
			key := line[st:i]
			i++
			vs := i
			if i < len(line) && line[i] == '"' {
				i++
				for i < len(line) && line[i] != '"' {
					if line[i] == '\\' {
						i++
					}
					i++
				}
				if i < len(line) {
					i++
				}
			} else {
				for i < len(line) && line[i] != ' ' && line[i] != '\t' {
					i++
				}
			}
			if i > len(line) {
				i = len(line)
			}
			toks = append(toks, lfTok{key: key, val: line[vs:i], pair: true, start: st, end: i})
			continue
		}
		// bare word (possibly quoted)
		if i < len(line) && line[i] == '"' {
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' {
					i++
				}
				i++
			}
			if i < len(line) {
				i++
			}
		}
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i > len(line) {
			i = len(line)
		}
		toks = append(toks, lfTok{val: line[st:i], start: st, end: i})
	}
	return toks
}

func lfUnquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return strings.ReplaceAll(strings.ReplaceAll(v[1:len(v)-1], `\"`, `"`), `\\`, `\`)
	}
	return v
}

type logfmtParser struct{}

func (logfmtParser) Parse(no int, line string) ([]Record, bool) {
	toks := lfTokens(line)
	pairs := 0
	for _, t := range toks {
		if t.pair {
			pairs++
		}
	}
	if pairs < 2 || pairs*2 < len(toks) {
		return nil, false
	}
	rec := Record{Line: no, End: no}
	var rest []string
	for _, t := range toks {
		if t.pair {
			rec.Fields = append(rec.Fields, Field{Key: t.key, Val: Infer(lfUnquote(t.val))})
		} else {
			rest = append(rest, t.val)
		}
	}
	if len(rest) > 0 {
		txt := strings.Join(rest, " ")
		// a bare level word among the prefix is promoted to a level field
		if _, has := rec.Get("level"); !has {
			for _, w := range rest {
				if l := NormLevel(w); l != "" && len(w) > 2 {
					rec.Fields = append(rec.Fields, Field{Key: "level", Val: StrValue(strings.ToLower(strings.Trim(w, "[]:")))})
					break
				}
			}
		}
		rec.Fields = append(rec.Fields, Field{Key: "_text", Val: StrValue(txt)})
	}
	return []Record{rec}, true
}

func (Logfmt) Pretty(line string, _ int) []string {
	toks := lfTokens(line)
	var b strings.Builder
	last := 0
	for _, t := range toks {
		b.WriteString(line[last:t.start])
		if t.pair {
			v := Infer(lfUnquote(t.val))
			b.WriteString(C(CKey, t.key) + C(CPunct, "=") + ColorValue(t.key, v, t.val))
		} else if c, ok := LevelColor(t.val); ok && len(t.val) > 2 {
			b.WriteString(Bold(C(c, t.val)))
		} else {
			b.WriteString(Highlight(t.val))
		}
		last = t.end
	}
	b.WriteString(line[last:])
	return []string{b.String()}
}

func detectLogfmt(sample []string) (float64, Format) {
	p := logfmtParser{}
	return fraction(sample, func(l string) bool { _, ok := p.Parse(0, l); return ok }) * 0.97, Logfmt{}
}
