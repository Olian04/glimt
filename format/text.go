package format

import (
	"regexp"
	"strings"
)

// Text is the fallback: every line is a record. Any key=value tokens in the
// line become fields, so semi-structured output (e.g. "icmp_seq=3 time=12.1
// ms") still gets field stats and graphs without command-specific code.
type Text struct{}

func (Text) Name() string                           { return "text" }
func (Text) Structured() bool                       { return false }
func (Text) GraphField() string                     { return "" }
func (Text) NewParser() Parser                      { return textParser{} }
func (Text) Pretty(line string, width int) []string { return BestEffort(line, width) }

type textParser struct{}

var reIdent = regexp.MustCompile(`^[A-Za-z_][\w.-]*$`)

func (textParser) Parse(no int, line string) ([]Record, bool) {
	rec := Record{Line: no, End: no}
	if strings.IndexByte(line, '=') >= 0 {
		for _, t := range lfTokens(line) {
			if t.pair && reIdent.MatchString(t.key) {
				rec.Fields = append(rec.Fields, Field{Key: t.key, Val: Infer(lfUnquote(t.val))})
			}
		}
	}
	return []Record{rec}, true
}

var reTok = regexp.MustCompile(strings.Join([]string{
	`(?P<ts>\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(?:[.,]\d+)?(?:Z|[+-]\d\d:?\d\d)?|\b\d\d:\d\d:\d\d(?:\.\d+)?\b|\b[A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d\b)`,
	`(?P<url>\b(?:https?|wss?|ftp)://[^\s"'<>]+)`,
	`(?P<uuid>\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b)`,
	`(?P<ip>\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b)`,
	`(?P<level>\b(?:ERROR|ERR|FATAL|CRITICAL|CRIT|PANIC|WARNING|WARN|INFO|NOTICE|DEBUG|TRACE)\b)`,
	`(?P<key>\b[A-Za-z_][\w.-]*=)`,
	`(?P<quoted>"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`,
	`(?P<hex>\b0x[0-9a-fA-F]+\b|\b[0-9a-f]{12,}\b)`,
	`(?P<num>\b\d+(?:\.\d+)?(?:ms|us|µs|ns|s|m|h|%|[KMGT]i?B|B)?\b)`,
}, "|"))

// Highlight colours recognisable tokens in arbitrary text.
func Highlight(s string) string {
	idx := reTok.FindAllStringSubmatchIndex(s, -1)
	if idx == nil {
		return s
	}
	names := reTok.SubexpNames()
	var b strings.Builder
	last := 0
	for _, m := range idx {
		b.WriteString(s[last:m[0]])
		tok := s[m[0]:m[1]]
		g := 1
		for ; g < len(names); g++ {
			if m[2*g] >= 0 {
				break
			}
		}
		switch names[g] {
		case "ts":
			b.WriteString(C(CTime, tok))
		case "url":
			b.WriteString(Underline(C(CURL, tok)))
		case "uuid", "hex":
			b.WriteString(C(CAccent, tok))
		case "ip":
			b.WriteString(C(CIP, tok))
		case "level":
			c, _ := LevelColor(tok)
			b.WriteString(Bold(C(c, tok)))
		case "key":
			b.WriteString(C(CKey, tok[:len(tok)-1]) + C(CPunct, "="))
		case "quoted":
			b.WriteString(C(CStr, tok))
		case "num":
			if glued(s, m[0], m[1]) { // part of a word like web-0-abc
				b.WriteString(tok)
			} else {
				b.WriteString(C(CNum, tok))
			}
		default:
			b.WriteString(tok)
		}
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// glued reports whether s[a:b] is stuck to a word through '-' or '_'.
func glued(s string, a, b int) bool {
	isW := func(c byte) bool { return c == '-' || c == '_' || c == '/' || (c|0x20 >= 'a' && c|0x20 <= 'z') }
	return (a > 0 && isW(s[a-1])) || (b < len(s) && (s[b] == '-' || s[b] == '_' || (s[b]|0x20 >= 'a' && s[b]|0x20 <= 'z')))
}

// BestEffort pretty-prints any line: embedded JSON is expanded, everything
// else is token-highlighted.
func BestEffort(line string, width int) []string {
	if i := strings.IndexAny(line, "{["); i >= 0 {
		end := strings.LastIndexAny(line, "}]")
		if end > i {
			if lines := PrettyJSON(line[i:end+1], width); lines != nil && strings.ContainsAny(line[i:end+1], ":,") {
				prefix := strings.TrimRight(line[:i], " ")
				suffix := strings.TrimSpace(line[end+1:])
				var out []string
				if prefix != "" {
					out = append(out, Highlight(prefix))
				}
				out = append(out, lines...)
				if suffix != "" {
					out = append(out, Highlight(suffix))
				}
				return out
			}
		}
	}
	return []string{Highlight(line)}
}

// Template reduces a message to its shape by replacing variable tokens with
// placeholders, so similar lines group together.
func Template(s string) string {
	idx := reTok.FindAllStringSubmatchIndex(s, -1)
	names := reTok.SubexpNames()
	var b strings.Builder
	last := 0
	for _, m := range idx {
		g := 1
		for ; g < len(names); g++ {
			if m[2*g] >= 0 {
				break
			}
		}
		ph := ""
		switch names[g] {
		case "ts":
			ph = "<TS>"
		case "url":
			ph = "<URL>"
		case "uuid":
			ph = "<UUID>"
		case "hex":
			ph = "<HEX>"
		case "ip":
			ph = "<IP>"
		case "quoted":
			ph = "<STR>"
		case "num":
			ph = "<NUM>"
		default:
			continue // keep levels and keys literal
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(ph)
		last = m[1]
	}
	b.WriteString(s[last:])
	t := b.String()
	if len(t) > 300 {
		t = t[:300]
	}
	return t
}
