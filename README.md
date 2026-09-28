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
picomatch/micromatch install (see `bench/differential` methodology below):

```
=== Overall (n=37134) ===
globcompat     n=37134  agree=35698  err=568  pct=96.13%
doublestar     n=37134  agree=32407  err=568  pct=87.27%
gobwas/glob    n=37134  agree=32171  err=568  pct=86.63%
```

Both incumbents land well under a 97% compatibility bar, confirming the
original finding at larger scale — extglobs and brace ranges are exactly
where they fall down. `err` above is the same 568 cases across all three
matchers: patterns from picomatch's own error-handling tests (e.g.
`+(a|b[)*`, an unterminated `[` inside an extglob group) that all three
matchers reject as parse errors rather than accept.

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
RE2 (Go `regexp`) source string: `*`/`?`/`**` get the non-lookahead
dot-hiding trick (`(?:[^/.][^/]*)?` instead of a negative lookahead), and
`**` between two literal segments compiles to an alternation
(`/(?:|<segments>/)`) so it can also match zero directories without
requiring a lookahead. `@()`, `+()`, `*()`, and `?()` extglobs are ordinary
RE2 alternation/repetition. `!()` (negated extglob) has no RE2 equivalent —
compiling it falls back to a small backtracking matcher (`bt.go`) that walks
the AST directly, memoized over (AST node index, rune offset) so one match
attempt is bounded by `O(nodes × len(input))` states; `Compile` also caps
pattern length at 4096 bytes to keep the extglob alternative search bounded.

## Limitations

What picomatch supports that this package deliberately does not, or only
partially:

- Dot-hiding is applied to the first AST node of a segment. A pattern where
  a zero-width-capable wildcard is followed by another wildcard at the very
  start of a segment (e.g. `*?.js`, a pattern that is unusual in practice)
  may not hide a leading dot on the second wildcard as strictly as
  picomatch.
- An extglob group containing a literal, unescaped `/` is parsed as part of
  the same segment rather than crossing a real path-segment boundary; this
  only matters for the unusual case of a `/` inside `@()`/`!()`/etc.
- Patterns with an unterminated `[` inside an extglob group (e.g.
  `+(a|b[)*`) are rejected as a parse error instead of picomatch's specific
  bracket-recovery heuristic. These are drawn from picomatch's own
  error-handling tests, not from real-world globs.
- `nonegate`/double-negation edge cases (`!!(foo)`) are not verified beyond
  what the corpus below happens to cover.
- `windows` only rewrites `\` to `/` in the candidate path; it does not
  special-case drive letters or UNC prefixes.
- Only the options listed in the `Options` table above are implemented;
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

### Differential test against real picomatch

`go test -tags differential -run TestDifferential -v ./...` fetches
micromatch's own `test/_patterns.js` and `test/_fixtures.js` (MIT licensed,
see `LICENSE-picomatch`) from GitHub, pinned to commit `8bd704e` (tag
4.0.8) and checked against a fixed sha256, installs `picomatch` via `npm`
into a gitignored cache directory, and compares this package's `Match`
against `picomatch.isMatch` over their cross product plus 5,000 random
combinations. It is excluded from the default `go test ./...` (needs network
and Node) and skips loudly, with an actionable message, if `node`/`npm` or
network access are unavailable. Last real run:

```
oracle run over 18561 cases took 126.249833ms
agreement with picomatch: 18264/18561 (98.40%)
```

### Fuzzing

`go test -run=^$ -fuzz=FuzzMatch -fuzztime=30s ./...` exercises
`Compile`+`Match` (including the backtracking path) for panics across
random pattern/path/option combinations, seeded with extglob, brace,
posix-class and Unicode cases. Last real run: 621,513 executions across 15
workers in ~32s, no crashers found.

### Benchmark vs. doublestar

`bench/` is a separate Go module (its own `go.mod`) so the comparison
dependency on `doublestar` never becomes a dependency of this package,
which stays stdlib-only. Run with `cd bench && go test -bench=. -benchmem ./...`.
Measured on Apple M5 Pro (darwin/arm64, Go 1.27.1):

```
BenchmarkGlobcompat/literal-15    55556090   22.08 ns/op      0 B/op   0 allocs/op
BenchmarkGlobcompat/star-15       11728418  104.3  ns/op      0 B/op   0 allocs/op
BenchmarkGlobcompat/globstar-15    2262379  530.5  ns/op      0 B/op   0 allocs/op
BenchmarkGlobcompat/brace-15        981370 1206    ns/op      0 B/op   0 allocs/op
BenchmarkGlobcompat/extglob-15      313550 3770    ns/op   7584 B/op  57 allocs/op
BenchmarkGlobcompat/nomatch-15     1439721  837.4  ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/literal-15    34421720   33.26 ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/star-15       30090678   38.57 ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/globstar-15   10314712  119.6  ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/brace-15       7223418  163.7  ns/op     32 B/op   2 allocs/op
BenchmarkDoublestar/extglob-15    30287764   39.51 ns/op      0 B/op   0 allocs/op
BenchmarkDoublestar/nomatch-15     9018114  127.9  ns/op      0 B/op   0 allocs/op
```

doublestar is faster on every case shown — it has a simpler regex compiler
and, for the `extglob` case, isn't actually evaluating an extglob at all
(it treats `!(*.test)` as literal characters, which is exactly the
compatibility gap this package exists to close). The `extglob` row is the
one case here that falls onto globcompat's backtracking matcher rather than
RE2, which is where its cost and allocations come from.

## Development

```sh
go build ./...
go vet ./...
go test -race -count=1 ./...
gofmt -l .                                              # must print nothing
go test -tags differential -run TestDifferential -v ./...  # needs Node + network
cd bench && go test -bench=. -benchmem ./...
```

## License

MIT — see [LICENSE](LICENSE). This package's matching semantics are modeled
closely on picomatch/micromatch and its differential/seed corpus draws on
micromatch's own test fixtures (both MIT); see
[LICENSE-picomatch](LICENSE-picomatch) for their notices.
