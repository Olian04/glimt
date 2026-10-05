package format

import "strings"

// JSONDoc is a JSON document spread over many lines (pretty-printed API
// output, `kubectl -o json`, `jq .`). Elements of a top-level array become
// records as soon as they close; a top-level object becomes records when it
// closes (its largest array of objects, e.g. "items", if it has one).
type JSONDoc struct{}

func (JSONDoc) Name() string                       { return "json-doc" }
func (JSONDoc) Structured() bool                   { return true }
func (JSONDoc) GraphField() string                 { return "" }
func (JSONDoc) NewParser() Parser                  { return &docParser{} }
func (JSONDoc) Pretty(line string, _ int) []string { return []string{HighlightJSON(line)} }

type docParser struct {
	depth    int
	inStr    bool
	esc      bool
	topArray bool
	doc      strings.Builder // whole document (top-level object)
	elem     strings.Builder // current top-level array element
	docStart int
	elStart  int
	inElem   bool
}

func (p *docParser) Parse(no int, line string) ([]Record, bool) {
	var recs []Record
	ok := true
	for i := 0; i < len(line); i++ {
		c := line[i]
		if p.depth == 0 && !p.inStr {
			switch c {
			case ' ', '\t', '\r':
				continue
			case '{':
				p.topArray = false
				p.doc.Reset()
				p.docStart = no
			case '[':
				p.topArray = true
				p.docStart = no
			default:
				ok = false
				continue
			}
		}
		// track structure
		if p.inStr {
			if p.esc {
				p.esc = false
			} else if c == '\\' {
				p.esc = true
			} else if c == '"' {
				p.inStr = false
			}
		} else {
			switch c {
			case '"':
				p.inStr = true
				if p.topArray && p.depth == 1 && !p.inElem { // string element
					p.inElem, p.elStart = true, no
					p.elem.Reset()
				}
			case '{', '[':
				p.depth++
				if p.topArray && p.depth == 2 && !p.inElem {
					p.inElem, p.elStart = true, no
					p.elem.Reset()
				}
			case '}', ']':
				p.depth--
			}
		}
		if p.topArray {
			if p.inElem {
				p.elem.WriteByte(c)
				if p.depth == 1 && !p.inStr && (c == '}' || c == ']' || c == '"') {
					if n, err := parseJSON(p.elem.String()); err == nil && n.t == 'o' {
						recs = append(recs, Record{Fields: flatten(n, "", nil), Line: p.elStart, End: no})
					}
					p.inElem = false
				}
			}
		} else {
			p.doc.WriteByte(c)
			if p.depth == 0 && c == '}' {
				recs = append(recs, p.finishObject(no)...)
			}
		}
	}
	if p.topArray && p.inElem {
		p.elem.WriteByte('\n')
	} else if !p.topArray && p.depth > 0 {
		p.doc.WriteByte('\n')
	}
	return recs, ok
}

func (p *docParser) finishObject(end int) []Record {
	n, err := parseJSON(p.doc.String())
	p.doc.Reset()
	if err != nil || n.t != 'o' {
		return nil
	}
	// largest array-of-objects child becomes the record set
	best := -1
	for i, k := range n.kids {
		if k.t == 'a' && len(k.kids) >= 2 && k.kids[0].t == 'o' &&
			(best < 0 || len(k.kids) > len(n.kids[best].kids)) {
			best = i
		}
	}
	if best < 0 {
		return []Record{{Fields: flatten(n, "", nil), Line: p.docStart, End: end}}
	}
	var recs []Record
	for _, el := range n.kids[best].kids {
		if el.t == 'o' {
			recs = append(recs, Record{Fields: flatten(el, "", nil), Line: p.docStart, End: end})
		}
	}
	return recs
}

func detectJSONDoc(sample []string) (float64, Format) {
	first := strings.TrimSpace(sample[0])
	if first == "" || (first[0] != '{' && first[0] != '[') {
		return 0, JSONDoc{}
	}
	if s, _ := detectJSON(sample); s > 0.5 {
		return 0, JSONDoc{}
	}
	p := &docParser{}
	for i, l := range sample {
		if _, ok := p.Parse(i, l); !ok {
			return 0.3, JSONDoc{}
		}
	}
	return 0.9, JSONDoc{}
}
