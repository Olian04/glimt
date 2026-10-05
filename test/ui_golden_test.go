package test

// Golden snapshot tests lock the design down: every view of every sample
// input is rendered at two terminal sizes and compared byte-for-byte
// (colours included) with test/testdata/golden/*.ansi.
//
//	go test ./test               # fails on any visual change
//	go test ./test -update       # accept an intended change, then review the diff
//
// A failure prints the first differing rows as plain text (or says the
// difference is colour-only) and writes the new frame next to the golden
// file as *.got for inspection (cat it in a terminal to see colours).

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/format"
	"github.com/Olian04/glimt/term"
	"github.com/Olian04/glimt/ui"
)

var update = flag.Bool("update", false, "rewrite golden files")

type scenario struct {
	name   string
	file   string        // under testdata
	step   time.Duration // fake time between lines
	mem    int64         // cache cap (0 = default)
	filter string
	keys   string // keys pressed before rendering (runes)
}

var scenarios = []scenario{
	{name: "ndjson", file: "app.ndjson"},
	{name: "ndjson-raw", file: "app.ndjson", keys: "p"},
	{name: "logfmt", file: "app.logfmt"},
	{name: "logfmt-filter", file: "app.logfmt", filter: "level=error dur>500ms"},
	{name: "csv", file: "data.csv"},
	{name: "table", file: "pods.txt"},
	{name: "jsondoc", file: "doc.json"},
	{name: "access", file: "access.log"},
	{name: "leveled", file: "app.log"},
	{name: "text", file: "plain.txt"},
	{name: "text-pretty", file: "plain.txt", keys: "p"},
	{name: "text-kv", file: "ping.txt", step: time.Second},
	{name: "evicted", file: "app.ndjson", mem: 64 << 10},
	{name: "help", file: "app.logfmt", keys: "?"},
}

var sizes = [][2]int{{120, 32}, {72, 20}}

func load(t *testing.T, sc scenario) *analysis.Analyzer {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", sc.file))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	now := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st := analysis.NewStore(sc.mem, clock)
	an := analysis.New(st, format.Default(nil), "")
	if sc.filter != "" {
		fl, err := analysis.ParseFilter(sc.filter)
		if err != nil {
			t.Fatal(err)
		}
		an.SetFilter(fl)
	}
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			st.Append(line)
			an.Drain() // synchronous, so eviction and detection are deterministic
			now = now.Add(sc.step)
		}
		if err != nil {
			break
		}
	}
	st.Close()
	an.Drain()
	return an
}

func TestGolden(t *testing.T) {
	for _, sc := range scenarios {
		for _, sz := range sizes {
			an := load(t, sc)
			app := ui.New(an)
			app.W, app.H = sz[0], sz[1]
			for _, r := range sc.keys {
				app.Handle(term.Key{Kind: term.KRune, Rune: r})
			}
			app.Frame() // computes available tabs
			for _, tab := range app.TabNames() {
				if sc.keys == "?" && tab != "input" {
					continue // the overlay looks the same on every tab
				}
				if err := app.SelectTab(tab); err != nil {
					t.Fatal(err)
				}
				name := fmt.Sprintf("%s_%s_%dx%d", sc.name, tab, sz[0], sz[1])
				t.Run(name, func(t *testing.T) {
					got := strings.Join(app.Frame(), "\n") + "\n"
					check(t, name, got)
				})
			}
		}
	}
}

func check(t *testing.T, name, got string) {
	path := filepath.Join("testdata", "golden", name+".ansi")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Remove(path + ".got")
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run: go test ./test -update)", path)
	}
	if string(want) == got {
		os.Remove(path + ".got")
		return
	}
	os.WriteFile(path+".got", []byte(got), 0o644)
	wl, gl := strings.Split(string(want), "\n"), strings.Split(got, "\n")
	var diff []string
	for i := 0; i < max(len(wl), len(gl)) && len(diff) < 12; i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w == g {
			continue
		}
		ws, gs := term.Strip(w), term.Strip(g)
		if ws == gs {
			diff = append(diff, fmt.Sprintf("row %2d: colours/attributes changed: %s", i+1, ws))
		} else {
			diff = append(diff, fmt.Sprintf("row %2d:\n  want %s\n  got  %s", i+1, ws, gs))
		}
	}
	t.Errorf("design changed for %s (accept with: go test ./test -update)\n%s\nnew frame written to %s.got",
		name, strings.Join(diff, "\n"), path)
}
