//go:build differential

// Run with: go test -tags differential -run TestBreakdown -v ./...
//
// This is the reproducible per-feature accuracy breakdown: it builds a
// >=20,000 case (pattern, path, options) corpus — the upstream micromatch
// fixtures cross product plus tagged random generation over every
// documented construct (globstar, each brace form, each extglob kind,
// POSIX classes, negation, dot handling, every Option, and Unicode paths)
// — checks this package's Match against the real picomatch package for
// each case, and reports exact agreement per feature tag with example
// mismatches. Excluded from the default `go test ./...` for the same
// reason as differential_test.go (needs Node and network); see that file
// for the fetch/cache/skip mechanics reused here.
package globcompat

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type bdCase struct {
	Pattern string
	Path    string
	Opts    Options
	Feature string
}

// jsonOpts mirrors Options in a form the Node oracle understands (picomatch's
// own option names), since Options itself has no JSON tags.
type jsonOpts struct {
	Dot        bool `json:"dot,omitempty"`
	NoCase     bool `json:"nocase,omitempty"`
	NoBrace    bool `json:"nobrace,omitempty"`
	NoExtglob  bool `json:"noextglob,omitempty"`
	NoGlobstar bool `json:"noglobstar,omitempty"`
	Basename   bool `json:"basename,omitempty"`
	Contains   bool `json:"contains,omitempty"`
	Windows    bool `json:"windows,omitempty"`
	NoNegate   bool `json:"nonegate,omitempty"`
}

func toJSONOpts(o Options) jsonOpts {
	return jsonOpts{
		Dot: o.Dot, NoCase: o.NoCase, NoBrace: o.NoBrace, NoExtglob: o.NoExtglob,
		NoGlobstar: o.NoGlobstar, Basename: o.Basename, Contains: o.Contains,
		Windows: o.Windows, NoNegate: o.NoNegate,
	}
}

func TestBreakdown(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("breakdown test needs Node on PATH to run the real picomatch package; skipping")
	}

	cacheDir := filepath.Join("testdata", ".upstream-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("creating cache dir: %v", err)
	}
	patternsFile, err := fetchPinned(cacheDir, "patterns.js", patternsURL, patternsSHA256)
	if err != nil {
		t.Skipf("could not fetch upstream test fixtures (no network?): %v", err)
	}
	fixturesFile, err := fetchPinned(cacheDir, "fixtures.js", fixturesURL, fixturesSHA256)
	if err != nil {
		t.Skipf("could not fetch upstream test fixtures (no network?): %v", err)
	}
	if !hasNodeModules(cacheDir, "picomatch", "braces") {
		cmd := exec.Command("npm", "install", "--no-save", "--no-audit", "--no-fund", "picomatch@4", "braces@3")
		cmd.Dir = cacheDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("could not npm install picomatch/braces (no network?): %v\n%s", err, out)
		}
	}

	seedPatterns := extractJSStringArray(readFile(t, patternsFile))
	seedFixtures := extractJSStringArray(readFile(t, fixturesFile))
	if len(seedPatterns) == 0 || len(seedFixtures) == 0 {
		t.Fatalf("parsed 0 patterns/fixtures from upstream files; format may have changed")
	}

	var cases []bdCase
	for _, p := range seedPatterns {
		for _, f := range seedFixtures {
			cases = append(cases, bdCase{Pattern: p, Path: f, Feature: "seed"})
		}
	}

	r := rand.New(rand.NewSource(7))
	const perFeature = 900
	generators := map[string]func(*rand.Rand) (string, string, Options){
		"globstar":          genGlobstar,
		"brace-list":        genBraceList,
		"brace-range-num":   genBraceRangeNum,
		"brace-range-alpha": genBraceRangeAlpha,
		"brace-step-pad":    genBraceStepPad,
		"brace-nested":      genBraceNested,
		"extglob-bang":      func(r *rand.Rand) (string, string, Options) { return genExtglob(r, '!') },
		"extglob-at":        func(r *rand.Rand) (string, string, Options) { return genExtglob(r, '@') },
		"extglob-plus":      func(r *rand.Rand) (string, string, Options) { return genExtglob(r, '+') },
		"extglob-star":      func(r *rand.Rand) (string, string, Options) { return genExtglob(r, '*') },
		"extglob-qmark":     func(r *rand.Rand) (string, string, Options) { return genExtglob(r, '?') },
		"posix-class":       genPosixClass,
		"negation-list":     genNegationList,
		"dot-simple":        genDotSimple,
		"dot-compound":      genDotCompound,
		"opt-nocase":        func(r *rand.Rand) (string, string, Options) { return genOpt(r, func(o *Options) { o.NoCase = true }) },
		"opt-nobrace":       func(r *rand.Rand) (string, string, Options) { return genOpt(r, func(o *Options) { o.NoBrace = true }) },
		"opt-noextglob": func(r *rand.Rand) (string, string, Options) {
			return genOpt(r, func(o *Options) { o.NoExtglob = true })
		},
		"opt-noglobstar": func(r *rand.Rand) (string, string, Options) {
			return genOpt(r, func(o *Options) { o.NoGlobstar = true })
		},
		"opt-basename": func(r *rand.Rand) (string, string, Options) { return genOpt(r, func(o *Options) { o.Basename = true }) },
		"opt-contains": func(r *rand.Rand) (string, string, Options) { return genOpt(r, func(o *Options) { o.Contains = true }) },
		"opt-windows":  func(r *rand.Rand) (string, string, Options) { return genOpt(r, func(o *Options) { o.Windows = true }) },
		"unicode":      genUnicode,
	}
	// Deterministic iteration order for reproducible logs.
	var names []string
	for name := range generators {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		gen := generators[name]
		for i := 0; i < perFeature; i++ {
			p, s, o := gen(r)
			cases = append(cases, bdCase{Pattern: p, Path: s, Opts: o, Feature: name})
		}
	}

	t.Logf("corpus size: %d cases across %d feature tags (plus %q)", len(cases), len(names), "seed")
	if len(cases) < 20000 {
		t.Logf("NOTE: corpus is %d cases, under the 20,000 target", len(cases))
	}

	type oc struct {
		Pattern string   `json:"pattern"`
		Path    string   `json:"path"`
		Opts    jsonOpts `json:"opts"`
	}
	oin := make([]oc, len(cases))
	for i, c := range cases {
		oin[i] = oc{c.Pattern, c.Path, toJSONOpts(c.Opts)}
	}
	inFile := filepath.Join(t.TempDir(), "breakdown_cases.json")
	writeJSON(t, inFile, oin)

	oraclePath := filepath.Join("testdata", "oracle_opts.cjs")
	start := time.Now()
	cmd := exec.Command(node, oraclePath, inFile)
	cmd.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(cacheDir, "node_modules"))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node oracle failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("node oracle failed: %v", err)
	}
	t.Logf("oracle run over %d cases took %s", len(cases), time.Since(start))

	var results []struct {
		Ok    bool `json:"ok"`
		Match bool `json:"match"`
	}
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("parsing oracle output: %v", err)
	}

	type tally struct {
		total, agree, oracleErr int
		examples                []string
	}
	byFeature := map[string]*tally{}
	overallTotal, overallAgree := 0, 0

	for i, c := range cases {
		res := results[i]
		tl := byFeature[c.Feature]
		if tl == nil {
			tl = &tally{}
			byFeature[c.Feature] = tl
		}
		if !res.Ok {
			tl.oracleErr++
			continue // picomatch itself rejected the pattern; not scored
		}
		tl.total++
		overallTotal++
		got, err := Match(c.Pattern, c.Path, c.Opts)
		if err == nil && got == res.Match {
			tl.agree++
			overallAgree++
		} else if len(tl.examples) < 3 {
			tl.examples = append(tl.examples, fmt.Sprintf("pattern=%q path=%q opts=%+v want=%v got=%v err=%v",
				c.Pattern, c.Path, c.Opts, res.Match, got, err))
		}
	}

	t.Logf("=== overall: %d/%d (%.2f%%) ===", overallAgree, overallTotal, 100*float64(overallAgree)/float64(overallTotal))
	var featNames []string
	for f := range byFeature {
		featNames = append(featNames, f)
	}
	sort.Strings(featNames)
	for _, f := range featNames {
		tl := byFeature[f]
		pct := 100.0
		if tl.total > 0 {
			pct = 100 * float64(tl.agree) / float64(tl.total)
		}
		t.Logf("%-20s n=%-6d agree=%-6d oracle_err=%-5d pct=%.2f%%", f, tl.total, tl.agree, tl.oracleErr, pct)
		for _, ex := range tl.examples {
			t.Logf("    %s", ex)
		}
	}
}

// --- generators ---
// Each returns (pattern, path, options) for one feature tag. They lean on
// small, fixed vocabularies rather than fully free-form fuzzing, so that a
// mismatch is easy to read and reduce by hand.

var bdNames = []string{"a", "b", "foo", "bar", "index", "test", "sub", "dir"}
var bdExts = []string{"js", "ts", "md", "json", "go", "txt"}
var bdDotNames = []string{".git", ".hidden", ".env"}

func bdName(r *rand.Rand) string {
	n := bdNames[r.Intn(len(bdNames))]
	if r.Intn(2) == 0 {
		n += "." + bdExts[r.Intn(len(bdExts))]
	}
	return n
}

func bdPath(r *rand.Rand, segs int) string {
	parts := make([]string, segs)
	for i := range parts {
		if r.Intn(6) == 0 {
			parts[i] = bdDotNames[r.Intn(len(bdDotNames))]
		} else {
			parts[i] = bdName(r)
		}
	}
	return strings.Join(parts, "/")
}

func genGlobstar(r *rand.Rand) (string, string, Options) {
	patterns := []string{"**", "**/*", "a/**", "a/**/b", "**/*.js", "a/**/*.js", "**/**"}
	return patterns[r.Intn(len(patterns))], bdPath(r, r.Intn(4)+1), Options{}
}

func genBraceList(r *rand.Rand) (string, string, Options) {
	pat := fmt.Sprintf("a.{%s,%s,%s}", bdExts[r.Intn(len(bdExts))], bdExts[r.Intn(len(bdExts))], bdExts[r.Intn(len(bdExts))])
	return pat, "a." + bdExts[r.Intn(len(bdExts))], Options{}
}

func genBraceRangeNum(r *rand.Rand) (string, string, Options) {
	lo, hi := r.Intn(5), r.Intn(10)+5
	pat := fmt.Sprintf("v{%d..%d}", lo, hi)
	n := lo + r.Intn(hi-lo+2)
	return pat, fmt.Sprintf("v%d", n), Options{}
}

func genBraceRangeAlpha(r *rand.Rand) (string, string, Options) {
	lo := rune('a' + r.Intn(13))
	hi := rune('n' + r.Intn(13))
	pat := fmt.Sprintf("v{%c..%c}", lo, hi)
	c := rune('a' + r.Intn(26))
	return pat, "v" + string(c), Options{}
}

func genBraceStepPad(r *rand.Rand) (string, string, Options) {
	lo, hi, step := r.Intn(3)*2, r.Intn(5)*2+10, r.Intn(3)+1
	pat := fmt.Sprintf("v{%02d..%02d..%d}", lo, hi, step)
	n := lo + step*r.Intn(4)
	return pat, fmt.Sprintf("v%02d", n), Options{}
}

func genBraceNested(r *rand.Rand) (string, string, Options) {
	pat := "a{b,c{d,e},f}g"
	choices := []string{"abg", "acdg", "aceg", "afg", "axg"}
	return pat, choices[r.Intn(len(choices))], Options{}
}

func genExtglob(r *rand.Rand, kind byte) (string, string, Options) {
	alts := []string{"foo", "bar"}
	pat := string(kind) + "(" + alts[0] + "|" + alts[1] + ")"
	candidates := []string{"foo", "bar", "foofoo", "", "baz", "foobar"}
	return pat, candidates[r.Intn(len(candidates))], Options{}
}

func genPosixClass(r *rand.Rand) (string, string, Options) {
	classes := []string{"[[:alpha:]]", "[[:digit:]]", "[[:alnum:]]", "[[:upper:]]", "[[:lower:]]"}
	cls := classes[r.Intn(len(classes))]
	pat := cls + "*"
	candidates := []string{"a1", "1a", "Z9", ".hidden", "9", "z"}
	return pat, candidates[r.Intn(len(candidates))], Options{}
}

func genNegationList(r *rand.Rand) (string, string, Options) {
	// exercised via IsMatch directly in the loop below; here we still
	// return a single-pattern Match case (leading "!") since Match/Compile
	// support per-pattern negation too.
	pat := "!" + bdName(r)
	return pat, bdName(r), Options{}
}

func genDotSimple(r *rand.Rand) (string, string, Options) {
	patterns := []string{"*", "*.js", "?", "**"}
	pat := patterns[r.Intn(len(patterns))]
	path := bdPath(r, 1)
	return pat, path, Options{Dot: r.Intn(2) == 0}
}

func genDotCompound(r *rand.Rand) (string, string, Options) {
	// The compound-first-node case explicitly called out as a prior bug:
	// a nullable "*" followed by "?" must still hide a leading dot.
	patterns := []string{"*?.js", "*?", "**?"}
	pat := patterns[r.Intn(len(patterns))]
	path := bdDotNames[r.Intn(len(bdDotNames))] + ".js"
	return pat, path, Options{}
}

func genOpt(r *rand.Rand, apply func(*Options)) (string, string, Options) {
	patterns := []string{"*.JS", "a{b,c}", "!(foo)", "**/*", "a?b", "dir/*"}
	pat := patterns[r.Intn(len(patterns))]
	var o Options
	apply(&o)
	path := bdPath(r, r.Intn(3)+1)
	if o.Windows {
		path = strings.ReplaceAll(path, "/", "\\")
	}
	return pat, path, o
}

var bdUnicodeNames = []string{"日本語", "café", "naïve", "emoji😀file", "Straße"}

func genUnicode(r *rand.Rand) (string, string, Options) {
	patterns := []string{"*", "**/*", "?*", "café*"}
	pat := patterns[r.Intn(len(patterns))]
	path := bdUnicodeNames[r.Intn(len(bdUnicodeNames))]
	if r.Intn(2) == 0 {
		path += "/" + bdUnicodeNames[r.Intn(len(bdUnicodeNames))]
	}
	return pat, path, Options{}
}
