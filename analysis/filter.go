package analysis

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Olian04/glimt/format"
)

// Filter is a conjunction of terms. Terms:
//
//	word        line contains word (case-insensitive)
//	-word       line does not contain word
//	/re/        line matches regex
//	key=val     field equals val (case-insensitive, * globs; key=* = present)
//	key!=val    field differs
//	key>n key>=n key<n key<=n   numeric comparison
//	key~re      field matches regex
type Filter struct {
	Src   string
	terms []fterm
}

type fterm struct {
	neg   bool
	key   string
	op    string
	val   string
	num   float64
	re    *regexp.Regexp // /re/, key~re, or a key=glob* pattern
	glob  bool
	numEq bool // key=n also matches numerically (200 = 200.0, 1s = 1000ms)
	lower string
}

var reFieldTerm = regexp.MustCompile(`^([A-Za-z_@][\w.\[\]@-]*)(!=|>=|<=|=|>|<|~)(.*)$`)

// ParseFilter parses filter syntax. The empty string is the match-all filter.
func ParseFilter(src string) (Filter, error) {
	f := Filter{Src: strings.TrimSpace(src)}
	for _, w := range splitTerms(f.Src) {
		t := fterm{}
		if strings.HasPrefix(w, "-") && len(w) > 1 {
			t.neg, w = true, w[1:]
		} else if strings.HasPrefix(w, "+") && len(w) > 1 {
			return f, fmt.Errorf("%s: no + needed, a bare term already keeps matching rows (write %s)", w, w[1:])
		}
		if len(w) >= 2 && w[0] == '/' && w[len(w)-1] == '/' {
			re, err := regexp.Compile("(?i)" + w[1:len(w)-1])
			if err != nil {
				return f, fmt.Errorf("bad regex %s: %v", w, err)
			}
			t.re = re
		} else if m := reFieldTerm.FindStringSubmatch(w); m != nil {
			t.key, t.op, t.val = m[1], m[2], unq(m[3])
			if t.op == "!=" { // key!=v is exactly -key=v
				t.op, t.neg = "=", !t.neg
			}
			switch t.op {
			case "=":
				if t.val != "*" && strings.ContainsAny(t.val, "*?") {
					// glob: * is any run of characters (including /), ? is one
					g := regexp.QuoteMeta(t.val)
					g = strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(g)
					t.re, t.glob = regexp.MustCompile("(?is)^"+g+"$"), true
				}
				if v := format.Infer(t.val); v.Kind == format.Num {
					t.num, t.numEq = v.N, true
				}
			case ">", ">=", "<", "<=":
				v := format.Infer(t.val)
				if v.Kind != format.Num {
					return f, fmt.Errorf("%s: %q is not a number", w, t.val)
				}
				t.num = v.N
			case "~":
				re, err := regexp.Compile("(?i)" + t.val)
				if err != nil {
					return f, fmt.Errorf("bad regex in %s: %v", w, err)
				}
				t.re = re
			}
			t.lower = strings.ToLower(t.val)
		} else {
			t.lower = strings.ToLower(unq(w))
		}
		f.terms = append(f.terms, t)
	}
	return f, nil
}

func unq(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// splitTerms splits on spaces outside quotes.
func splitTerms(s string) []string {
	var out []string
	var b strings.Builder
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			b.WriteByte(c)
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
			b.WriteByte(c)
		case c == ' ':
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// Empty reports whether the filter matches everything.
func (f Filter) Empty() bool { return len(f.terms) == 0 }

// HasFieldTerms reports whether any term needs structured fields.
func (f Filter) HasFieldTerms() bool {
	for _, t := range f.terms {
		if t.key != "" {
			return true
		}
	}
	return false
}

// Match tests a record (nil for a line that produced no record) and its text.
func (f Filter) Match(rec *format.Record, text string) bool {
	var lower string
	for _, t := range f.terms {
		var ok bool
		switch {
		case t.key != "":
			ok = rec != nil && t.matchField(rec)
		case t.re != nil:
			ok = t.re.MatchString(text)
		default:
			if lower == "" {
				lower = strings.ToLower(text)
			}
			ok = strings.Contains(lower, t.lower)
		}
		if ok == t.neg {
			return false
		}
	}
	return true
}

func (t fterm) matchField(rec *format.Record) bool {
	for _, fl := range rec.Fields {
		if fl.Key != t.key {
			continue
		}
		v := fl.Val
		switch t.op {
		case "=":
			if t.val == "*" {
				return true
			}
			switch {
			case t.glob:
				if t.re.MatchString(v.S) {
					return true
				}
			case strings.EqualFold(v.S, t.val):
				return true
			case t.numEq && v.Kind == format.Num && v.N == t.num:
				return true
			}
		case "~":
			if t.re.MatchString(v.S) {
				return true
			}
		default:
			if v.Kind != format.Num || v.N != v.N { // NaN
				continue
			}
			switch t.op {
			case ">":
				if v.N > t.num {
					return true
				}
			case ">=":
				if v.N >= t.num {
					return true
				}
			case "<":
				if v.N < t.num {
					return true
				}
			case "<=":
				if v.N <= t.num {
					return true
				}
			}
		}
	}
	return false // key absent, or no value matched
}
