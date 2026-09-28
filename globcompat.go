// Package globcompat matches file paths against glob patterns using the
// semantics of picomatch and micromatch (https://github.com/micromatch),
// the matcher behind lint-staged, chokidar, fast-glob, Jest, Vite and much
// of the rest of the JavaScript ecosystem. It exists so a Go program can
// honour a glob config that was written and tested against those tools —
// "**/*.{js,ts}", "!(*.test).go", "src/**/!(*.min).js" — instead of falling
// back to Go's shell-glob-flavoured filepath.Match or a globstar library
// with different extglob and brace behaviour.
//
// Compile a pattern once and reuse the Matcher, or use the package-level
// Match / MatchAny / IsMatch for one-off calls:
//
//	m, err := globcompat.Compile("src/**/*.{ts,tsx}", globcompat.Options{})
//	if err != nil {
//		return err
//	}
//	if m.Match("src/components/Button.tsx") {
//		// ...
//	}
//
// See README.md for the exact feature set implemented and its Limitations
// section for what picomatch supports that this package deliberately does
// not.
package globcompat

import (
	"fmt"
	"regexp"
	"strings"
)

// maxPatternLength bounds Compile's input. It exists to keep the
// backtracking matcher's worst case bounded (see bt.go) and to fail fast on
// clearly-pathological input rather than spend unbounded CPU compiling it.
const maxPatternLength = 4096

// Matcher is a compiled glob pattern, ready to test candidate paths
// repeatedly without re-parsing the pattern each time.
type Matcher struct {
	opts    Options
	re      *regexp.Regexp // fast path; nil if backtracking is required
	altSegs [][]segment    // backtracking path: one []segment per brace expansion
	negated bool
	source  string
}

// Compile parses pattern under opts and returns a reusable Matcher.
//
// A leading "!" is treated as an ordinary negation marker only by MatchAny
// and IsMatch, which combine several patterns; Compile and (*Matcher).Match
// match the pattern literally, including any leading "!", except that
// Matcher.Negated() reports whether one was present so callers building
// their own list semantics can use it.
func Compile(pattern string, opts Options) (*Matcher, error) {
	if len(pattern) > maxPatternLength {
		return nil, fmt.Errorf("globcompat: pattern of %d bytes exceeds the %d byte limit", len(pattern), maxPatternLength)
	}

	body := pattern
	negated := false
	if !opts.NoNegate && strings.HasPrefix(body, "!") && !strings.HasPrefix(body, "!(") {
		negated = true
		body = body[1:]
	}

	var patterns []string
	if opts.NoBrace {
		patterns = []string{body}
	} else {
		expanded, err := Braces(body)
		if err != nil {
			return nil, err
		}
		patterns = expanded
	}

	var allSegs [][]segment
	needsBT := false
	for _, p := range patterns {
		segs, err := parsePattern(p, opts)
		if err != nil {
			return nil, err
		}
		allSegs = append(allSegs, segs)
		if containsNegatedExtglob(segs) {
			needsBT = true
		}
	}

	m := &Matcher{opts: opts, negated: negated, source: pattern}

	if !needsBT {
		var alts []string
		ok := true
		for _, segs := range allSegs {
			re, o := renderSegments(segs, opts)
			if !o {
				ok = false
				break
			}
			alts = append(alts, re)
		}
		if ok {
			body := strings.Join(alts, "|")
			if len(alts) > 1 {
				body = "(?:" + body + ")"
			}
			var src string
			if opts.Contains {
				src = body
			} else {
				src = "^" + body + "$"
			}
			if opts.NoCase {
				src = "(?i)" + src
			}
			re, err := regexp.Compile(src)
			if err != nil {
				return nil, fmt.Errorf("globcompat: compiling %q: %w", pattern, err)
			}
			m.re = re
			return m, nil
		}
	}

	m.altSegs = allSegs
	return m, nil
}

// Negated reports whether the compiled pattern had a leading "!" marker.
func (m *Matcher) Negated() bool { return m.negated }

// String returns the original pattern passed to Compile.
func (m *Matcher) String() string { return m.source }

// Match reports whether path matches the compiled pattern. Following
// picomatch's own isMatch, a leading "!" (unless opts.NoNegate is set)
// negates the result: Match returns true when path does NOT match the rest
// of the pattern. MatchAny and IsMatch build list-exclusion semantics on
// top of that per-pattern behaviour.
func (m *Matcher) Match(path string) bool {
	got := m.matchInner(path)
	if m.negated {
		return !got
	}
	return got
}

func (m *Matcher) matchInner(path string) bool {
	path = normalizePath(path, m.opts)
	if m.opts.Basename && !strings.Contains(m.source, "/") {
		path = basename(path)
	}
	if m.re != nil {
		return m.re.MatchString(path)
	}
	for _, segs := range m.altSegs {
		if m.opts.Contains {
			if containsSegMatch(segs, path, m.opts) {
				return true
			}
		} else if matchSegsBT(segs, path, m.opts) {
			return true
		}
	}
	return false
}

// containsSegMatch implements Options.Contains ("match anywhere in the
// path") for the backtracking path, by trying the matcher anchored at every
// possible starting path segment and every suffix truncation. This is only
// reached for patterns with a negated extglob and Contains both set, an
// unusual combination; it is correct but not fast.
func containsSegMatch(segs []segment, path string, opts Options) bool {
	parts := strings.Split(path, "/")
	for i := 0; i <= len(parts); i++ {
		for j := i; j <= len(parts); j++ {
			if matchSegsBT(segs, strings.Join(parts[i:j], "/"), opts) {
				return true
			}
		}
	}
	return false
}

func normalizePath(path string, opts Options) string {
	if opts.Windows {
		path = strings.ReplaceAll(path, "\\", "/")
	}
	return path
}

func basename(path string) string {
	path = strings.TrimSuffix(path, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Match compiles pattern and reports whether path matches it under opts. If
// you will test the same pattern against many paths, call Compile once and
// reuse the Matcher instead.
func Match(pattern, path string, opts Options) (bool, error) {
	m, err := Compile(pattern, opts)
	if err != nil {
		return false, err
	}
	return m.Match(path), nil
}

// MatchAny reports whether path matches at least one of patterns (each
// compiled and matched independently under opts). It does not apply
// micromatch's list-negation semantics; use IsMatch for that.
func MatchAny(patterns []string, path string, opts Options) (bool, error) {
	for _, p := range patterns {
		ok, err := Match(p, path, opts)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// IsMatch reports whether path matches patterns under micromatch's isMatch
// list semantics: path matches if it matches at least one non-negated
// pattern (or there are none, in which case every path matches by default)
// and does not match any pattern with a leading "!" (unless opts.NoNegate
// is set, in which case "!" is not special and every pattern is positive).
func IsMatch(path string, patterns []string, opts Options) (bool, error) {
	var positives, negatives []string
	for _, p := range patterns {
		if !opts.NoNegate && strings.HasPrefix(p, "!") && !strings.HasPrefix(p, "!(") {
			negatives = append(negatives, p[1:])
		} else {
			positives = append(positives, p)
		}
	}

	matched := len(positives) == 0
	if !matched {
		ok, err := MatchAny(positives, path, opts)
		if err != nil {
			return false, err
		}
		matched = ok
	}
	if !matched {
		return false, nil
	}
	for _, n := range negatives {
		ok, err := Match(n, path, opts)
		if err != nil {
			return false, err
		}
		if ok {
			return false, nil
		}
	}
	return true, nil
}
