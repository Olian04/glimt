package format

import (
	"fmt"
	"regexp"
	"strings"
)

// RegexSpec describes a format as a regular expression whose named groups
// become fields. Built-in formats like nginx and syslog are just specs.
type RegexSpec struct {
	Name  string `yaml:"name"`
	Expr  string `yaml:"regex"`
	Graph string `yaml:"graph"` // default numeric field to graph
}

// Regex is a format defined by a RegexSpec.
type Regex struct {
	spec RegexSpec
	re   *regexp.Regexp
}

func (r Regex) Name() string       { return r.spec.Name }
func (Regex) Structured() bool     { return true }
func (r Regex) GraphField() string { return r.spec.Graph }
func (r Regex) NewParser() Parser  { return r }

func (r Regex) Parse(no int, line string) ([]Record, bool) {
	m := r.re.FindStringSubmatchIndex(line)
	if m == nil {
		return nil, false
	}
	rec := Record{Line: no, End: no}
	for i, name := range r.re.SubexpNames() {
		if i == 0 || name == "" || m[2*i] < 0 { // unnamed or didn't participate
			continue
		}
		rec.Fields = append(rec.Fields, Field{Key: name, Val: Infer(line[m[2*i]:m[2*i+1]])})
	}
	return []Record{rec}, true
}

func (r Regex) Pretty(line string, _ int) []string {
	m := r.re.FindStringSubmatchIndex(line)
	if m == nil {
		return nil
	}
	return []string{colorGroups(line, r.re, m)}
}

// Compile validates a spec.
func (s RegexSpec) Compile() (Regex, error) {
	re, err := regexp.Compile(s.Expr)
	if err != nil {
		return Regex{}, fmt.Errorf("format %q: %w", s.Name, err)
	}
	named := 0
	for _, n := range re.SubexpNames() {
		if n != "" {
			named++
		}
	}
	if named == 0 {
		return Regex{}, fmt.Errorf("format %q: regex needs at least one named group (?P<name>...)", s.Name)
	}
	return Regex{spec: s, re: re}, nil
}

// RegexDetector wraps a spec as a detector.
func RegexDetector(s RegexSpec) Detector {
	r, err := s.Compile()
	return Detector{Name: s.Name, Detect: func(sample []string) (float64, Format) {
		if err != nil {
			return 0, nil
		}
		// specific regexes beat logfmt/csv on equal coverage
		return fraction(sample, func(l string) bool { return r.re.MatchString(l) }) * 0.985, r
	}}
}

var builtinRegex = []RegexSpec{
	{
		Name:  "access-log", // nginx/apache combined & common
		Expr:  `^(?P<client>\S+) \S+ (?P<user>\S+) \[(?P<ts>[^\]]+)\] "(?P<method>[A-Z]+) (?P<path>\S+) (?P<proto>[^"]*)" (?P<status>\d{3}) (?P<bytes>\d+|-)(?: "(?P<referer>[^"]*)" "(?P<agent>[^"]*)")?(?: (?P<request_time>[\d.]+))?`,
		Graph: "bytes",
	},
	{
		Name: "syslog",
		Expr: `^(?:<(?P<pri>\d+)>)?(?P<ts>[A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d|\d{4}-\d\d-\d\dT[\d:.]+(?:Z|[+-]\d\d:?\d\d)?) (?P<host>\S+) (?P<app>[^:\[\s]+)(?:\[(?P<pid>\d+)\])?: (?P<msg>.*)$`,
	},
	{
		Name: "leveled", // "<timestamp> <LEVEL> message" application logs
		Expr: `^(?P<ts>\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(?:[.,]\d+)?(?:Z|[+-]\d\d:?\d\d)?)\s+\[?(?P<level>(?i:TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|ERR|FATAL|CRITICAL|PANIC))\]?:?\s+(?P<msg>.*)$`,
	},
}

// colorGroups colours each named capture group of a match by its name.
func colorGroups(line string, re *regexp.Regexp, m []int) string {
	var b strings.Builder
	last := 0
	names := re.SubexpNames()
	for g := 1; g < len(names); g++ {
		s, e := m[2*g], m[2*g+1]
		if s < 0 || s < last {
			continue
		}
		b.WriteString(C(CPunct, line[last:s]))
		val := line[s:e]
		if names[g] == "" {
			b.WriteString(val)
		} else {
			b.WriteString(ColorValue(names[g], Infer(val), val))
		}
		last = e
	}
	b.WriteString(C(CPunct, line[last:m[1]]))
	b.WriteString(line[m[1]:])
	return b.String()
}
