package globcompat

import "fmt"

// nodeKind identifies the kind of a parsed glob AST node. A compiled pattern
// is a flat sequence of nodes within a path segment; segments are split on
// unescaped "/" before parsing (see splitSegments in segments.go).
type nodeKind int

const (
	nLiteral nodeKind = iota
	nAnyChar          // ?
	nStar             // *
	nClass            // [...]
	nExt              // extglob group: @(...) +(...) *(...) ?(...) !(...)
)

type node struct {
	kind nodeKind
	lit  rune      // nLiteral
	cls  classSpec // nClass
	ext  byte      // nExt: '@', '+', '*', '?', '!'
	alts [][]node  // nExt: each alternative is itself a node sequence
}

type classSpec struct {
	negate bool
	src    string // the raw content between [ and ], negation marker stripped
}

// segment is one "/"-delimited piece of a pattern, already brace-expanded.
type segment struct {
	globstar bool   // true if this whole segment is "**" (and globstar enabled)
	nodes    []node // parsed content when !globstar
}

// parsePattern splits pattern into "/"-delimited segments (respecting
// brackets and extglob parens, which may themselves contain "/") and parses
// each non-globstar segment into a node sequence.
func parsePattern(pattern string, opts Options) ([]segment, error) {
	raw := splitSegments(pattern)
	segs := make([]segment, 0, len(raw))
	for _, r := range raw {
		if !opts.NoGlobstar && isGlobstarSegment(r) {
			segs = append(segs, segment{globstar: true})
			continue
		}
		nodes, err := parseSegment(r, opts)
		if err != nil {
			return nil, err
		}
		segs = append(segs, segment{nodes: nodes})
	}
	return segs, nil
}

// isGlobstarSegment reports whether a single path segment (no "/" inside)
// is exactly "**". picomatch only treats precisely two stars occupying a
// whole segment as a globstar; a run of three or more ("***") is not
// special at all and behaves exactly like a single "*" (verified against
// picomatch: isMatch('a/b', '***') is false, the same as isMatch('a/b',
// '*'), while isMatch('ab', '***') is true).
func isGlobstarSegment(seg string) bool {
	return seg == "**"
}

// splitSegments splits on "/" that is not escaped and not inside a bracket
// expression or an extglob group's parens.
func splitSegments(pattern string) []string {
	rs := []rune(pattern)
	var out []string
	var cur []rune
	depth := 0 // paren nesting (extglob)
	inClass := false
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			cur = append(cur, c, rs[i+1])
			i++
		case c == '[' && !inClass:
			inClass = true
			cur = append(cur, c)
		case c == ']' && inClass:
			inClass = false
			cur = append(cur, c)
		case inClass:
			cur = append(cur, c)
		case isExtOpen(rs, i):
			depth++
			cur = append(cur, rs[i], rs[i+1])
			i++
		case c == '(':
			depth++
			cur = append(cur, c)
		case c == ')' && depth > 0:
			depth--
			cur = append(cur, c)
		case c == '/' && depth == 0:
			out = append(out, string(cur))
			cur = nil
		default:
			cur = append(cur, c)
		}
	}
	out = append(out, string(cur))
	return out
}

// isExtOpen reports whether rs[i:i+2] opens an extglob group, e.g. "@(".
func isExtOpen(rs []rune, i int) bool {
	if i+1 >= len(rs) {
		return false
	}
	if rs[i+1] != '(' {
		return false
	}
	switch rs[i] {
	case '@', '+', '*', '?', '!':
		return true
	}
	return false
}

// parseSegment parses one path segment (no unescaped "/") into a node
// sequence.
func parseSegment(seg string, opts Options) ([]node, error) {
	rs := []rune(seg)
	var out []node
	i := 0
	for i < len(rs) {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			out = append(out, node{kind: nLiteral, lit: rs[i+1]})
			i += 2
		case !opts.NoExtglob && isExtOpen(rs, i):
			end, alts, err := parseExtglob(rs, i+2)
			if err != nil {
				return nil, err
			}
			var parsedAlts [][]node
			for _, alt := range alts {
				an, err := parseSegment(alt, opts)
				if err != nil {
					return nil, err
				}
				parsedAlts = append(parsedAlts, an)
			}
			out = append(out, node{kind: nExt, ext: byte(c), alts: parsedAlts})
			i = end + 1 // skip past closing ')'
		case c == '*':
			// picomatch collapses any run of consecutive "*" within a
			// segment to a single star node — "a***b" behaves exactly
			// like "a*b" — not just the whole-segment "**"/"***" cases
			// isGlobstarSegment already handles. Collapsing here also
			// keeps exactly one star node per run, which matters for the
			// terminal-star handling in regex.go (renderSegments): with
			// three separate un-collapsed star nodes, RE2's own greedy
			// resolution of the first two can consume everything and
			// starve the third, wrongly tripping its "was anything left"
			// guard.
			out = append(out, node{kind: nStar})
			i++
			for i < len(rs) && rs[i] == '*' {
				i++
			}
		case c == '?':
			out = append(out, node{kind: nAnyChar})
			i++
		case c == '[':
			cls, end, err := parseClass(rs, i)
			if err != nil {
				return nil, err
			}
			out = append(out, node{kind: nClass, cls: cls})
			i = end + 1
		default:
			out = append(out, node{kind: nLiteral, lit: c})
			i++
		}
	}
	return out, nil
}

// parseExtglob scans from just after "X(" (start index of the group body)
// to the matching ")", honoring nested extglob parens and bracket
// expressions, and splits the body on top-level "|" into alternatives. It
// returns the index of the matching ")".
func parseExtglob(rs []rune, start int) (end int, alts []string, err error) {
	depth := 0
	inClass := false
	var cur []rune
	i := start
	for ; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			cur = append(cur, c, rs[i+1])
			i++
		case c == '[' && !inClass:
			inClass = true
			cur = append(cur, c)
		case c == ']' && inClass:
			inClass = false
			cur = append(cur, c)
		case inClass:
			cur = append(cur, c)
		case isExtOpen(rs, i):
			depth++
			cur = append(cur, rs[i], rs[i+1])
			i++
		case c == '(':
			depth++
			cur = append(cur, c)
		case c == ')':
			if depth == 0 {
				alts = append(alts, string(cur))
				return i, alts, nil
			}
			depth--
			cur = append(cur, c)
		case c == '|' && depth == 0:
			alts = append(alts, string(cur))
			cur = nil
		default:
			cur = append(cur, c)
		}
	}
	return 0, nil, fmt.Errorf("globcompat: unbalanced extglob parentheses")
}

// parseClass parses a bracket expression starting at rs[i] == '['. It
// returns the classSpec and the index of the matching ']'.
//
// Only "^" negates, matching picomatch: unlike some shells' extglob mode,
// picomatch does not treat a leading "!" inside "[...]" as negation, so
// "[!x]" is a positive class containing the literal characters '!' and 'x'
// (confirmed against picomatch: isMatch('axd', 'a[!bc]d') is false, and
// isMatch('abd', 'a[!bc]d') is true because 'b' is a literal member).
func parseClass(rs []rune, i int) (classSpec, int, error) {
	j := i + 1
	negate := false
	if j < len(rs) && rs[j] == '^' {
		negate = true
		j++
	}
	start := j
	// A "]" immediately after the opening (or negation) is a literal
	// member, per POSIX/glob convention.
	if j < len(rs) && rs[j] == ']' {
		j++
	}
	for j < len(rs) && rs[j] != ']' {
		if rs[j] == '[' && j+1 < len(rs) && rs[j+1] == ':' {
			k := j + 2
			for k+1 < len(rs) && !(rs[k] == ':' && rs[k+1] == ']') {
				k++
			}
			j = k + 2
			continue
		}
		j++
	}
	if j >= len(rs) {
		return classSpec{}, 0, fmt.Errorf("globcompat: unbalanced bracket expression")
	}
	return classSpec{negate: negate, src: string(rs[start:j])}, j, nil
}
