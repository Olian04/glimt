package format

import (
	"encoding/json"
	"errors"
	"strings"
)

// ---- a small order-preserving JSON parser --------------------------------

type jnode struct {
	t    byte // o a s n b z
	raw  string
	keys []string
	kids []*jnode
}

var errJSON = errors.New("bad json")

type jparser struct {
	s string
	i int
}

func (p *jparser) ws() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jparser) value(depth int) (*jnode, error) {
	if depth > 200 {
		return nil, errJSON
	}
	p.ws()
	if p.i >= len(p.s) {
		return nil, errJSON
	}
	switch c := p.s[p.i]; {
	case c == '{':
		p.i++
		n := &jnode{t: 'o'}
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return n, nil
		}
		for {
			p.ws()
			if p.i >= len(p.s) || p.s[p.i] != '"' {
				return nil, errJSON
			}
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			p.ws()
			if p.i >= len(p.s) || p.s[p.i] != ':' {
				return nil, errJSON
			}
			p.i++
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.keys = append(n.keys, unquote(k))
			n.kids = append(n.kids, v)
			p.ws()
			if p.i >= len(p.s) {
				return nil, errJSON
			}
			if p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.s[p.i] == '}' {
				p.i++
				return n, nil
			}
			return nil, errJSON
		}
	case c == '[':
		p.i++
		n := &jnode{t: 'a'}
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return n, nil
		}
		for {
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.kids = append(n.kids, v)
			p.ws()
			if p.i >= len(p.s) {
				return nil, errJSON
			}
			if p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.s[p.i] == ']' {
				p.i++
				return n, nil
			}
			return nil, errJSON
		}
	case c == '"':
		raw, err := p.str()
		return &jnode{t: 's', raw: raw}, err
	case c == 't' && strings.HasPrefix(p.s[p.i:], "true"):
		p.i += 4
		return &jnode{t: 'b', raw: "true"}, nil
	case c == 'f' && strings.HasPrefix(p.s[p.i:], "false"):
		p.i += 5
		return &jnode{t: 'b', raw: "false"}, nil
	case c == 'n' && strings.HasPrefix(p.s[p.i:], "null"):
		p.i += 4
		return &jnode{t: 'z', raw: "null"}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		st := p.i
		p.i++
		for p.i < len(p.s) && strings.IndexByte("0123456789.eE+-", p.s[p.i]) >= 0 {
			p.i++
		}
		return &jnode{t: 'n', raw: p.s[st:p.i]}, nil
	}
	return nil, errJSON
}

// str scans a quoted string, returning it with quotes.
func (p *jparser) str() (string, error) {
	st := p.i
	p.i++
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case '\\':
			p.i += 2
			continue
		case '"':
			p.i++
			return p.s[st:p.i], nil
		}
		p.i++
	}
	return "", errJSON
}

func unquote(q string) string {
	if !strings.Contains(q, `\`) {
		return q[1 : len(q)-1]
	}
	var s string
	if json.Unmarshal([]byte(q), &s) != nil {
		return q[1 : len(q)-1]
	}
	return s
}

// parseJSON parses one complete JSON value occupying all of s.
func parseJSON(s string) (*jnode, error) {
	p := &jparser{s: s}
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, errJSON
	}
	return n, nil
}

func (n *jnode) value() Value {
	switch n.t {
	case 's':
		return Value{Kind: Str, S: unquote(n.raw)}
	case 'n':
		v := Infer(n.raw)
		return v
	case 'b':
		return Value{Kind: Bool, S: n.raw}
	}
	return Value{Kind: Null, S: "null"}
}

// flatten appends dotted-path fields for n.
func flatten(n *jnode, prefix string, out []Field) []Field {
	switch n.t {
	case 'o':
		for i, k := range n.keys {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			out = flatten(n.kids[i], key, out)
		}
	case 'a':
		for _, kid := range n.kids {
			out = flatten(kid, prefix+"[]", out)
		}
	default:
		if prefix == "" {
			prefix = "value"
		}
		v := n.value()
		if v.Kind == Str { // strings may still carry numbers/durations: "12ms"
			if iv := Infer(v.S); iv.Kind == Num && iv.Unit != "" {
				v = iv
			}
		}
		out = append(out, Field{Key: prefix, Val: v})
	}
	return out
}

// ---- pretty printing ------------------------------------------------------

func (n *jnode) compact(key string) (string, int) {
	switch n.t {
	case 'o', 'a':
		open, close := "{", "}"
		if n.t == 'a' {
			open, close = "[", "]"
		}
		var b strings.Builder
		w := 2
		b.WriteString(C(CPunct, open))
		for i, kid := range n.kids {
			if i > 0 {
				b.WriteString(C(CPunct, ", "))
				w += 2
			}
			k := key
			if n.t == 'o' {
				k = n.keys[i]
				q := `"` + n.keys[i] + `"`
				b.WriteString(C(CKey, q) + C(CPunct, ": "))
				w += len(q) + 2
			}
			s, sw := kid.compact(k)
			b.WriteString(s)
			w += sw
		}
		b.WriteString(C(CPunct, close))
		return b.String(), w
	}
	return ColorValue(key, n.value(), n.raw), len(n.raw)
}

func (n *jnode) pretty(key, indent string, width int, top bool, out []string, lead string, trail string) []string {
	if n.t == 'o' || n.t == 'a' {
		s, w := n.compact(key)
		if len(n.kids) == 0 || (!top && len(indent)+len(lead)+w+len(trail) <= min(width, 100)) {
			return append(out, indent+lead+s+trail)
		}
		open, close := "{", "}"
		if n.t == 'a' {
			open, close = "[", "]"
		}
		out = append(out, indent+lead+C(CPunct, open))
		in := indent + "  "
		for i, kid := range n.kids {
			t := ""
			if i < len(n.kids)-1 {
				t = C(CPunct, ",")
			}
			k, l := key, ""
			if n.t == 'o' {
				k = n.keys[i]
				l = C(CKey, `"`+k+`"`) + C(CPunct, ": ")
			}
			out = kid.pretty(k, in, width, false, out, l, t)
		}
		return append(out, indent+C(CPunct, close)+trail)
	}
	s, _ := n.compact(key)
	return append(out, indent+lead+s+trail)
}

// PrettyJSON renders a JSON text expanded and coloured; nil if invalid.
func PrettyJSON(s string, width int) []string {
	n, err := parseJSON(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return n.pretty("", "", width, true, nil, "", "")
}

// HighlightJSON colours JSON tokens in a fragment without reformatting.
func HighlightJSON(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(s) {
				j++
			} else {
				j = len(s)
			}
			tok := s[i:j]
			k := j
			for k < len(s) && s[k] == ' ' {
				k++
			}
			if k < len(s) && s[k] == ':' {
				b.WriteString(C(CKey, tok))
			} else {
				b.WriteString(C(CStr, tok))
			}
			i = j
		case strings.IndexByte("{}[],:", c) >= 0:
			b.WriteString(C(CPunct, string(c)))
			i++
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(s) && strings.IndexByte("0123456789.eE+-", s[j]) >= 0 {
				j++
			}
			b.WriteString(C(CNum, s[i:j]))
			i = j
		case strings.HasPrefix(s[i:], "true") || strings.HasPrefix(s[i:], "false"):
			n := 4
			if c == 'f' {
				n = 5
			}
			b.WriteString(C(CBool, s[i:i+n]))
			i += n
		case strings.HasPrefix(s[i:], "null"):
			b.WriteString(C(CNull, "null"))
			i += 4
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// ---- NDJSON format ----------------------------------------------------------

// JSON is newline-delimited JSON: one value per line.
type JSON struct{ graph string }

func (JSON) Name() string         { return "json" }
func (JSON) Structured() bool     { return true }
func (j JSON) GraphField() string { return j.graph }
func (JSON) NewParser() Parser    { return jsonParser{} }
func (JSON) Pretty(line string, w int) []string {
	return PrettyJSON(line, w)
}

type jsonParser struct{}

func (jsonParser) Parse(no int, line string) ([]Record, bool) {
	t := strings.TrimSpace(line)
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return nil, false
	}
	n, err := parseJSON(t)
	if err != nil {
		return nil, false
	}
	if n.t == 'a' { // a line holding an array of objects: one record each
		var recs []Record
		for _, k := range n.kids {
			recs = append(recs, Record{Fields: flatten(k, "", nil), Line: no, End: no})
		}
		return recs, true
	}
	return []Record{{Fields: flatten(n, "", nil), Line: no, End: no}}, true
}

func detectJSON(sample []string) (float64, Format) {
	p := jsonParser{}
	return fraction(sample, func(l string) bool { _, ok := p.Parse(0, l); return ok }), JSON{}
}
