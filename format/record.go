// Package format turns lines of text into Records. Every input format is the
// same three things: a detector that scores a sample, a parser that turns a
// line into zero or more records, and a pretty printer for display.
package format

import (
	"math"
	"strconv"
	"strings"
)

// Kind is the inferred type of a value.
type Kind uint8

const (
	Str Kind = iota
	Num
	Bool
	Null
)

func (k Kind) String() string {
	return [...]string{"str", "num", "bool", "null"}[k]
}

// Value is a scalar field value. Num values keep their original text in S.
type Value struct {
	Kind Kind
	S    string
	N    float64
	Unit string // e.g. "ms" for durations, normalised
}

// Field is one key/value pair of a record.
type Field struct {
	Key string
	Val Value
}

// Record is the unit of analysis: an ordered set of fields that came from the
// source lines [Line, End].
type Record struct {
	Fields []Field
	Line   int
	End    int
}

// Get returns the first value for key.
func (r *Record) Get(key string) (Value, bool) {
	for _, f := range r.Fields {
		if f.Key == key {
			return f.Val, true
		}
	}
	return Value{}, false
}

// Message returns the record's human message field, if any.
func (r *Record) Message() (string, bool) {
	for _, k := range MessageKeys {
		if v, ok := r.Get(k); ok && v.Kind == Str {
			return v.S, true
		}
	}
	return "", false
}

// MessageKeys and LevelKeys are the conventional names, in priority order.
var (
	MessageKeys = []string{"msg", "message", "MESSAGE", "Message", "log", "event", "text", "_text"}
	LevelKeys   = []string{"level", "lvl", "severity", "loglevel", "log.level", "levelname", "Level", "LEVEL", "@l"}
)

var durUnits = []struct {
	suf string
	ms  float64
}{{"ns", 1e-6}, {"µs", 1e-3}, {"us", 1e-3}, {"ms", 1}, {"s", 1000}, {"m", 60000}, {"h", 3600000}}

// Infer types a raw string: number, duration (→ ms), bool, null or string.
func Infer(s string) Value {
	t := strings.TrimSpace(s)
	if t == "" {
		return Value{Kind: Str, S: s}
	}
	switch t {
	case "true", "false", "TRUE", "FALSE", "True", "False":
		return Value{Kind: Bool, S: t}
	case "null", "NULL", "nil", "None", "-":
		return Value{Kind: Null, S: t}
	}
	c := t[0]
	if !(c >= '0' && c <= '9') && c != '-' && c != '+' && c != '.' {
		return Value{Kind: Str, S: s}
	}
	if n, ok := parseNum(t); ok {
		return Value{Kind: Num, S: t, N: n}
	}
	for _, u := range durUnits {
		if strings.HasSuffix(t, u.suf) {
			if n, ok := parseNum(t[:len(t)-len(u.suf)]); ok {
				return Value{Kind: Num, S: t, N: n * u.ms, Unit: "ms"}
			}
		}
	}
	return Value{Kind: Str, S: s}
}

func parseNum(t string) (float64, bool) {
	if t == "" || strings.ContainsAny(t, "xXiInN_") { // no hex, inf, nan, 1_000
		return 0, false
	}
	// leading zeros like 007 or ids like 0123 are still numbers; reject
	// multi-dot strings (versions, IPs) which ParseFloat rejects anyway.
	n, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

// StrValue builds a string value.
func StrValue(s string) Value { return Value{Kind: Str, S: s} }
