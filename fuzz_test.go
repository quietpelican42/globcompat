package globcompat

import "testing"

// FuzzMatch exercises Compile+Match against arbitrary pattern/path pairs.
// It never asserts a particular match result (there is no oracle available
// inside a fuzz corpus run); it only asserts that Compile+Match do not
// panic and that Compile either succeeds or returns a well-formed error,
// for every option combination, including the backtracking path (forced by
// including "!(" in the seed corpus).
func FuzzMatch(f *testing.F) {
	seeds := []string{
		"*.js", "**/*.ts", "a/**/b", "!(foo)", "a!(b)c", "@(a|b|c)",
		"+(ab)*.js", "*(x)?(y)", "a{b,c}d", "a{1..10}b", "a{01..10..2}b",
		"[[:alpha:]]*.md", "[!abc]*", "!**", "!*.md", "**/!(*.min).js",
		"a\\{b\\}c", "{a,{b,c}}", "a/[b-d]/**", "", "*", "**", "!(!(a))",
	}
	paths := []string{
		"a.js", "a/b/c.ts", ".hidden", "a/.b/c", "foo", "bar", "a/b",
		"", "/", "a/", "a\\b\\c", "日本語.md", "a(b)c", "a[b]c",
	}
	for _, s := range seeds {
		for _, p := range paths {
			f.Add(s, p, true, false, false, false, false, false, false, false, false)
		}
	}

	f.Fuzz(func(t *testing.T, pattern, path string, dot, nocase, nobrace, noextglob, noglobstar, basename, contains, windows, nonegate bool) {
		if len(pattern) > maxPatternLength {
			return
		}
		opts := Options{
			Dot: dot, NoCase: nocase, NoBrace: nobrace, NoExtglob: noextglob,
			NoGlobstar: noglobstar, Basename: basename, Contains: contains,
			Windows: windows, NoNegate: nonegate,
		}
		m, err := Compile(pattern, opts)
		if err != nil {
			return
		}
		_ = m.Match(path)
		_ = m.Match(path) // calling twice must be stable/side-effect free
	})
}
