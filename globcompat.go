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
	"strconv"
	"strings"
)

// maxPatternLength bounds Compile's input. It exists to keep the
// backtracking matcher's worst case bounded (see bt.go) and to fail fast on
// clearly-pathological input rather than spend unbounded CPU compiling it.
const maxPatternLength = 4096

// Matcher is a compiled glob pattern, ready to test candidate paths
// repeatedly without re-parsing the pattern each time.
type Matcher struct {
	opts            Options
	re              *regexp.Regexp // fast path; nil if backtracking is required
	altSegs         [][]segment    // backtracking path: one []segment per brace expansion
	negated         bool
	source          string
	requireNonEmpty bool // see patternStartsWithExtglob
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

	if opts.NoExtglob && !opts.NoNegate && strings.HasPrefix(pattern, "!(") {
		if m, ok, err := compileNoExtglobBang(pattern, opts); err != nil {
			return nil, err
		} else if ok {
			return m, nil
		}
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
	requireNonEmpty := false
	for _, p := range patterns {
		segs, err := parsePattern(p, opts)
		if err != nil {
			return nil, err
		}
		allSegs = append(allSegs, segs)
		if containsNegatedExtglob(segs) {
			needsBT = true
		}
		if patternStartsWithExtglob(segs) {
			requireNonEmpty = true
		}
	}

	m := &Matcher{opts: opts, negated: negated, source: pattern, requireNonEmpty: requireNonEmpty}

	if !needsBT {
		var alts []string
		ok := true
		for i, segs := range allSegs {
			re, _, o := renderSegments(segs, opts, strconv.Itoa(i))
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
	if m.opts.Basename {
		path = basename(path)
	}
	if m.requireNonEmpty && path == "" {
		return false
	}
	if m.re != nil {
		if m.opts.Contains {
			// An unanchored regexp can match starting later in the path
			// when the leftmost match trips a guard (picomatch's
			// unanchored "(?!\.)" just fails there and the engine
			// retries at the next offset), so do the same.
			for i := 0; i <= len(path); i++ {
				if matchWithDotGuard(m.re, path[i:]) {
					return true
				}
			}
			return false
		}
		return matchWithDotGuard(m.re, path)
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

// matchWithDotGuard runs re against path and, if re has named capture
// groups, rejects the match when a guard added by renderSegments (see
// regex.go) is violated. Ordinary unguarded segments/nodes are never
// captured, so this only does the extra work when a guard is present.
// There are two independent, possibly-nested kinds of group, distinguished
// by name prefix:
//
//   - "dg...": wraps a whole non-dot segment; reject if the captured text
//     starts with ".". A segment's matched span is fixed by the literal
//     "/" boundaries around it regardless of which node inside it ends up
//     consuming which character, so there is no alternative parse to miss
//     by checking only the text that was actually captured.
//   - "term...": wraps just the pattern's terminal "*" node (the last node
//     of the last segment); reject if the group's start offset is the end
//     of path, meaning nothing was left for it to work with — not whether
//     its own captured text is empty, since it may legitimately match zero
//     characters when there is still something after it for the rest of
//     the segment to require (picomatch: isMatch('/a', '*/a') is true, but
//     isMatch('a/b/', 'a/b/*') and isMatch('.', '.*') are both false).
func matchWithDotGuard(re *regexp.Regexp, path string) bool {
	if re.NumSubexp() == 0 {
		return re.MatchString(path)
	}
	idx := re.FindStringSubmatchIndex(path)
	if idx == nil {
		return false
	}
	names := re.SubexpNames()
	for i, name := range names {
		if name == "" || i == 0 {
			continue
		}
		start, end := idx[2*i], idx[2*i+1]
		if start < 0 {
			continue // group did not participate in this match
		}
		switch {
		case strings.HasPrefix(name, "dg"):
			if start < end && path[start] == '.' {
				return false
			}
		case strings.HasPrefix(name, "term"):
			if start == len(path) {
				return false
			}
		}
	}
	return true
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

// compileNoExtglobBang reproduces a picomatch quirk: with noextglob set, a
// pattern that is exactly "!(a|b)" is not treated as literal text. The
// leading "!" is taken as whole-pattern negation, and the remaining bare
// "(a|b)" group must match the entire path (across "/") for the negation
// to reject it. picomatch compiles pm.makeRe('!(foo)', {noextglob:true}) to
// ^(?!^(?:(foo))$).*$, so isMatch('a/b', '!(foo)', {noextglob:true}) is
// true and isMatch('foo', ...) is false. The quirk only applies when "!(" is
// the very start of the pattern and its ")" is the very end; elsewhere
// noextglob leaves the text literal. ok is false when the shape doesn't fit.
func compileNoExtglobBang(pattern string, opts Options) (*Matcher, bool, error) {
	rs := []rune(pattern)
	end, alts, err := parseExtglob(rs, 2)
	if err != nil || end != len(rs)-1 {
		return nil, false, nil
	}
	parsed := make([][]node, 0, len(alts))
	for _, a := range alts {
		nodes, err := parseSegment(a, opts)
		if err != nil {
			return nil, false, err
		}
		parsed = append(parsed, nodes)
	}
	group, ok := renderExtAlts(parsed, opts)
	if !ok {
		return nil, false, nil
	}
	src := "^" + group + "$"
	if opts.NoCase {
		src = "(?i)" + src
	}
	re, err := regexp.Compile(src)
	if err != nil {
		return nil, false, fmt.Errorf("globcompat: compiling %q: %w", pattern, err)
	}
	return &Matcher{opts: opts, re: re, negated: true, source: pattern}, true, nil
}
