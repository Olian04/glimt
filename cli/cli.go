// Package cli turns the command line into options and a filter.
package cli

import (
	"fmt"
	"strings"

	"github.com/Olian04/glimt/analysis"
)

// Options is a parsed command line.
type Options struct {
	Format, Regex, Graph, Config, Render, Tab, Mem string
	Summary, List, Version, Help                   bool
	Positional                                     []string
}

// ParseArgs: flags anywhere, as -x (one letter) or --name[=value]; "--"
// ends them. Anything else starting with one dash (-healthcheck,
// -level=info) is a negated filter term. Unknown --flags are errors, so
// typos aren't silently filters.
func ParseArgs(args []string) (Options, error) {
	o := Options{Tab: "input", Mem: "256MB"}
	str := map[string]*string{"f": &o.Format, "format": &o.Format, "r": &o.Regex, "regex": &o.Regex,
		"graph": &o.Graph, "config": &o.Config, "render": &o.Render, "tab": &o.Tab, "mem": &o.Mem}
	boo := map[string]*bool{"s": &o.Summary, "summary": &o.Summary, "list": &o.List,
		"version": &o.Version, "h": &o.Help, "help": &o.Help}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			o.Positional = append(o.Positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			o.Positional = append(o.Positional, a)
			continue
		}
		// Flags are -x (one letter) or --name[=value]. A single dash followed
		// by more than one character (-healthcheck, -level=info) is a filter.
		long := strings.HasPrefix(a, "--")
		name := strings.TrimLeft(a, "-")
		val, hasVal := "", false
		if long {
			if k, v, ok := strings.Cut(name, "="); ok {
				name, val, hasVal = k, v, true
			}
		} else if len(name) != 1 {
			o.Positional = append(o.Positional, a)
			continue
		}
		if p, ok := str[name]; ok {
			if !hasVal {
				if i+1 >= len(args) {
					return o, fmt.Errorf("%s needs a value", a)
				}
				i++
				val = args[i]
			}
			*p = val
			continue
		}
		if p, ok := boo[name]; ok && !hasVal {
			*p = true
			continue
		}
		if a == "-v" {
			return o, fmt.Errorf("-v: to exclude lines use -word, e.g. glimt -healthcheck")
		}
		if strings.HasPrefix(a, "--") {
			return o, fmt.Errorf("unknown flag %s (see --help)", a)
		}
		o.Positional = append(o.Positional, a) // -word: negated filter term
	}
	return o, nil
}

// Filter joins the arguments with spaces and parses them exactly as the
// TUI's / prompt parses its text: one syntax, whatever the shell did.
func Filter(args []string) (analysis.Filter, error) {
	return analysis.ParseFilter(strings.Join(args, " "))
}
