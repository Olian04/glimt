package format

import (
	"sort"
	"strings"
)

// Format is a configured input format (detection may tune it, e.g. a CSV
// separator or a table header).
type Format interface {
	Name() string
	// Structured formats are line formats: a line that doesn't fit is
	// "unparsed". Text is not structured, though its records may still carry
	// fields found in the line (key=value pairs).
	Structured() bool
	// NewParser returns a fresh, possibly stateful, parser.
	NewParser() Parser
	// Pretty renders one source line for display; nil means "can't".
	Pretty(line string, width int) []string
	// GraphField is the numeric field to graph by default ("" = none).
	GraphField() string
}

// Parser converts lines into records. OK reports that the line was
// recognised by the format, even if it produced no record (e.g. a header).
type Parser interface {
	Parse(lineNo int, line string) (recs []Record, ok bool)
}

// Detector scores how well a sample of lines fits a format, returning a
// configured instance. Scores are in [0,1].
type Detector struct {
	Name   string
	Detect func(sample []string) (float64, Format)
}

// Registry holds detectors in priority order (ties go to the earlier one).
type Registry struct {
	Detectors []Detector
}

// MinScore is the threshold below which input is treated as plain text.
const MinScore = 0.6

// Default builds the registry: user regex formats first (most specific),
// then built-ins, with plain text as the fallback.
func Default(user []RegexSpec) *Registry {
	r := &Registry{}
	for _, u := range user {
		r.Detectors = append(r.Detectors, RegexDetector(u))
	}
	r.Detectors = append(r.Detectors,
		Detector{"json", detectJSON},
		Detector{"json-doc", detectJSONDoc},
	)
	for _, b := range builtinRegex {
		r.Detectors = append(r.Detectors, RegexDetector(b))
	}
	r.Detectors = append(r.Detectors,
		Detector{"logfmt", detectLogfmt},
		Detector{"csv", detectCSV},
		Detector{"table", detectTable},
	)
	return r
}

// Names lists all format names, plus "text".
func (r *Registry) Names() []string {
	var out []string
	for _, d := range r.Detectors {
		out = append(out, d.Name)
	}
	return append(out, "text")
}

// Candidate is one detector's verdict.
type Candidate struct {
	Score  float64
	Format Format
}

// Detect returns the best format for sample (plain text if nothing fits).
func (r *Registry) Detect(sample []string) (Format, float64) {
	sample = nonEmpty(sample)
	if len(sample) == 0 {
		return Text{}, 0
	}
	best := Candidate{Score: -1}
	for _, d := range r.Detectors {
		s, f := d.Detect(sample)
		if f != nil && s > best.Score+1e-9 {
			best = Candidate{s, f}
		}
	}
	if best.Score < MinScore {
		return Text{}, best.Score
	}
	return best.Format, best.Score
}

// Force returns the named format configured from sample, even if it scores
// poorly.
func (r *Registry) Force(name string, sample []string) (Format, bool) {
	if name == "text" {
		return Text{}, true
	}
	sample = nonEmpty(sample)
	for _, d := range r.Detectors {
		if d.Name == name {
			_, f := d.Detect(sample)
			if f == nil {
				return nil, false
			}
			return f, true
		}
	}
	return nil, false
}

func nonEmpty(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// fraction is the share of lines for which ok returns true.
func fraction(lines []string, ok func(string) bool) float64 {
	if len(lines) == 0 {
		return 0
	}
	n := 0
	for _, l := range lines {
		if ok(l) {
			n++
		}
	}
	return float64(n) / float64(len(lines))
}

// sortedKeys returns map keys sorted.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
