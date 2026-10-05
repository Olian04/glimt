package test

import (
	"reflect"
	"testing"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/cli"
)

func TestParseArgs(t *testing.T) {
	o, err := cli.ParseArgs([]string{"-healthcheck", "level=error", "--mem", "64MB", "-f", "json", "dur>5", "-s", "-graph=x", "-level=info", "--", "--literal"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-healthcheck", "level=error", "dur>5", "-graph=x", "-level=info", "--literal"}
	if !reflect.DeepEqual(o.Positional, want) || o.Mem != "64MB" || o.Format != "json" || !o.Summary {
		t.Fatalf("%+v", o)
	}
	for _, bad := range [][]string{{"--mme", "1G"}, {"-v"}, {"--mem"}} {
		if _, err := cli.ParseArgs(bad); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
}

// The CLI and the TUI prompt share one syntax: arguments mean exactly what
// the same text typed into the / prompt means.
func TestCLIFilterIsPromptSyntax(t *testing.T) {
	for _, c := range []struct {
		args   []string
		prompt string
	}{
		{[]string{"msg=user login"}, "msg=user login"}, // two terms, as in the prompt
		{[]string{`msg="user login"`, "-healthcheck"}, `msg="user login" -healthcheck`},
		{[]string{"level=error", "dur>500ms"}, "level=error dur>500ms"},
	} {
		got, err := cli.Filter(c.args)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := analysis.ParseFilter(c.prompt)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: CLI parses differently from prompt %q", c.args, c.prompt)
		}
	}
}
