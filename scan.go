package globcompat

import "strings"

// ScanResult is the base/glob split of a pattern, as returned by Scan.
type ScanResult struct {
	// Base is the longest literal (non-glob) directory prefix of the
	// pattern, always ending in "/" unless it is empty, in which case the
	// caller should walk from ".". A directory walker can list Base
	// directly and apply Glob only from there, instead of walking from the
	// filesystem root for every pattern.
	Base string

	// Glob is the remainder of the pattern after Base.
	Glob string

	// IsGlob reports whether Glob actually contains glob syntax (false
	// means the whole pattern is a plain literal path).
	IsGlob bool

	// Negated reports whether the pattern had a leading "!" negation
	// marker; Base and Glob describe the pattern with that marker removed.
	Negated bool
}

// Scan returns pattern's base/glob split, mirroring picomatch.scan. It is
// useful for directory walking: a caller can list only Base and match Glob
// against entries relative to it, instead of walking every directory in the
// tree.
//
// The first magic character (any of "*?[]{}()!+@|" not escaped with "\")
// marks where glob syntax begins; since "[" is itself magic, nothing before
// the first magic character can be inside a bracket expression, so no
// separate bracket-tracking pass is needed to find it.
func Scan(pattern string) ScanResult {
	negated := false
	p := pattern
	if strings.HasPrefix(p, "!") && !strings.HasPrefix(p, "!(") {
		negated = true
		p = p[1:]
	}

	rs := []rune(p)
	magicIdx := firstMagicIndex(rs)
	if magicIdx < 0 {
		return ScanResult{Base: pattern, Glob: "", IsGlob: false, Negated: negated}
	}

	lastSlash := -1
	for i := 0; i < magicIdx; i++ {
		if rs[i] == '/' {
			lastSlash = i
		}
	}

	var base, glob string
	if lastSlash < 0 {
		base = "."
		glob = p
	} else {
		base = string(rs[:lastSlash+1])
		glob = string(rs[lastSlash+1:])
	}
	return ScanResult{Base: base, Glob: glob, IsGlob: true, Negated: negated}
}

func firstMagicIndex(rs []rune) int {
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '\\' && i+1 < len(rs) {
			i++
			continue
		}
		if isMagicRune(c) {
			return i
		}
	}
	return -1
}

func isMagicRune(c rune) bool {
	switch c {
	case '*', '?', '[', ']', '{', '}', '(', ')', '!', '+', '@', '|':
		return true
	}
	return false
}
