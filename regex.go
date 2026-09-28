package globcompat

import (
	"regexp"
	"strings"
)

// renderSegments turns a parsed, brace-expanded pattern into an RE2 regular
// expression body (no anchors, no case-insensitivity flag; the caller adds
// those). ok is false when the pattern uses a construct RE2 cannot express
// (a negated extglob, "!(...)"), in which case the caller falls back to the
// backtracking matcher in bt.go.
func renderSegments(segs []segment, opts Options) (string, bool) {
	segs = collapseGlobstars(segs)
	var sb strings.Builder
	n := len(segs)
	for idx := 0; idx < n; idx++ {
		s := segs[idx]
		if s.globstar {
			leading := idx > 0
			trailing := idx < n-1
			gsne := globstarCore(opts.Dot)
			switch {
			case leading && trailing:
				sb.WriteString(`/(?:|` + gsne + `/)`)
			case leading && !trailing:
				sb.WriteString(`(?:/` + gsne + `)?`)
			case !leading && trailing:
				sb.WriteString(`(?:` + gsne + `/)?`)
			default:
				sb.WriteString(`(?:` + gsne + `)?`)
			}
			continue
		}
		if idx > 0 && !segs[idx-1].globstar {
			sb.WriteString("/")
		}
		segRe, ok := renderSegmentNodes(s.nodes, opts)
		if !ok {
			return "", false
		}
		sb.WriteString(segRe)
	}
	return sb.String(), true
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

// globstarCore returns the regex core matching one-or-more path segments,
// none of which start with "." unless dot is true.
func globstarCore(dot bool) string {
	if dot {
		return `[^/]+(?:/[^/]+)*`
	}
	return `[^/.][^/]*(?:/[^/.][^/]*)*`
}

// renderSegmentNodes renders a single path segment's node sequence to an
// RE2 fragment. ok is false if it contains a negated extglob.
func renderSegmentNodes(nodes []node, opts Options) (string, bool) {
	var sb strings.Builder
	for i, nd := range nodes {
		atStart := i == 0
		switch nd.kind {
		case nLiteral:
			sb.WriteString(regexp.QuoteMeta(string(nd.lit)))
		case nAnyChar:
			if atStart && !opts.Dot {
				sb.WriteString(`[^/.]`)
			} else {
				sb.WriteString(`[^/]`)
			}
		case nStar:
			if atStart && !opts.Dot {
				sb.WriteString(`(?:[^/.][^/]*)?`)
			} else {
				sb.WriteString(`[^/]*`)
			}
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
