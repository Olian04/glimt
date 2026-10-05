//go:build !glimt_nodeps

package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Olian04/glimt/format"
)

func writeConfig(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfig(t *testing.T) {
	c, err := format.LoadConfig(writeConfig(t, `
# my formats
formats:
  - name: myapp
    regex: '^(?P<ts>\S+) \[(?P<level>\w+)\] (?P<msg>.*?) took=(?P<took>\S+)$'
    graph: took
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Formats) != 1 || c.Formats[0].Name != "myapp" || c.Formats[0].Graph != "took" || !strings.HasPrefix(c.Formats[0].Expr, `^(?P<ts>\S+)`) {
		t.Fatalf("%+v", c)
	}
	if c, err := format.LoadConfig(writeConfig(t, "")); err != nil || len(c.Formats) != 0 {
		t.Fatalf("empty file: %v %+v", err, c)
	}
	for body, want := range map[string]string{
		"formats:\n  - name: a\n    regx: 'x'\n":                                         "regx",
		"formats:\n  - name: a\n":                                                        "missing regex",
		"formats:\n  - regex: '(?P<a>x)'\n":                                              "missing name",
		"formats:\n  - name: a\n    regex: 'nogroups'\n":                                 "named group",
		"formats:\n  - {name: a, regex: '(?P<x>a)'}\n  - {name: a, regex: '(?P<x>a)'}\n": "twice",
	} {
		if _, err := format.LoadConfig(writeConfig(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want error containing %q", body, err, want)
		}
	}
	if _, err := format.LoadConfig(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("explicit missing path should be an error")
	}
}
