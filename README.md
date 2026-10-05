# glimt

Pipe any program's output into `glimt` (Swedish for *glimpse*). It works out the format, parses the input as it arrives, and shows live statistics in a terminal UI. Arguments are a filter, the way a pattern is for grep.

```sh
alias g=glimt                               # like k=kubectl

kubectl logs -f deploy/api | g              # everything
kubectl logs -f deploy/api | g level=error  # just errors: stats, fields, graphs of those
kubectl get pods -A | g status!=Running
ping 1.1.1.1 | g
g 'status>=500' -healthcheck < access.log  # input is always stdin
kubectl logs api | g level=error > errors.log   # not a terminal? streams matching lines, like grep
```

## Install

Each [GitHub release](https://github.com/Olian04/glimt/releases) has binaries for Linux and macOS, on amd64 and arm64:

```sh
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -fsSL -o glimt "https://github.com/Olian04/glimt/releases/latest/download/glimt-${os}-${arch}"
chmod +x glimt && sudo mv glimt /usr/local/bin/
```

`checksums.txt` is on the same page. `latest` skips prereleases; for one of those, use `releases/download/<tag>/glimt-${os}-${arch}`.

With the Go toolchain:

```sh
go install github.com/Olian04/glimt@latest   # into $(go env GOBIN), or $(go env GOPATH)/bin
```

As a Go tool (Go 1.24 or later), pinned in your project's `go.mod`:

```sh
go get -tool github.com/Olian04/glimt@latest
kubectl logs -f deploy/api | go tool glimt level=error
```

To try it once without installing anything: `go run github.com/Olian04/glimt@latest --version`.

glimt runs on Linux and macOS. Windows isn't supported.

## Shell setup

```sh
# bash
alias g=glimt
# zsh: noglob stops zsh from expanding * in filters like path=/api/*
alias g='noglob glimt'
```

The arguments are one filter, in exactly the syntax of the TUI's `/` prompt: glimt joins them with spaces and parses the result. The shell strips its own quoting first, so quote whatever glimt itself should see:

- `g 'dur>500ms'`: quote `>` and `<`, or the shell treats them as redirection.
- `g 'msg="user login"'`: the outer quotes are for the shell, the inner ones are for glimt. This is exactly what you'd type in the prompt: `msg="user login"`.
- `g msg="user login"` is the same as typing `msg=user login` in the prompt: the term `msg=user` plus the word `login`.

With nothing piped in, glimt prints its help and exits. To read a file, redirect it: `g < app.log`.

Every screen has the same layout:

```
 ◆ logfmt 97% │ 12,345 lines │ 1.2 MB │ parsed 100% │ 12,345 rec │ 340/s ▂▃▅▇ │ E 12 W 40 I 900 │ ● live 3m10s
 1 Input   2 Fields   3 Graph   4 Patterns                                  pretty wrap follow ? help
 …view…
 footer: key hints / filter prompt
```

The top row always shows stats about the stream: the detected format and its confidence, line and byte counts, parse rate, records, input rate with a sparkline, log level counts, the number of filter matches, live/done state, and, once the cache fills, memory use and how many rows are cached.

Tabs only appear when the data supports them:

| View     | Shown when | What it shows |
|----------|------------|---------------|
| Input    | always | The input, follow mode, line numbers. Lines the format couldn't parse are marked with a red bar. |
| Fields   | the format is structured | One row per key: type, presence %, distinct count, then either top values or min/p50/avg/p95/max. The selected key gets a top-values bar chart and, for numbers, percentiles and a histogram. |
| Graph    | there are numeric fields, or the stream has run for 3s or more | A braille line chart of any numeric field in the cached rows, plus input lines/s. By default it shows the most varied field that isn't a steadily increasing counter. |
| Patterns | messages vary | Messages grouped into templates (`Starting worker <NUM> on host-<NUM>`), with counts and an example. |

## Pretty printing

`pretty` is **on** when the format is known and **off** for plain text. Press `p` to turn on best-effort pretty printing for any input. Best-effort mode highlights timestamps, levels, IPs, URLs, numbers, quoted strings and `key=` pairs, and expands JSON embedded in a line (`2026-… started {"pid": 1}`).

| Format | Pretty print |
|---|---|
| json (NDJSON) | Expanded and coloured. Small nested values are kept on one line. |
| json-doc (multi-line JSON, `kubectl -o json`, `jq .`) | Syntax colouring |
| logfmt | Keys and values coloured, with level, status, duration and message meaning applied |
| csv / tsv | Aligned columns, numbers right-aligned, header in bold |
| table (`kubectl get`, `ps`, `docker ps`, `df`) | Header in bold, each cell coloured by meaning (`CrashLoopBackOff` in red) |
| access-log, syslog, leveled, regex | Each captured field coloured by its name |

## Formats and detection

glimt scores a growing sample (1, 2, 4 … 200 lines) against every format and picks the best one above 0.6. Anything below that is `text`. Ties go to the more specific format. Press `F` to cycle through formats by hand, or pass `-f NAME`.

Built-in formats:

- `json` (NDJSON)
- `json-doc` (each element of a top-level array becomes a record, or the largest array of objects such as `items`)
- `access-log` (nginx/apache combined)
- `syslog`
- `leveled` (`<ts> <LEVEL> msg`)
- `logfmt`
- `csv`/`tsv` (separator and header are detected)
- `table`
- `text`: any `key=value` tokens in a line become fields, so semi-structured output still gets Fields and Graph tabs. For example, `ping` output gets `icmp_seq`, `ttl` and `time`. glimt has no command-specific parsers.

**Regex formats.** Any line shape a regex can describe can be a format. Named groups become fields:

```sh
glimt -r '^(?P<method>\w+) (?P<path>\S+) (?P<status>\d+) (?P<took>\S+)$' --graph took
```

To have glimt detect your formats automatically, add them to the config file at `~/.config/glimt/config.yaml`, or pass `--config PATH`. User formats are tried before the built-in ones. Defining formats is the config file's only job.

```yaml
formats:
  - name: myapp
    regex: '^(?P<ts>\S+) \[(?P<level>\w+)\] (?P<msg>.*?) took=(?P<took>\S+)$'
    graph: took        # optional: default field for the Graph tab
```

Put regexes in single quotes so backslashes stay literal. The file is parsed strictly: an unknown key (`regx:`), a missing name or regex, a duplicate name, or a regex without named groups is reported with the file path.

Values are typed by inference: number, bool, null or string. Durations like `12ms`, `1.5s` or `2m` become numbers in milliseconds, so `dur>200` works whether the source wrote `200ms` or `0.2s`.

## Filter

Pass the filter as arguments (`g level=error`), or press `/` in the TUI to edit it. The prompt starts with the current filter. Enter applies it and Esc clears it. The filter defines the working set for everything: the input view, the stats bar, fields, graphs and patterns.

Input is always stdin; arguments are never file names. `-word` excludes lines, and a bare `word` keeps only lines containing it. A leading `+` isn't needed, so `+word` is rejected with a hint rather than quietly searching for the literal text "+word". `g -v` is also rejected with a hint. Unknown `--flags` are errors, so a typo never silently becomes a filter.

**Pipe mode.** When stdout isn't a terminal, glimt prints the matching lines and exits 0 if any matched, or 1 if none did. Output is flushed whenever glimt catches up with its input, so `tail -f | g level=error | tee err.log` stays live. For `json-doc` input, a matching item inside a top-level object, such as `kubectl get -o json`, prints the whole document. Use `-o json | jq -c '.items[]' | g …` to filter items one per line.

### Filter syntax

```
filter  = term { " " term }              all terms must match (AND); empty = everything
term    = [ "-" ] atom                   "-" negates any atom
atom    = field | regex | word
field   = key op value
key     = ( letter | "_" | "@" ) { letter | digit | "_" | "." | "-" | "[" | "]" | "@" }
op      = "=" | "!=" | ">" | ">=" | "<" | "<=" | "~"
regex   = "/" re2 "/"
word    = any other text
value   = text, optionally wrapped in '…' or "…" (needed for spaces)
```

| Term | Keeps a record when |
|---|---|
| `word` | the record's text contains `word` (case-insensitive) |
| `/re/` | the record's text matches the RE2 regex (case-insensitive) |
| `key=val` | some value of `key` equals `val`: case-insensitive, or numerically equal (`code=200` matches `200.0`, `dur=1s` matches `1000ms`) |
| `key=a*b?` | some value matches the glob: `*` is any run of characters (including `/`), `?` is one character, and the whole value must match |
| `key=*` | `key` is present |
| `key>n` `>=` `<` `<=` | some numeric value compares true. `n` must be a number; durations (`500ms`, `1.5s`) are in milliseconds |
| `key~re` | some value of `key` matches the regex (unanchored, case-insensitive) |
| `-term` | `term` does not match |
| `key!=val` | exactly `-key=val`: no value of `key` equals `val`, or `key` is absent. Globs and numeric equality work the same as with `=` |

The details:

- **Keys** are case-sensitive field names, as shown in Fields. Nested JSON uses dots (`http.status`), array elements use `[]` (`tags[]`), and table and CSV columns use their lower-cased header.
- **Multi-valued keys** (arrays) match if *any* value matches. A negation therefore means *no* value matches.
- **A missing key** fails every field atom, so `-key=…` and `key!=…` keep records that don't have the key at all. Use `key=* key!=x` to require the key.
- **The record's text** is its source line, or all of its lines for a multi-line JSON record. Lines that produced no record, like headers, have no fields. Field atoms are false for them, so `word` and `/re/` can match them, and so can negated field terms.
- **Splitting:** terms split on spaces outside `'…'` or `"…"`, and the operator is the first one that appears after the key. Command-line arguments are joined with spaces before parsing, so the CLI and the `/` prompt read the same text the same way.
- **Rejected terms:** `+term` is refused with a hint, because a bare term already keeps matching rows. Comparisons against non-numbers and invalid regexes are errors.

All terms must match: `level=error path=/api/* dur>500ms -healthcheck`. In Fields, press Enter on a string key to add `key=<top value>` to the filter, or on a numeric key to graph it.

## Keys

| | |
|---|---|
| `tab` `1`–`4` | switch view |
| `j/k` `↑/↓` `space/b` `PgUp/PgDn` `g/G` | scroll / select (`G` resumes follow) |
| `←/→` | pan (when not wrapping) / choose graph series |
| `p` `w` `n` `f` | pretty / wrap / line numbers / follow |
| `/` `esc` | filter / clear filter |
| `F` | cycle format |
| `?` `q` | help / quit |

The mouse wheel scrolls.

## Memory

Rows are cached up to `--mem` (default `256MB`, for example `--mem 64MB` or `--mem 2GB`). The oldest rows are evicted first. The budget covers each row's text plus the numeric values derived from it. The bounded counters and the Go runtime add some overhead on top: about 100 MB resident at `--mem 32MB`.

| Kept as counters (whole stream) | Computed from cached rows (window) |
|---|---|
| lines, bytes, rate, records, parse % | percentiles (p50…p99) |
| per key: count, presence, types, min/max/mean/std | histograms |
| distinct and top values, levels, patterns | graph series |
| | input view, filter matches shown |

Rows are never dropped before they're analysed. If the cache is full of rows the analyzer hasn't reached yet, glimt briefly stops reading so the producer waits.

When rows are evicted, the stats bar shows `mem used/cap window N rows`, and Fields notes that percentiles and the histogram cover the cache.

A filter or format change replays only what's cached. After a replay, the counters cover the window, and the stats bar shows `counters since line N`.

## Design lock

The UI is pinned by golden snapshot tests (see `DESIGN.md`). `go test ./...` fails on any visual change. To accept an intended change, run `make update` (or `go test ./test -update`) and review the diff.

## Non-interactive use

```sh
cmd | glimt -s level=error          # text summary after EOF
cmd | glimt --render 120x40 --tab fields   # one TUI frame to stdout (ANSI)
```

If no terminal is available at all, glimt falls back to `-s`.

## Design

There are three ideas, and each package owns one of them:

1. **Everything is lines → records** (`format/`). A format has three parts: a *detector* that scores a sample, a *parser* that turns a line into 0..n records (each record is an ordered list of typed fields), and a *pretty printer* for display. nginx, CSV and plain text are all the same shape. Built-in regex formats are just specs, the same kind of thing you can write yourself.
2. **Analytics are a function of (cached rows, format, filter)** (`analysis/`). Rows live in a cache with a memory cap. *Counters* accumulate as rows are analysed and survive eviction. *Window stats* are computed from the rows still cached. Changing the format or the filter replays the cache through a fresh parser. Everything is generic over records, so a new format gets all the views for free.
3. **Views are derived from the data** (`ui/`). Each frame is a pure render of (result, view state, size). A tab exists only if the result supports it. `--render` uses this same function.

`term/` is a small terminal layer with no dependencies: raw mode, key and mouse decoding, and ANSI-aware width, slice and wrap. glimt uses only the Go standard library.

Limits:

- Distinct values are tracked up to 10k per key, and keys up to 2k.
- Producers that block-buffer when piped (some `grep` builds, Python) need `--line-buffered`, `stdbuf -oL` or `PYTHONUNBUFFERED=1`.

## Build

```sh
go mod tidy          # first time: fetches gopkg.in/yaml.v3, glimt's only dependency
go build -o glimt .
go test ./...
```

`make build`, `make test`, `make race`, `make vet`, `make update` and `make clean` wrap these. All tests live in `test/`.

`-tags glimt_nodeps` builds without the YAML library, for environments that can't download modules. With that tag, config files can't be read.
