# glimt: handoff notes for Claude

Read this first. It records what glimt is, the decisions behind it and why,
the rules that keep it coherent, and what is still open. `README.md` is the
user-facing spec and `DESIGN.md` defines the locked UI. When this file and
the code disagree, the code wins: fix this file.

## What glimt is

A terminal tool that you pipe any program's output into. It detects the
format, parses the stream as it arrives, and shows live structure in a TUI.

- A **stats bar** is always on top.
- Below it are tabs that appear only when the data supports them: **Input**
  (pretty-printed when the format is known), **Fields** (per-key stats),
  **Graph** (numeric fields, lines/s) and **Patterns** (message templates).
- The arguments are a filter, like a grep pattern: `kubectl logs -f api | g level=error`.
- When stdout isn't a terminal, glimt streams the matching lines instead,
  grep-style.

Owner: Oliver (repo `github.com/Olian04/glimt`, MIT). Users are expected to
`alias g=glimt` (zsh: `alias g='noglob glimt'`).

## How Oliver works, and what he expects

- **Conceptual integrity over feature-piling.** The system should come from
  a few clean ideas. Before adding anything, ask whether it is a special case
  of an existing idea. A second way to do the same thing is a defect.
- **Be direct, and push back.** Say plainly when something is wrong or
  risky. Don't flatter. Surface trade-offs and let him choose.
- **Ask about real forks.** When a decision is genuinely his (naming,
  semantics, dependencies, defaults), ask a short question with a
  recommended option. Don't guess silently.
- **Flag every unrequested addition.** He accepted pipe mode, but only
  because it was called out. Never slip extra behaviour in.
- **Consistency across surfaces matters a lot.** The CLI and the TUI must
  behave identically: "the shell is an implementation detail, not grounds
  for different behaviour".
- **Commits:** the history uses prefixes like `ops:`, `doc:`. Don't commit
  or push unless asked.

## Architecture: three ideas, one package each

| Package | Idea |
|---|---|
| `format/` | **Everything is lines → records.** A *Format* is a detector (scores a sample), a parser (line → 0..n records of ordered, typed fields) and a pretty printer. Built-in regex formats are just `RegexSpec`s, the same thing users write in config. |
| `analysis/` | **Analytics are a function of (cached rows, format, filter).** `Store` is a memory-capped row cache. *Counters* accumulate as rows are analysed and survive eviction. *Window stats* are computed from cached rows. Changing the format or filter replays the cache through a fresh parser. |
| `ui/` | **Views are derived from the data.** A frame is a pure render of (result, view state, size). A tab exists only if the data supports it. `--render` and the golden tests use this same function. |
| `term/` | A dependency-free terminal layer: raw mode (`/dev/tty`, so stdin can be a pipe), keys, SGR mouse wheel, ANSI-aware width/slice/wrap, and diffed frame drawing. |
| `cli/` | The command line: `ParseArgs` → `Options`, and `Filter`, the one place the arguments become an `analysis.Filter`. |
| `main.go` | Pipe mode and wiring. |

### Key types and flow

1. `main` reads stdin and calls `Store.Append`. Lines are cleaned: ANSI is
   stripped, tabs expanded, UTF-8 made valid.
2. The `Analyzer.Run` goroutine calls `step()` in batches of 4000 lines.
3. `detect()` re-scores the sample at 1, 2, 4 … 200 lines and at EOF. If the
   format changes, it replays from `Store.First()`.
4. Each line goes through `Parser.Parse` to produce `[]Record`, then
   `Filter.Match`, then `accumulate`:
   - `FieldStat` counters
   - window points (charged to the row's memory cost)
   - levels
   - patterns
5. The UI reads everything under `Analyzer.Read(fn)`, holding the analyzer
   lock while it renders.

### Data model: counters vs window

| Counters (every record since the last replay) | Window (rows still cached) |
|---|---|
| lines, bytes, rate, records, parse %; per key: count, presence, types, min/max/mean/std; distinct values (≤10k per key, ≤2k keys) and top values; levels; patterns (≤5k) | percentiles, histogram, graph series, input view, filter matches shown |

- **Never drop unanalysed rows.** The store pins rows at or after the
  analyzer cursor. When the cache is full of unanalysed rows, `Append` blocks,
  which is backpressure on the producer. `Store.Unpin()` on exit.
- **A replay can only cover the cache.** Afterwards `Result.From` is greater
  than 0, and the stats bar shows `counters since line N`. When rows have
  been evicted it shows `mem used/cap window N rows`.
- **Line numbers are absolute** and never reused. The input view stores its
  position as a line number (`App.top`), so eviction can't make it jump.
- **The cap is approximate.** It covers line text, 48 B of row overhead and
  16 B per numeric point. Bounded counters and the Go runtime come on top:
  about 105 MB resident at `--mem 32MB`. `debug.SetMemoryLimit` is set from
  the cap.

## Locked behaviour: don't change without asking Oliver

### UI design

- `DESIGN.md` and 84 golden frames in `test/testdata/golden/` pin the UI:
  14 scenarios × every available tab × 120×32 and 72×20, colours included.
- `go test ./...` fails on any visual change. Accept an intended change with
  `make update` (`go test ./test -update`), then review
  `git diff test/testdata/golden`.
  Failures write `*.ansi.got` (git-ignored).
- **New UI behaviour needs a new golden scenario** in `test/ui_golden_test.go`.
- The palette lives only in `format/style.go`.
- Determinism: the golden test uses a fake clock and drains synchronously
  after every line. Keep anything time- or goroutine-dependent out of
  `Frame()`.

### CLI

- **Input is only stdin.** Arguments are never files: `g < file`, never
  `g file`.
- **No stdin** (stdin is a terminal): print the help to stderr and exit 2.
  `--help`, `--version` and `--list` still work without input.
- **The arguments are the filter.** They are joined with spaces and parsed
  exactly like the TUI `/` prompt (`cli.Filter`; test
  `TestCLIFilterIsPromptSyntax`). Never special-case shell arguments.
- **Flags** are `-x` (one letter: `-f -r -s -h`) or `--name[=value]`. Any
  other single-dash argument is a filter term (`-healthcheck`,
  `-level=info`). Unknown `--flags` are errors. `-v` is rejected with a hint,
  because it would have meant invert in grep. `--` ends flags.
- **Pipe mode:** when stdout isn't a terminal, matching lines go to stdout,
  once each, in order, even across replays (`Analyzer.SetSink`, high-water
  mark). Exit 0 if anything matched, 1 if nothing did. Output is flushed
  whenever processing catches up.

### Filter language

The full grammar is in README "Filter syntax"; the implementation is
`analysis/filter.go`.

- **Terms are ANDed.** `-` negates any term.
- **`key!=v` is exactly `-key=v`.** It is rewritten at parse time; keep it
  that way.
- **`+term` is rejected with a hint.** A bare term already includes.
- **`=` matching:**
  - Values compare case-insensitively, and also numerically, through
    `format.Infer`: `dur=1s` matches `1000ms`.
  - Globs: `*` is any run of characters including `/`, `?` is one, and the
    whole value must match.
  - `key=*` means the key is present.
- **Multi-valued keys** (arrays): a term matches if any value matches.
- **A missing key** fails every field atom, so negated field terms keep
  records that don't have the key.
- **Keys are case-sensitive.** Word and regex terms are case-insensitive and
  run on the record's raw text.

### Formats

- **No command-specific parsers.** Ping support was deliberately removed.
  Plain `text` lines turn any `key=value` tokens into fields, which is how
  ping still gets graphs. Prefer generic mechanisms.
- **Built-in formats:**
  - `json` (NDJSON)
  - `json-doc` (multi-line JSON)
  - `access-log`, `syslog`, `leveled` (all regex specs)
  - `logfmt`
  - `csv`/`tsv`
  - `table` (kubectl/ps style)
  - `text` (the fallback below 0.6)
  Ties go to the earlier detector; user formats come first.
- **Durations** become numbers in milliseconds (`Value.Unit = "ms"`).
- **Pretty print** is on for known formats and off for `text`. `p` toggles
  it, and the user's choice then sticks.

### Config

- `~/.config/glimt/config.yaml`, or `--config PATH`. Its only job is
  `formats:` (name, regex, graph). It is parsed strictly
  (`KnownFields`), with errors for unknown keys, missing name or regex,
  duplicates, and regexes without named groups.
- **One dependency:** `gopkg.in/yaml.v3`, chosen by Oliver over a
  hand-written parser. Otherwise, standard library only. Ask before adding
  any other dependency.

## Build and test

```sh
go mod tidy                 # first time; fetches yaml.v3
go test ./...               # unit + golden snapshots
go test -race ./...
go build -o glimt .
go test ./test -update      # only for an intended visual change, then review the diff
```

The `Makefile` wraps these: `make build | test | race | vet | update | clean`.

Useful while developing:

```sh
./glimt -s level=error < test/testdata/app.logfmt                # text summary
./glimt --render 120x32 --tab fields < test/testdata/app.ndjson  # one ANSI frame
```

- **All tests live in `test/`**, one black-box package (`package test`), with
  all data in `test/testdata/` (one sample per format, plus `golden/`). Don't
  put a `_test.go` file back in a package dir. If a test needs something
  unexported, export the smallest hook (that is why `term.Decode`,
  `ui.App.Handle`, `ui.App.TabNames` and the `cli/` package exist) or ask.
  Tests run with `test/` as the working directory, so data paths are
  `testdata/...`.
- To drive the real TUI in a pty, use Python `pty` plus `pyte` to capture
  screens. That was how interactive behaviour was verified before.

### Environment caveat (temporary)

The first implementation was written in a sandbox that couldn't download Go
modules. To deal with that:

- `format/config_stub.go` (build tag `glimt_nodeps`) replaces the YAML
  decoder so everything else could compile and be tested.
- `format/config_yaml.go` and `test/config_test.go` were type-checked
  against yaml.v3's signatures but **had never been run** at handoff.

What to do:

1. Run `go test ./...` once to confirm the config tests pass.
2. Delete `config_stub.go` and the `//go:build !glimt_nodeps` lines.
3. Remove the `glimt_nodeps` paragraph from README "Build".

The terminal layer was tested in a Linux pty, and only cross-compiled for
macOS. Check it on a real macOS terminal: Terminal.app and iTerm, resize,
mouse wheel, and exit restoring the screen.

## Release

Manual: Actions → **Release** → Run workflow, with `X.Y.Z` (or `X.Y.Z-N` with
"prerelease" ticked, which is the default). `.github/workflows/release.yml`
validates the version, runs `go test ./...`, tags, then runs GoReleaser
(`.goreleaser.yaml`) to publish binaries and `checksums.txt`.

- **Tags are always `vX.Y.Z` or `vX.Y.Z-N`.** The Go toolchain only resolves
  `go install …@version` from semver tags with the `v`, so the workflow adds it
  when missing and rejects `X.Y`. (bifrost, which this was adapted from, tags
  without the `v`; that would break `go install` here.) `X.Y.Z-N` is the Nth
  prerelease *before* `X.Y.Z`, and is refused once `X.Y.Z` is tagged.
- **Never move or reuse a published tag.** proxy.golang.org caches versions for
  good. If GoReleaser fails after the tag was pushed, delete the tag
  (`git push --delete origin vX.Y.Z`) and release again.
- **Targets are linux and darwin × amd64 and arm64.** No Windows: `term/` has
  no implementation for it (`syscall.Termios`). Assets are loose binaries named
  `glimt-<os>-<arch>` with no version in the name, so
  `releases/latest/download/glimt-<os>-<arch>` is a stable URL.
- **The version** is `main.version`, set with `-X main.version={{ .Tag }}`
  (keeps the `v`). `go install …@vX.Y.Z`, `go tool glimt` and a plain source
  build take it from the build info instead (`buildVersion` in `main.go`).
- `make release-check` validates `.goreleaser.yaml`; `make snapshot` builds all
  four binaries into `dist/` without publishing (both need `goreleaser`).
- Left out of the bifrost template on purpose: the Docker/GHCR image and the
  Syft SBOMs. Add them only if asked.
- The install instructions in the README only work once the repo is public.
- **The workflow has never run on GitHub.** The config and the scripts were
  checked locally (`goreleaser check`, a snapshot build, the version script
  against a local remote, `go install` / `go get -tool` against a local module
  proxy). The first real run is the test, so start with a prerelease.

## Known limitations and open questions

Raise these with Oliver before changing behaviour.

- **json-doc in pipe mode:** a matching item inside a top-level object (for
  example `kubectl get -o json`) prints the whole document. Today's
  workaround is `jq -c '.items[]' | g …`. Fixing it properly means emitting
  records rather than lines.
- **Detection may flip early.** Pipe mode can emit lines selected under an
  early detected format before detection settles (it re-detects up to 200
  lines). The high-water mark prevents duplicates, not mis-selection.
- **Units after a space are lost** in `text` key=value pairs: `time=12.3 ms`
  becomes 12.3 with no unit.
- **The Graph x-axis is line number**, not event time. Timestamp fields are
  not yet used for time-based axes or rates.
- **No Ctrl-Z suspend.** Raw mode disables ISIG, and Ctrl-C quits.
- **Pattern templates** are truncated to 300 bytes; there are no
  user-defined patterns.
- **Caps** are hard-coded: distinct values, keys, patterns, and the 1h
  per-second history.
- **`--mem` is a soft cap**, as described above.
- **No CI on push or PR.** The only workflow is the manual Release one, which
  runs `go test ./...` before it tags (see "Release").
- **The help overlay** (`ui/patterns.go: helpText`) doesn't mention the CLI
  argument syntax. Changing it changes the golden files, so it needs an
  explicit decision.

## Decision log (most recent last)

1. Go, standard library only, with a hand-written TUI layer. Bubble Tea
   couldn't be fetched at the time; zero dependencies became a feature.
2. Any regex can be a format: built-ins are specs, and users add their own.
3. The design was locked with golden snapshot tests and `DESIGN.md`.
4. Ping-specific parsing was dropped in favour of generic key=value
   extraction in `text`.
5. Rows live in a memory-capped cache with counters vs window semantics,
   backpressure instead of loss, and `--mem` (default 256MB).
6. Renamed lens → glimt. "lens" and "logviz" collided with existing tools
   and search results. Alias `g`.
7. The arguments became the filter. Pipe mode was added as a flagged
   addition and accepted.
8. `+word` is rejected with a hint, and `!=` is defined as `-key=v`.
9. Config moved from INI to YAML via yaml.v3, still only for formats.
10. A single filter syntax for CLI and TUI: arguments are joined with
    spaces, and shell quoting gets no special treatment.
11. Input is stdin only (no file arguments); with no input, print help and
    exit 2.
12. All tests and test data moved to `test/` (black-box). To make that
    possible, argument parsing moved from `main` into `cli/`, and `term.Decode`,
    `ui.App.Handle` and `ui.App.TabNames` were exported. Added a `Makefile`.
13. Releases: a manual GitHub workflow plus GoReleaser, adapted from
    lolocompany/bifrost. Tags are `vX.Y.Z[-N]` so `go install` and
    `go get -tool` work; the version comes from the tag (ldflags) or the build
    info. Linux and macOS only; no Docker image or SBOMs.
