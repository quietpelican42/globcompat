package globcompat

// Options mirrors a subset of picomatch's option set (see
// https://github.com/micromatch/picomatch#options). Only the options
// documented on each field are implemented; anything else picomatch supports
// is out of scope for this package (see README Limitations).
type Options struct {
	// Dot allows patterns to match filenames or path segments that begin
	// with a dot. Off by default, matching picomatch: "*" does not match
	// ".git", "**" does not descend into dot-directories, and "?" does not
	// match a leading ".".
	Dot bool

	// NoCase makes matching case-insensitive.
	NoCase bool

	// NoBrace disables brace-set/range expansion ("{a,b}", "{1..10}").
	// Braces are left as literal characters.
	NoBrace bool

	// NoExtglob disables extglob parsing ("@(a|b)", "!(x)", "+(x)", "*(x)",
	// "?(x)"). The characters are treated literally.
	NoExtglob bool

	// NoGlobstar disables "**" descending through multiple path segments;
	// it is treated as an ordinary "*" instead (still segment-bound), except a
	// leading "**/" which becomes an optional single segment, as in picomatch.
	NoGlobstar bool

	// Basename (picomatch's matchBase) matches the pattern against
	// only the last path segment of the candidate, ignoring any directory
	// prefix, even when the pattern itself contains a slash (verified: picomatch
	// with basename:true rejects isMatch('dir/a.go', 'dir/*')).
	Basename bool

	// Contains matches the pattern anywhere inside the path instead of
	// anchoring the match to the whole string.
	Contains bool

	// Posix enables POSIX bracket expression classes such as
	// "[[:alpha:]]" inside character classes. Go's regexp already
	// understands these natively, so this package honours them whether or
	// not Posix is set; the field exists for API parity with picomatch and
	// to make the intent explicit in caller code.
	Posix bool

	// Windows treats "\" in the candidate path as a path separator
	// equivalent to "/" before matching. It does not change how "\" is
	// interpreted inside the pattern itself (there it is always the glob
	// escape character, as in picomatch).
	Windows bool

	// NoNegate disables leading "!" negation handling in MatchAny and
	// IsMatch; a pattern that starts with "!" is then matched literally
	// instead of being treated as an exclusion.
	NoNegate bool
}
