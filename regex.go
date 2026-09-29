package globcompat

import (
	"fmt"
	"regexp"
	"strings"
)

// renderSegments turns a parsed, brace-expanded pattern into an RE2 regular
// expression body (no anchors, no case-insensitivity flag; the caller adds
// those). altPrefix must be unique across the alternatives of a single
// Compile call (brace expansion can produce several); it is used to keep
// this alternative's named capture groups distinct from the others.
//
// ok is false when the pattern uses a construct RE2 cannot express (a
// negated extglob, "!(...)"), in which case the caller falls back to the
// backtracking matcher in bt.go.
//
// dotGroups names the capture groups that hold a plain path segment whose
// match must not start with "." (picomatch hides dotfiles unless the
// segment *pattern* itself starts with a literal "."). RE2 has no
// lookahead, so this can't be enforced inline the way a JS regex would with
// "(?!\.)"; instead each such segment is wrapped in a named group, and the
// caller (globcompat.go) checks the captured text after a successful match.
// This is exact, not an approximation: a segment's matched span is fixed by
// the literal "/" boundaries around it regardless of which node inside it
// ends up consuming which character, so there is no alternative parse to
// miss.
func renderSegments(segs []segment, opts Options, altPrefix string) (re string, dotGroups []string, ok bool) {
	segs = collapseGlobstars(segs)
	var sb strings.Builder
	n := len(segs)
	groupN := 0
	for idx := 0; idx < n; idx++ {
		s := segs[idx]
		if s.globstar {
			leading := idx > 0
			trailing := idx < n-1
			mid := globstarMiddle(opts.Dot)
			// The wrapping "?" is lazy ("??"), not greedy: Go's regexp
			// engine resolves an anchored match deterministically (as if
			// by backtracking with these operators' priorities), and a
			// terminal segment's own guard (see dgterm below) rejects a
			// match where the globstar's greedy choice leaves it empty
			// when a non-empty alternative exists — e.g. "**/*" vs "a/b/"
			// needs the globstar to take "a/" so the trailing "*"
			// segment gets "b/", not consume nothing and leave "*" empty.
			// Lazy makes the engine try the smaller (often empty)
			// globstar match first and only grow it if that path doesn't
			// lead to an overall match, which is what lets the
			// terminal-segment guard see the intended split instead of
			// silently validating whichever arbitrary split RE2 found.
			switch {
			case leading && trailing:
				sb.WriteString(`/(?:` + mid + `/)??`)
			case leading && !trailing:
				sb.WriteString(`(?:/` + mid + `)??`)
			case !leading && trailing:
				sb.WriteString(`(?:` + mid + `/)??`)
			default:
				sb.WriteString(mid)
			}
			continue
		}
		if idx > 0 && !segs[idx-1].globstar {
			sb.WriteString("/")
		}
		isLastSeg := idx == n-1
		endsInStar := len(s.nodes) > 0 && s.nodes[len(s.nodes)-1].kind == nStar
		// The extra "must have had at least one character available"
		// requirement (see the "term" case below) only applies when the
		// segment's first node is "*" or "?", or the whole segment is
		// exactly a literal "." followed by the star with nothing between
		// (i.e. the pattern ".*"). A terminal "*" preceded by an ordinary
		// literal never gets it: isMatch('a/b', 'a/b*') is true (star
		// matches zero characters, nothing needed after 'b'), matching
		// picomatch's own compiled regex for such patterns, which has no
		// "(?=.)" at all — and neither does a literal "." followed by
		// anything other than just the star, e.g. isMatch('b/.c',
		// '?/.?*') is true even though its last segment also starts with
		// a literal ".": the "?" between the "." and the "*" is enough to
		// drop the requirement, the same way an ordinary literal does.
		needsEmptyCheck := isLastSeg && endsInStar && len(s.nodes) > 0 &&
			(s.nodes[0].kind == nStar || s.nodes[0].kind == nAnyChar ||
				(len(s.nodes) == 2 && s.nodes[0].kind == nLiteral && s.nodes[0].lit == '.'))

		var segRe string
		switch {
		case isLastSeg && endsInStar && needsEmptyCheck:
			// The pattern's terminal "*" gets two things a "*" elsewhere
			// doesn't: picomatch tolerates one trailing "/" after it,
			// treating "name/" like "name" (isMatch('a/', '*') and
			// isMatch('a/b/', 'a/b*') are both true), and — only in this
			// case — it requires at least one character to have been
			// available when it started matching (isMatch('a/b/',
			// 'a/b/*') is false; isMatch('.', '.*') is false the same
			// way). That second requirement is checked by wrapping just
			// this node — not the whole segment — in a "term" group and
			// having matchWithDotGuard reject a match where the group's
			// start offset is the end of path: a terminal "*" elsewhere
			// in a longer segment (not last) never gets this, e.g.
			// isMatch('/a', '*/a') is true even though its "*" may also
			// match zero characters, because there is still "/a" left
			// for the rest of the pattern to require.
			head, o := renderSegmentNodes(s.nodes[:len(s.nodes)-1], opts)
			if !o {
				return "", nil, false
			}
			name := fmt.Sprintf("term%s_%d", altPrefix, groupN)
			groupN++
			segRe = head + "(?P<" + name + ">[^/]*)/?"
			dotGroups = append(dotGroups, name)
		case isLastSeg && endsInStar:
			head, o := renderSegmentNodes(s.nodes[:len(s.nodes)-1], opts)
			if !o {
				return "", nil, false
			}
			segRe = head + `[^/]*/?`
		default:
			var o bool
			segRe, o = renderSegmentNodes(s.nodes, opts)
			if !o {
				return "", nil, false
			}
		}
		if !opts.Dot && segmentNeedsDotGuard(s.nodes) {
			name := fmt.Sprintf("dg%s_%d", altPrefix, groupN)
			groupN++
			sb.WriteString("(?P<" + name + ">" + segRe + ")")
			dotGroups = append(dotGroups, name)
		} else {
			sb.WriteString(segRe)
		}
	}
	return sb.String(), dotGroups, true
}

// segmentNeedsDotGuard reports whether a segment's matched text must be
// checked against a leading ".", per picomatch: dot-hiding applies unless
// the segment pattern's first token is a bracket expression or an extglob
// group (both are "explicit" as far as picomatch is concerned, whatever
// they match), or is itself a literal ".".
func segmentNeedsDotGuard(nodes []node) bool {
	if len(nodes) == 0 {
		return false
	}
	switch nodes[0].kind {
	case nClass, nExt:
		return false
	case nLiteral:
		return nodes[0].lit != '.'
	default: // nStar, nAnyChar
		return true
	}
}

// collapseGlobstars merges consecutive globstar segments ("**/**/x") into a
// single one, matching picomatch's treatment of redundant globstars.
func collapseGlobstars(segs []segment) []segment {
	var out []segment
	for _, s := range segs {
		if s.globstar && len(out) > 0 && out[len(out)-1].globstar {
			continue
		}
		out = append(out, s)
	}
	return out
}

// globstarMiddle returns the regex core for what "**" spans: zero or more
// "/"-joined pieces, each either empty or (unless dot is true) not starting
// with ".". picomatch's own globstar matches across empty path segments —
// "/a/" matches "**", "a//b" matches "a/**/b" — rather than requiring each
// segment to be non-empty, so the piece itself is optional
// ("(?:[^/.][^/]*)?"), and the whole core can be empty (verified against
// picomatch: isMatch('/a/', '**'), isMatch('a//b', 'a/**/b'), and
// isMatch('a//', 'a/**') are all true).
func globstarMiddle(dot bool) string {
	// Every quantifier here is lazy, matching as little as possible: this
	// construct is only ever used inside an outer lazy "??" wrapper (see
	// renderSegments), and Go's regexp resolves a single deterministic
	// split for an anchored match by the same greedy/lazy priority a
	// backtracking engine would use — laziness all the way down is what
	// lets a following terminal segment (see the "dgterm" guard) end up
	// with the split that leaves it non-empty, when one exists, instead
	// of the globstar swallowing everything it structurally can.
	if dot {
		return `[^/]*?(?:/[^/]*?)*?`
	}
	return `(?:[^/.][^/]*?)??(?:/(?:[^/.][^/]*?)??)*?`
}

// renderSegmentNodes renders a single path segment's node sequence to an
// RE2 fragment. Every node renders the same way regardless of position;
// dot-hiding is applied once per segment by the caller (renderSegments), not
// per node — see segmentNeedsDotGuard. ok is false if the segment contains a
// negated extglob.
func renderSegmentNodes(nodes []node, opts Options) (string, bool) {
	var sb strings.Builder
	for _, nd := range nodes {
		switch nd.kind {
		case nLiteral:
			sb.WriteString(regexp.QuoteMeta(string(nd.lit)))
		case nAnyChar:
			sb.WriteString(`[^/]`)
		case nStar:
			sb.WriteString(`[^/]*`)
		case nClass:
			sb.WriteString(renderClass(nd.cls))
		case nExt:
			if nd.ext == '!' {
				return "", false
			}
			group, ok := renderExtAlts(nd.alts, opts)
			if !ok {
				return "", false
			}
			switch nd.ext {
			case '@':
				sb.WriteString(group)
			case '+':
				sb.WriteString(group + "+")
			case '*':
				sb.WriteString(group + "*")
			case '?':
				sb.WriteString(group + "?")
			}
		}
	}
	return sb.String(), true
}

func renderExtAlts(alts [][]node, opts Options) (string, bool) {
	rendered := make([]string, 0, len(alts))
	for _, a := range alts {
		as, ok := renderSegmentNodes(a, opts)
		if !ok {
			return "", false
		}
		rendered = append(rendered, as)
	}
	return "(?:" + strings.Join(rendered, "|") + ")", true
}

// renderClass renders a bracket expression's contents to an RE2 character
// class. picomatch and RE2 both accept POSIX class names like "[:alpha:]"
// inside "[...]", so the raw contents pass through mostly unchanged; a
// literal leading "]" (valid in glob bracket syntax without escaping) is
// escaped for RE2.
//
// Only "^" negates a bracket expression, matching picomatch: "[!x]" is not
// negation there (picomatch has no "!" bracket-negation form the way some
// shells do); "!" appearing right after "[" is just a literal member
// character, which is how parseClass in ast.go parses it too.
func renderClass(cls classSpec) string {
	src := cls.src
	if strings.HasPrefix(src, "]") {
		src = `\]` + src[1:]
	}
	if cls.negate {
		return "[^" + src + "]"
	}
	return "[" + src + "]"
}

// containsNegatedExtglob reports whether any segment's node tree contains a
// "!(...)" extglob, which forces the backtracking matcher.
func containsNegatedExtglob(segs []segment) bool {
	for _, s := range segs {
		if s.globstar {
			continue
		}
		if nodesContainNegatedExtglob(s.nodes) {
			return true
		}
	}
	return false
}

func nodesContainNegatedExtglob(nodes []node) bool {
	for _, nd := range nodes {
		if nd.kind != nExt {
			continue
		}
		if nd.ext == '!' {
			return true
		}
		for _, alt := range nd.alts {
			if nodesContainNegatedExtglob(alt) {
				return true
			}
		}
	}
	return false
}
