# glimt design (locked)

The look and behaviour below are fixed by golden snapshot tests in
`test/testdata/golden/` (84 frames: 14 scenarios × each available view × 120×32
and 72×20, colours included). `go test ./...` fails on any visual change.

To change the design on purpose:

```sh
go test ./test -update           # rewrite the snapshots (or: make update)
git diff test/testdata/golden    # review every changed frame
```

A failing test prints the first differing rows, says when a change is
colour-only, and writes the new frame next to the old one as `*.ansi.got`.
Run `cat` on the file in a terminal to see it.

## Invariants

**Screen layout:** four bands, top to bottom.

1. **Stats bar.** One row on background 236. Segments are separated by a dim
   ` │ `, and when space runs out the lowest-priority segments drop first.
   The segments, with their priority:

   | Segment | Shown when | Priority |
   |---|---|---|
   | `◆ format score%` | always | 10 |
   | lines | always | 9 |
   | filter matches | a filter is set | 8 |
   | state `● live` / `■ done` / `◌ n%` and elapsed | always | 7 |
   | levels `E W I D` | levels found | 6 |
   | counters-since | counters don't cover the whole stream | 6 |
   | `rec` | structured format | 5 |
   | `mem used/cap` and `window` | cache over half full / evicting | 5 |
   | parsed % (warns below 95%) | structured format | 4 |
   | bytes | always | 3 |
   | rate and 12-cell sparkline | more than one second of input | 2 |

2. **Tab bar.** Numbered tabs on the left. The active tab is shown inverse on
   accent 141. The flags `pretty wrap follow ? help` sit on the right: green
   when on, dim when off.
3. **View.** All remaining rows.
4. **Footer.** Hints for the current view, the filter prompt (`/`), or a
   3-second message.

**Views:** each one exists only when the data supports it.

| View | Shown when |
|---|---|
| Input | always |
| Fields | any record has fields |
| Graph | a numeric field has two or more cached points, or the stream has run for 3s or more |
| Patterns | more than one message template |

**Palette:** xterm-256, defined only in `format/style.go`.

| Element | Colour |
|---|---|
| key | 75 |
| string | 114 |
| number | 221 |
| bool | 176 |
| null and time | 244 |
| punctuation | 245 |
| error | 203, bold for level and status words |
| warn | 214 |
| info / ok | 78 |
| debug | 111 |
| accent | 141 |
| selection background | 238 |
| bar background | 236 |

Values take their colour from what they mean before their type: level,
time, HTTP status, state words (`Running`, `CrashLoopBackOff`), and the
message (bright white).

**Glyphs:**

| Element | Glyph |
|---|---|
| gutter | `│` (dim) |
| unparsed row | `┃` (red) |
| graph line | braille (75) over a dim envelope (24) |
| histogram | `▁▂▃▄▅▆▇█` |
| top-value bars | `█` |
| pattern bars | `▮` |
| section titles | `── title ───` (accent) |
| help box | rounded box `╭╮╰╯` |

**Pretty print:** on for known formats and off for `text`. Pressing `p`
pins the user's choice, so later changes in format detection no longer
override it.

## Data model behind the views

- **Counters** cover every record since the last replay and survive
  eviction. They are: count, presence, types, min, max, mean, std, distinct
  and top values, levels and patterns.
- **Window** stats are computed from rows still in the memory-capped cache:
  percentiles, histogram, graph series and the input view. When the two
  differ, the UI says so: `window N rows` in the stats bar, and a note above
  the histogram in Fields.
