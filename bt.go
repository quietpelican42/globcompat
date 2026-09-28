package globcompat

import "strings"

// matchSegsBT is the backtracking matcher used whenever a pattern contains
// a negated extglob ("!(...)"), which cannot be expressed as an RE2 regular
// expression (RE2 has no lookaround). It mirrors the same segment/globstar
// semantics as renderSegments, but walks the candidate string directly.
//
// Complexity: matching within one path segment is memoized over (node
// index, rune offset), giving O(nodes * len(segment)) states, each with a
// bounded number of transitions from extglob alternation; globstar
// backtracking across path segments tries each split point once, so overall
// work is O(segments^2 * per-segment cost) in the worst case. Patterns seen
// in practice (a handful of segments, short extglob alternatives) stay well
// inside this bound; pathological patterns are rejected by Compile's
// pattern-length guard (see globcompat.go).
func matchSegsBT(segs []segment, path string, opts Options) bool {
	segs = collapseGlobstars(segs)
	pathSegs := strings.Split(path, "/")
	return matchSegSeq(segs, pathSegs, opts)
}

func matchSegSeq(segs []segment, pathSegs []string, opts Options) bool {
	if len(segs) == 0 {
		return len(pathSegs) == 0
	}
	s := segs[0]
	if s.globstar {
		// Try consuming 0..len(pathSegs) path segments as the globstar,
		// each of which must not start with "." unless dot is set.
		for k := 0; k <= len(pathSegs); k++ {
			ok := true
			for i := 0; i < k && !opts.Dot; i++ {
				if strings.HasPrefix(pathSegs[i], ".") {
					ok = false
					break
				}
			}
			if ok && matchSegSeq(segs[1:], pathSegs[k:], opts) {
				return true
			}
		}
		return false
	}
	if len(pathSegs) == 0 {
		return false
	}
	if !matchNodesFull(s.nodes, []rune(pathSegs[0]), opts) {
		return false
	}
	return matchSegSeq(segs[1:], pathSegs[1:], opts)
}

type btKey struct {
	ni, si int
}

// matchNodesFull reports whether nodes matches s in its entirety.
func matchNodesFull(nodes []node, s []rune, opts Options) bool {
	memo := make(map[btKey]int8)
	return matchFrom(nodes, 0, s, 0, opts, memo)
}

func matchFrom(nodes []node, ni int, s []rune, si int, opts Options, memo map[btKey]int8) bool {
	key := btKey{ni, si}
	if v, ok := memo[key]; ok {
		return v == 1
	}
	// Guard against runaway recursion on adversarial input.
	if len(memo) > 2_000_000 {
		return false
	}
	res := matchFromUncached(nodes, ni, s, si, opts, memo)
	if res {
		memo[key] = 1
	} else {
		memo[key] = 0
	}
	return res
}

func matchFromUncached(nodes []node, ni int, s []rune, si int, opts Options, memo map[btKey]int8) bool {
	if ni == len(nodes) {
		return si == len(s)
	}
	nd := nodes[ni]
	switch nd.kind {
	case nLiteral:
		if si < len(s) && s[si] == nd.lit {
			return matchFrom(nodes, ni+1, s, si+1, opts, memo)
		}
		return false
	case nAnyChar:
		if si >= len(s) {
			return false
		}
		if si == 0 && !opts.Dot && s[si] == '.' {
			return false
		}
		return matchFrom(nodes, ni+1, s, si+1, opts, memo)
	case nStar:
		for k := 0; si+k <= len(s); k++ {
			if k > 0 && si == 0 && !opts.Dot && s[0] == '.' {
				break
			}
			if matchFrom(nodes, ni+1, s, si+k, opts, memo) {
				return true
			}
		}
		return false
	case nClass:
		if si >= len(s) {
			return false
		}
		if si == 0 && !opts.Dot && s[si] == '.' {
			return false
		}
		if !classMatches(nd.cls, s[si]) {
			return false
		}
		return matchFrom(nodes, ni+1, s, si+1, opts, memo)
	case nExt:
		return matchExtFrom(nd, nodes, ni, s, si, opts, memo)
	}
	return false
}

// matchExtFrom handles one extglob node starting at (ni, si), then
// continues with the rest of nodes.
func matchExtFrom(nd node, nodes []node, ni int, s []rune, si int, opts Options, memo map[btKey]int8) bool {
	rest := func(newSi int) bool {
		return matchFrom(nodes, ni+1, s, newSi, opts, memo)
	}
	switch nd.ext {
	case '@':
		for _, alt := range nd.alts {
			if end, ok := matchAltPrefix(alt, s, si, opts); ok && rest(end) {
				return true
			}
		}
		return matchAnyAltAnyEnd(nd.alts, s, si, opts, rest, 1, 1)
	case '?':
		if rest(si) {
			return true
		}
		return matchAnyAltAnyEnd(nd.alts, s, si, opts, rest, 0, 1)
	case '*':
		if rest(si) {
			return true
		}
		return matchAnyAltAnyEnd(nd.alts, s, si, opts, rest, 1, -1)
	case '+':
		return matchAnyAltAnyEnd(nd.alts, s, si, opts, rest, 1, -1)
	case '!':
		// Try every possible length k for the substring this group
		// consumes; it matches so long as that substring does not fully
		// match any alternative, and the remainder of the pattern matches
		// what follows.
		for k := 0; si+k <= len(s); k++ {
			excluded := false
			for _, alt := range nd.alts {
				if matchNodesFullFrom(alt, s[si:si+k], opts) {
					excluded = true
					break
				}
			}
			if !excluded && rest(si+k) {
				return true
			}
		}
		return false
	}
	return false
}

// matchNodesFullFrom is matchNodesFull over a rune slice that is already a
// sub-slice (avoids re-copying).
func matchNodesFullFrom(nodes []node, s []rune, opts Options) bool {
	memo := make(map[btKey]int8)
	return matchFrom(nodes, 0, s, 0, opts, memo)
}

// matchAltPrefix tries to match alt as a prefix of s starting at si,
// returning the first (greedy) end offset that lets alt itself fully
// consume some prefix. It tries longest-to-shortest via full backtracking
// through a bounded search of possible end offsets.
func matchAltPrefix(alt []node, s []rune, si int, opts Options) (int, bool) {
	for end := len(s); end >= si; end-- {
		if matchNodesFullFrom(alt, s[si:end], opts) {
			return end, true
		}
	}
	return 0, false
}

// matchAnyAltAnyEnd tries to match between minReps and maxReps (maxReps<0
// means unbounded) repetitions of any alternative in alts, back-to-back
// starting at si, calling rest(end) after each candidate total length; it
// returns true on the first combination for which rest succeeds.
func matchAnyAltAnyEnd(alts [][]node, s []rune, si int, opts Options, rest func(int) bool, minReps, maxReps int) bool {
	var try func(pos, reps int) bool
	try = func(pos, reps int) bool {
		if reps >= minReps {
			if rest(pos) {
				return true
			}
		}
		if maxReps >= 0 && reps >= maxReps {
			return false
		}
		for end := pos + 1; end <= len(s); end++ {
			for _, alt := range alts {
				if matchNodesFullFrom(alt, s[pos:end], opts) {
					if try(end, reps+1) {
						return true
					}
				}
			}
		}
		return false
	}
	return try(si, 0)
}

func classMatches(cls classSpec, r rune) bool {
	re := compileClassOnce(cls)
	if re == nil {
		return false
	}
	return re.MatchString(string(r))
}
