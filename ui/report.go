package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/term"
)

// Report writes a plain-text summary (for --summary and non-tty use).
func Report(w io.Writer, an *analysis.Analyzer, color bool) {
	info := an.Store().Info()
	var b strings.Builder
	an.Read(func(r *analysis.Result) {
		fmt.Fprintf(&b, "format   %s (score %.2f)\n", r.Format.Name(), r.Score)
		fmt.Fprintf(&b, "lines    %s  (%s)\n", commas(info.Lines), humanBytes(info.Bytes))
		if r.Format.Structured() {
			fmt.Fprintf(&b, "parsed   %s  records %s", pct(r.Parsed, max(1, r.Lines)), commas(r.Records))
			if r.Filtering() {
				fmt.Fprintf(&b, "  matched %s", commas(r.Matched))
			}
			b.WriteString("\n")
		}
		if lv := levelSummary(r.Levels); lv != "" {
			b.WriteString("levels   " + lv + "\n")
		}
		if info.Evicted > 0 {
			fmt.Fprintf(&b, "cache    %s rows (%s of %s); percentiles cover the cache\n", commas(info.Cached), humanBytes(info.Used), humanBytes(info.Cap))
		}
		if fs := r.SortedFields(); len(fs) > 0 {
			b.WriteString("\nfields\n")
			for i, f := range fs {
				if i == 40 {
					fmt.Fprintf(&b, "  … %d more\n", len(fs)-40)
					break
				}
				sum := topSummary(f, 4)
				if f.Numeric() {
					sum = numSummary(f)
				}
				fmt.Fprintf(&b, "  %-24s %-6s %6s  %s\n", term.Truncate(f.Key, 24), f.Type(), pct(f.Records, r.Matched), sum)
			}
		}
		if ps := r.SortedPatterns(); len(ps) > 1 {
			b.WriteString("\npatterns\n")
			for i, p := range ps {
				if i == 10 {
					break
				}
				fmt.Fprintf(&b, "  %8s  %s\n", commas(p.Count), term.Truncate(colorTemplate(p.Template), 110))
			}
		}
	})
	out := b.String()
	if !color {
		ls := strings.Split(out, "\n")
		for i := range ls {
			ls[i] = term.Strip(ls[i])
		}
		out = strings.Join(ls, "\n")
	}
	io.WriteString(w, out)
}
