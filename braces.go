package globcompat

import (
	"fmt"
	"strconv"
	"strings"
)

// maxBraceExpansions bounds the number of strings Braces will produce for a
// single pattern, mirroring the "braces" npm package's own expansion-limit
// guard against exponential blowup from deeply nested or combined brace
// sets (e.g. "{a,b}{c,d}{e,f}{g,h}{i,j}{k,l}...").
const maxBraceExpansions = 65536

// Braces expands brace sets and ranges the way the "braces" npm package
// does: comma-separated alternatives ("{a,b,c}"), numeric ranges with an
// optional step ("{1..10}", "{01..10..2}"), zero-padded when either bound is
// written with a leading zero, single-character alphabetic ranges
// ("{a..z}"), and arbitrary nesting ("a/{b,c{d,e}}/f"). "\{" and "\}" are
// literal. It returns an error if the expansion would exceed
// maxBraceExpansions alternatives.
func Braces(pattern string) ([]string, error) {
	return expandBraces(pattern)
}

func expandBraces(s string) ([]string, error) {
	rs := []rune(s)
	idx := findUnescapedBrace(rs)
	if idx < 0 {
		return []string{unescapeBraces(s)}, nil
	}
	end, err := matchingBrace(rs, idx)
	if err != nil {
		return nil, err
	}
	prefix := string(rs[:idx])
	body := string(rs[idx+1 : end])
	suffix := string(rs[end+1:])

	altsRaw, isRange := splitBraceBody(body)

	var bodyExpansions []string
	if isRange {
		vals, err := expandRange(body)
		if err != nil {
			return nil, err
		}
		bodyExpansions = vals
	} else {
		for _, alt := range altsRaw {
			exp, err := expandBraces(alt)
			if err != nil {
				return nil, err
			}
			bodyExpansions = append(bodyExpansions, exp...)
			if len(bodyExpansions) > maxBraceExpansions {
				return nil, fmt.Errorf("globcompat: brace expansion exceeds %d alternatives", maxBraceExpansions)
			}
		}
	}

	suffixExpansions, err := expandBraces(suffix)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, b := range bodyExpansions {
		for _, t := range suffixExpansions {
			out = append(out, prefix+b+t)
			if len(out) > maxBraceExpansions {
				return nil, fmt.Errorf("globcompat: brace expansion exceeds %d alternatives", maxBraceExpansions)
			}
		}
	}
	return out, nil
}

// findUnescapedBrace returns the rune index of the first unescaped "{" in
// rs, or -1 if there is none.
func findUnescapedBrace(rs []rune) int {
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			i++
			continue
		}
		if rs[i] == '{' {
			return i
		}
	}
	return -1
}

// matchingBrace returns the rune index of the "}" matching the "{" at idx,
// honoring nesting.
func matchingBrace(rs []rune, idx int) (int, error) {
	depth := 0
	for i := idx; i < len(rs); i++ {
		switch {
		case rs[i] == '\\' && i+1 < len(rs):
			i++
		case rs[i] == '{':
			depth++
		case rs[i] == '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("globcompat: unbalanced brace expression in %q", string(rs))
}

// splitBraceBody splits a brace body on top-level commas. isRange is true
// when the body has no top-level comma and looks like "a..b" or "a..b..c".
func splitBraceBody(body string) (parts []string, isRange bool) {
	rs := []rune(body)
	depth := 0
	var cur []rune
	hasComma := false
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '\\' && i+1 < len(rs):
			cur = append(cur, rs[i], rs[i+1])
			i++
		case rs[i] == '{':
			depth++
			cur = append(cur, rs[i])
		case rs[i] == '}':
			depth--
			cur = append(cur, rs[i])
		case rs[i] == ',' && depth == 0:
			hasComma = true
			parts = append(parts, string(cur))
			cur = nil
		default:
			cur = append(cur, rs[i])
		}
	}
	parts = append(parts, string(cur))
	if !hasComma && looksLikeRange(body) {
		return nil, true
	}
	return parts, false
}

func looksLikeRange(body string) bool {
	return strings.Count(body, "..") >= 1 && !strings.Contains(body, ",")
}

// expandRange expands a numeric or single-letter range body such as
// "1..10", "01..10..2", or "a..e".
func expandRange(body string) ([]string, error) {
	parts := strings.Split(body, "..")
	if len(parts) < 2 || len(parts) > 3 {
		return []string{"{" + body + "}"}, nil // not actually a range; treat literally
	}
	step := 1
	if len(parts) == 3 {
		n, err := strconv.Atoi(parts[2])
		if err != nil || n == 0 {
			return []string{"{" + body + "}"}, nil
		}
		if n < 0 {
			n = -n
		}
		step = n
	}

	if isAlphaEndpoint(parts[0]) && isAlphaEndpoint(parts[1]) {
		lo, hi := []rune(parts[0])[0], []rune(parts[1])[0]
		var out []string
		if lo <= hi {
			for r := lo; r <= hi; r += rune(step) {
				out = append(out, string(r))
			}
		} else {
			for r := lo; r >= hi; r -= rune(step) {
				out = append(out, string(r))
			}
		}
		return out, nil
	}

	loN, errA := strconv.Atoi(parts[0])
	hiN, errB := strconv.Atoi(parts[1])
	if errA != nil || errB != nil {
		return []string{"{" + body + "}"}, nil
	}
	width := 0
	if hasLeadingZero(parts[0]) || hasLeadingZero(parts[1]) {
		w0, w1 := digitWidth(parts[0]), digitWidth(parts[1])
		if w0 > w1 {
			width = w0
		} else {
			width = w1
		}
	}
	var out []string
	emit := func(n int) {
		out = append(out, padInt(n, width))
	}
	if loN <= hiN {
		for n := loN; n <= hiN; n += step {
			emit(n)
		}
	} else {
		for n := loN; n >= hiN; n -= step {
			emit(n)
		}
	}
	return out, nil
}

func isAlphaEndpoint(s string) bool {
	return len(s) == 1 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z'))
}

func hasLeadingZero(s string) bool {
	s = strings.TrimPrefix(s, "-")
	return len(s) > 1 && s[0] == '0'
}

func digitWidth(s string) int {
	return len(strings.TrimPrefix(s, "-"))
}

func padInt(n, width int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for len(s) < width {
		s = "0" + s
	}
	if neg {
		s = "-" + s
	}
	return s
}

func unescapeBraces(s string) string {
	return strings.NewReplacer(`\{`, `{`, `\}`, `}`, `\,`, `,`).Replace(s)
}
