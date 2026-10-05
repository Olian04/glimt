// glimt: pipe any program's output in and get structured, live analytics.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/cli"
	"github.com/Olian04/glimt/format"
	"github.com/Olian04/glimt/term"
	"github.com/Olian04/glimt/ui"
)

// version is set at release time (-ldflags "-X main.version=vX.Y.Z"). Builds
// made by `go install …@vX.Y.Z` or `go tool glimt` carry it in the build info
// instead, and so does a plain source build (as a VCS pseudo-version). "dev"
// is only for builds with no build info at all.
var version string

func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Path == bi.Path &&
		bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

const usage = `glimt — pipe anything in, get structure out

usage:
  some-command | glimt [flags] [filter...]
  glimt [flags] [filter...] < file

Input is always stdin; with nothing piped in, glimt prints this help.

The arguments are the filter, in exactly the syntax of the / prompt in the
TUI: they are joined with spaces and parsed as one filter (all terms must
match):
  word  -word  /regex/  key=val  key!=val  key=*  key=glob*
  key>n  key>=n  key<n  key<=n  key~regex   value with spaces: key="a b"
  e.g.  kubectl logs -f api | glimt level=error 'dur>500ms' -healthcheck
The shell strips its own quotes first, so quote what glimt should see:
  glimt 'msg="user login"'      (not: glimt msg="user login")

When stdout is a terminal glimt opens the TUI; otherwise it streams the
matching lines to stdout like grep (exit 0 if anything matched, else 1).

flags:
  -f, --format NAME   force a format instead of auto-detecting
  -r, --regex EXPR    parse lines with a regex; named groups (?P<name>...) become fields
      --graph FIELD   default field to graph (with --regex)
      --config PATH   config file (default ~/.config/glimt/config.yaml)
      --mem SIZE      memory cap for cached rows (default 256MB; e.g. 64MB, 2GB)
  -s, --summary       print a text summary after EOF instead of the TUI
      --render WxH    render one TUI frame after EOF to stdout (e.g. 120x40)
      --tab NAME      view for --render: input, fields, graph, patterns
      --list          list known formats
      --version
  -h, --help
  --                  end of flags (everything after is filter)

formats: json, json-doc, access-log, syslog, leveled, logfmt, csv/tsv,
table (kubectl/ps/docker style), text, plus your own regex formats.

config.yaml (your own formats, tried before the built-in ones):
  formats:
    - name: myapp
      regex: '^(?P<ts>\S+) (?P<level>\w+) (?P<msg>.*)$'
      graph: latency_ms
`

func main() {
	o, err := cli.ParseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "glimt:", err)
		os.Exit(2)
	}
	if o.Help {
		fmt.Print(usage)
		return
	}
	if o.Version {
		fmt.Println("glimt", buildVersion())
		return
	}
	if isTTY(os.Stdin) && !o.List { // nothing piped in
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	memCap, err := parseSize(o.Mem)
	if err != nil {
		fail(err)
	}
	// Let the GC work towards the cap too (rows + bounded counters + slack).
	debug.SetMemoryLimit(memCap + memCap/2 + 64<<20)
	cfg, err := format.LoadConfig(o.Config)
	if err != nil {
		fail(err)
	}
	user := cfg.Formats
	forced := o.Format
	if o.Regex != "" {
		spec := format.RegexSpec{Name: "regex", Expr: o.Regex, Graph: o.Graph}
		if _, err := spec.Compile(); err != nil {
			fail(err)
		}
		user = append([]format.RegexSpec{spec}, user...)
		if forced == "" {
			forced = "regex"
		}
	}
	reg := format.Default(user)
	if o.List {
		fmt.Println(strings.Join(reg.Names(), "\n"))
		return
	}
	if forced != "" {
		if _, ok := reg.Force(forced, nil); !ok && forced != "text" {
			fail(fmt.Errorf("unknown format %q (try --list)", forced))
		}
	}

	// The arguments are one filter, joined exactly as if typed into the
	// TUI's / prompt, so the CLI and the TUI share a single syntax.
	filter, err := cli.Filter(o.Positional)
	if err != nil {
		fail(err)
	}
	var in io.Reader = os.Stdin

	store := analysis.NewStore(memCap, nil)
	an := analysis.New(store, reg, forced)
	an.SetFilter(filter)

	switch {
	case o.Render != "":
		var w, h int
		if _, err := fmt.Sscanf(o.Render, "%dx%d", &w, &h); err != nil || w < 20 || h < 5 {
			fail(fmt.Errorf("--render wants WxH, e.g. 120x40"))
		}
		go read(in, store)
		an.RunToEOF(nil)
		app := ui.New(an)
		app.W, app.H = w, h
		app.Frame() // first frame computes the available tabs
		if err := app.SelectTab(o.Tab); err != nil {
			fail(err)
		}
		for _, l := range app.Frame() {
			fmt.Println(l + "\x1b[0m")
		}
		return
	case o.Summary:
		go read(in, store)
		an.RunToEOF(nil)
		ui.Report(os.Stdout, an, isTTY(os.Stdout))
		return
	case !isTTY(os.Stdout):
		os.Exit(pipe(in, store, an))
	}

	t, err := term.Open()
	if err != nil { // no terminal at all: degrade to a summary
		go read(in, store)
		an.RunToEOF(nil)
		ui.Report(os.Stdout, an, false)
		return
	}
	go read(in, store)
	stop := make(chan struct{})
	go an.Run(stop)
	app := ui.New(an)
	func() {
		defer t.Close() // also runs on panic, so the terminal is restored first
		app.Run(t)
	}()
	close(stop)
	store.Unpin()
}

// pipe streams matching lines to stdout, grep-style. Exit code 0 if any line
// was written, 1 if none.
func pipe(in io.Reader, store *analysis.Store, an *analysis.Analyzer) int {
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	n := 0
	an.SetSink(func(line string) {
		out.WriteString(line)
		out.WriteByte('\n')
		n++
	})
	go read(in, store)
	an.RunToEOF(func() { out.Flush() }) // flush whenever caught up: live streams stay live
	if n == 0 {
		return 1
	}
	return 0
}

func read(in io.Reader, store *analysis.Store) {
	br := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			store.Append(line)
		}
		if err != nil {
			break
		}
	}
	store.Close()
}

// parseSize parses "256MB", "2GB", "512k", "1048576".
func parseSize(s string) (int64, error) {
	u := strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, x := range []struct {
		suf string
		m   int64
	}{{"GB", 1 << 30}, {"G", 1 << 30}, {"MB", 1 << 20}, {"M", 1 << 20}, {"KB", 1 << 10}, {"K", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(u, x.suf) {
			u, mult = strings.TrimSpace(strings.TrimSuffix(u, x.suf)), x.m
			break
		}
	}
	var n float64
	if _, err := fmt.Sscanf(u, "%g", &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("--mem: bad size %q", s)
	}
	return int64(n * float64(mult)), nil
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "glimt:", err)
	os.Exit(1)
}
