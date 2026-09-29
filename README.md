# globcompat

Glob matching for Go with picomatch/micromatch semantics — the matcher behind
lint-staged, chokidar, fast-glob, Jest, Vite and most of the rest of the
JavaScript tooling ecosystem.

```go
m, err := globcompat.Compile("src/**/*.{ts,tsx}", globcompat.Options{})
if err != nil {
	log.Fatal(err)
}
m.Match("src/components/Button.tsx") // true
m.Match("src/components/Button.css") // false
```

## Why

A Go tool that reads a glob pattern from a `.eslintrc`, `lint-staged` config,
`tsconfig` `include` list, or any other file written for a Node tool needs to
match it the way picomatch does — not the way `path/filepath.Match` or a
generic Go glob library does. The two gaps that matter most:

- **Extglobs.** `!(*.test).ts`, `@(foo|bar)`, `+(a|b)` — used constantly in
  ignore lists and include globs — are not part of `path/filepath.Match`, and
  the maintained Go glob libraries checked below don't implement them either.
- **Brace ranges.** `{1..10}`, `{01..10..2}` (zero-padded, stepped numeric
  ranges) are a `braces`/micromatch feature with no equivalent in
  `path/filepath.Match`.

Incumbents checked: `path/filepath.Match` (stdlib), `github.com/bmatcuk/doublestar/v4`,
`github.com/gobwas/glob`, and `mvdan.cc/sh/v3`'s pattern package (shell glob
semantics, a different dialect entirely). None of the three third-party
libraries support extglobs or numeric brace ranges.

**Measured:** `go test -bench=. -benchmem ./bench/...` compares raw match
speed (see Reference); the differential corpus below compares match
*correctness* against picomatch/micromatch, run with:
`go test -tags differential -run TestDifferential -v ./...`

A pre-build research pass reported doublestar scoring 17/25 and gobwas/glob
14/25 against micromatch on a small hand-built corpus, with both libraries
missing extglobs and numeric brace ranges (open issues: doublestar#87,
gobwas/glob#3). This package re-measured that on a larger, differently built
corpus of 37,134 (pattern, path, options) cases checked against a real
picomatch/micromatch install (methodology: `go test -tags differential -run
TestBreakdown -v ./...`, which also drives the per-feature table below):

```
=== Overall (n=37134) ===
globcompat     n=37134  agree=35982  err=568  pct=96.90%
doublestar     n=37134  agree=32407  err=568  pct=87.27%
gobwas/glob    n=37134  agree=32171  err=568  pct=86.63%
```

Both incumbents land well under a 97% compatibility bar, confirming the
original finding at larger scale — extglobs and brace ranges are exactly
where they fall down. `err` above is the same 568 cases across all three
matchers: patterns from picomatch's own error-handling tests (e.g.
`+(a|b[)*`, an unterminated `[` inside an extglob group) that all three
matchers reject as parse errors rather than accept.

On the corpus built directly from micromatch's own test fixtures (the
"seed corpus" — see Compatibility below), agreement is 99.81% (18,526/18,561
against a real picomatch install). See Limitations for exactly what makes up
the remaining gap on both corpora, with counts and examples.

## Installation

```sh
go get github.com/__GH_OWNER__/globcompat
```

Requires Go 1.21+. No runtime dependencies — stdlib only.

## Usage

### One-off match

```go
ok, err := globcompat.Match("**/*.md", "docs/guide.md", globcompat.Options{})
```

### Reused matcher

```go
m, err := globcompat.Compile("!(*.min).js", globcompat.Options{})
// ...
if m.Match(path) { ... }
```

### A list with negation (micromatch's `isMatch`)

```go
ok, err := globcompat.IsMatch("src/index.test.ts", []string{
	"src/**/*.ts",
	"!**/*.test.ts",
}, globcompat.Options{})
// ok == false: excluded by the second, negated pattern
```

### Walking only the relevant directory

```go
s := globcompat.Scan("src/**/*.ts") // s.Base == "src/", s.Glob == "**/*.ts"
filepath.WalkDir(s.Base, func(path string, d fs.DirEntry, err error) error {
	if !d.IsDir() {
		if ok, _ := globcompat.Match(s.Glob, strings.TrimPrefix(path, s.Base), globcompat.Options{}); ok {
			// ...
		}
	}
	return nil
})
```

### Brace expansion on its own

```go
globcompat.Braces("a{1..3}b")       // ["a1b", "a2b", "a3b"]
globcompat.Braces("v{01..03}")      // ["v01", "v02", "v03"]
globcompat.Braces("{a,b{c,d}}")     // ["a", "bc", "bd"]
```

## Reference

### Functions

| Function | Signature | Description |
| --- | --- | --- |
| `Compile` | `(pattern string, opts Options) (*Matcher, error)` | Parses pattern once for reuse. |
| `Match` | `(pattern, path string, opts Options) (bool, error)` | Compiles and matches in one call. |
| `MatchAny` | `(patterns []string, path string, opts Options) (bool, error)` | True if path matches any pattern. |
| `IsMatch` | `(path string, patterns []string, opts Options) (bool, error)` | micromatch's list semantics: matches a positive pattern and no negated (`!`) one. |
| `Braces` | `(pattern string) ([]string, error)` | Brace-set/range expansion alone. |
| `Scan` | `(pattern string) ScanResult` | Base/glob split for directory walking. |

### `Matcher`

`(*Matcher).Match(path string) bool`, `(*Matcher).Negated() bool`,
`(*Matcher).String() string`.

### `Options`

| Name | Type | Default | Description |
| --- | --- | --- | --- |
| `Dot` | bool | false | Match dotfiles/dot-directories with wildcards. |
| `NoCase` | bool | false | Case-insensitive matching. |
| `NoBrace` | bool | false | Disable `{...}` expansion. |
| `NoExtglob` | bool | false | Disable `@() +() *() ?() !()`. |
| `NoGlobstar` | bool | false | Treat `**` as `*`. |
| `Basename` | bool | false | Slash-free patterns match only the last path segment (picomatch's `matchBase`). |
| `Contains` | bool | false | Match anywhere in the path, not just the whole string. |
| `Posix` | bool | false | No-op: `[[:alpha:]]`-style classes are always honored (Go's regexp supports them natively). Kept for API parity with picomatch. |
| `Windows` | bool | false | Treat `\` in the candidate path as `/`. Does not add drive-letter or UNC handling. |
| `NoNegate` | bool | false | Disable leading-`!` negation. |

### `ScanResult`

`Base string`, `Glob string`, `IsGlob bool`, `Negated bool`.

## How it works

A pattern is brace-expanded first (textual, before parsing), then each
expansion is parsed into path segments and, within each segment, a small AST
of literals, `*`, `?`, `[...]`, and extglob groups. Segments compile to an
RE2 (Go `regexp`) source string. `@()`, `+()`, `*()`, and `?()` extglobs are
ordinary RE2 alternation/repetition; `**` between two literal segments
compiles to a lazy alternation (`/(?:<segments>/)??`) so it can also match
zero directories without a lookahead.

Dot-hiding (picomatch hides dotfiles from `*`/`?`/`**` unless `dot` is set or
the pattern segment explicitly starts with `.`) and the "a trailing `*` needs
something to match, unless something can already follow it" rule picomatch
applies to the pattern's very last segment both need a check RE2 can't
express as a lookahead. Both are instead implemented by wrapping the
relevant piece of the segment in a named capture group and checking the
*matched text* after a successful RE2 match (see `matchWithDotGuard` in
globcompat.go) — exact, not an approximation, because a segment's span is
fixed by the literal `/` around it regardless of which node inside it
consumes which character, so there's no alternative parse to miss by
checking only what RE2 actually captured.

`!()` (negated extglob) has no RE2 equivalent at all — compiling it falls
back to a small backtracking matcher (`bt.go`) that walks the AST directly,
memoized over (AST node index, rune offset) so one match attempt is bounded
by `O(nodes × len(input))` states; `Compile` also caps pattern length at
4096 bytes to keep the extglob alternative search bounded.

## Limitations

What picomatch supports that this package does not, or only partially,
verified against a real picomatch install (`go test -tags differential -run
TestBreakdown -v ./...`, 900 cases per row unless noted):

| Gap | Agreement | Example |
| --- | --- | --- |
| Extglob group containing a literal, unescaped `/` (e.g. `!(b/a)`) is parsed as part of one path segment instead of crossing a real segment boundary | 91.0% on the `seed-extglob` corpus row (11,573 cases; this single pattern shape accounts for essentially all of the gap) | `isMatch('a/b', '!(b/a)')` is `true` in picomatch; this package returns `false` |
| A nullable extglob quantifier (`*(x)`, `?(x)`, `!(x)`) that matches zero characters is not required to be non-empty the way picomatch requires the *whole match* to be non-empty | `*(foo\|bar)`: 84.7%, `?(foo\|bar)`: 82.1%, `!(foo\|bar)`: 84.2% | `isMatch('', '*(ab)')` is `false` in picomatch; this package returns `true` |
| `noextglob` does not disable the `!(...)` form the way it disables `@()`/`+()`/`*()`/`?()` — verified as picomatch's own behaviour, not a guess: picomatch still compiles `!(foo)` to a negation regex with `noextglob: true` | `opt-noextglob`: 82.7% | `pm.isMatch('.hidden', '!(foo)', {noextglob:true})` is `true` in picomatch; this package treats `!(foo)` as four literal characters once `NoExtglob` is set, so it returns `false` |
| `noglobstar` on a pattern like `**/*` doesn't fully replicate picomatch's fallback behaviour for paths without a matching number of segments | `opt-noglobstar`: 96.1% | `isMatch('a', '**/*', {noglobstar:true})` is `true` in picomatch (a single segment still matches); this package requires two segments once `**` stops being special |
| `basename`/`contains` combined with a dotfile deep in the path | `opt-basename`: 97.3%, `opt-contains`: 96.6% | `isMatch('.git/test.js', '**/*', {contains:true})` is `true` in picomatch; this package's `Contains` doesn't hide dotfiles the same way inside a substring search |
| Patterns with an unterminated `[` inside an extglob group (e.g. `+(a|b[)*`) are rejected as a parse error instead of picomatch's specific bracket-recovery heuristic | 568 cases across the whole 37,134-case corpus, identical set for every matcher tested (see Why) | These are drawn from picomatch's own error-handling tests, not real-world globs |
| `nonegate`/double-negation edge cases (`!!(foo)`) | not separately measured | Undefined behaviour here; picomatch's own handling of this is also not documented |
| `windows` only rewrites `\` to `/` in the candidate path | not separately measured | No drive-letter or UNC handling |

Not a bug, but worth stating plainly: plain `picomatch.isMatch` (and
`micromatch.isMatch`, which delegates to it) does **not** expand numeric
brace ranges like `{2..11}` by default — that needs an `expandRange`
callback the caller supplies, which neither library's `isMatch` does on its
own (confirmed by reading `pm.makeRe('v{2..11}').source`, which comes out as
a garbled character class, not a range). This package's own `Braces()`
*does* expand these correctly — verified directly against the real `braces`
npm package, which is what micromatch's CLI-facing brace expansion actually
uses — so a caller that pre-expands braces with `Braces()` before matching
(the way `fast-glob` and `lint-staged` configs assume braces work) gets the
range expansion right. The `brace-range-num`/`brace-range-alpha`/
`brace-step-pad` rows in the differential breakdown measure agreement
against bare `picomatch.isMatch`, so they show this same gap in reverse —
low agreement there reflects the oracle not expanding the range, not this
package computing the wrong range.

Only the options listed in the `Options` table above are implemented;
picomatch's `bash`, `capture`, `expandRange`, `maxLength`,
`maxExtglobRecursion`, `nobracket`, `onIgnore`/`onMatch`, `prepend`, and
`regex` options have no equivalent here.

## Compatibility

Actually run: Go 1.27.1 on darwin/arm64 (`go build ./...`, `go vet ./...`,
`go test -race -count=1 ./...`, `gofmt -l .`), Node v24.20.0 with
picomatch 4.0.7/micromatch 4.0.8 for the differential test. CI
(`.github/workflows/test.yml`) runs the same build/vet/test/fmt steps on
`ubuntu-latest` with the Go version from `go.mod` — that configuration has
not been run locally, only in CI.

### Differential tests against real picomatch

Both fetch micromatch's own `test/_patterns.js` and `test/_fixtures.js` (MIT
licensed, see `LICENSE-picomatch`) from GitHub, pinned to commit `8bd704e`
(tag 4.0.8) and checked against a fixed sha256, and install `picomatch` via
`npm` into a gitignored cache directory. Both are excluded from the default
`go test ./...` (needs network and Node) and skip loudly, with an actionable
message, if `node`/`npm` or network access are unavailable.

`go test -tags differential -run TestDifferential -v ./...` compares this
package's `Match` against `picomatch.isMatch` (default options) over the
fixtures' cross product plus 5,000 random combinations, and fails if
agreement drops below 99.5%. Last real run:

```
oracle run over 18561 cases took 99.54ms
agreement with picomatch: 18526/18561 (99.81%)
```

`go test -tags differential -run TestBreakdown -v ./...` is the
reproducible source of every number in Why and Limitations above: it adds
~20,700 more cases tagged by feature (globstar, each brace form, each
extglob kind, POSIX classes, negation, plain and compound dot-hiding, every
`Option`, and Unicode paths) and logs exact agreement plus up to 3 examples
per tag. It doesn't gate on a threshold — its job is the report, not a
pass/fail signal.

### Fuzzing

`go test -run=^$ -fuzz=FuzzMatch -fuzztime=30s ./...` exercises
`Compile`+`Match` (including the backtracking path) for panics across
random pattern/path/option combinations, seeded with extglob, brace,
posix-class and Unicode cases. Last real run: 4,939,138 executions across 15
workers in ~32s, no crashers found.

### Benchmark vs. doublestar

`bench/` is a separate Go module (its own `go.mod`) so the comparison
dependency on `doublestar` never becomes a dependency of this package,
which stays stdlib-only. Run with `cd bench && go test -bench=. -benchmem ./...`.
Measured on Apple M5 Pro (darwin/arm64, Go 1.27.1):

```
BenchmarkGlobcompat/literal-15    17289591    70.34 ns/op     48 B/op   1 allocs/op
BenchmarkGlobcompat/star-15        8692964   138.7  ns/op     48 B/op   1 allocs/op
BenchmarkGlobcompat/globstar-15    1784806   683.3  ns/op     48 B/op   1 allocs/op
BenchmarkGlobcompat/brace-15        916636  1275    ns/op    144 B/op   1 allocs/op
BenchmarkGlobcompat/extglob-15      299498  3894    ns/op   7584 B/op  57 allocs/op
BenchmarkGlobcompat/nomatch-15     1479843   813.2  ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/literal-15    34045006    34.76 ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/star-15       31681773    39.13 ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/globstar-15    9432873   127.0  ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/brace-15       6970954   171.9  ns/op     32 B/op   2 allocs/op
BenchmarkDoublestar/extglob-15    30067494    41.52 ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/nomatch-15     8689520   134.5  ns/op      0 B/op   0 allocs/op
```

doublestar is faster on every case shown — it has a simpler regex compiler
and, for the `extglob` case, isn't actually evaluating an extglob at all
(it treats `!(*.test)` as literal characters, which is exactly the
compatibility gap this package exists to close). globcompat's non-extglob
cases now allocate once per match: the dot-hiding and terminal-star guards
(see How it works) need `FindStringSubmatchIndex`, which allocates a
result slice, where a plain `MatchString` would not. The `extglob` row is
the one case here that falls onto globcompat's backtracking matcher rather
than RE2, which is where its much larger cost and allocation count come
from.

## Development

```sh
go build ./...
go vet ./...
go test -race -count=1 ./...
gofmt -l .                                              # must print nothing
go test -tags differential -run TestDifferential -v ./...  # needs Node + network
go test -tags differential -run TestBreakdown -v ./...     # per-feature report; needs Node + network
cd bench && go test -bench=. -benchmem ./...
```

## License

MIT — see [LICENSE](LICENSE). This package's matching semantics are modeled
closely on picomatch/micromatch and its differential/seed corpus draws on
micromatch's own test fixtures (both MIT); see
[LICENSE-picomatch](LICENSE-picomatch) for their notices.
