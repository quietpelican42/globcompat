package globcompat

import "testing"

func TestSmoke(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		opts    Options
		want    bool
	}{
		{"*.js", "a.js", Options{}, true},
		{"*.js", "a.ts", Options{}, false},
		{"*.js", ".a.js", Options{}, false},
		{"*.js", ".a.js", Options{Dot: true}, true},
		{"**/*.js", "a/b/c.js", Options{}, true},
		{"a/**/b", "a/b", Options{}, true},
		{"a/**/b", "a/x/y/b", Options{}, true},
		{"a/**", "a/x/y", Options{}, true},
		{"**", "x/y", Options{}, true},
		{"**", ".x", Options{}, false},
		{"a?c", "abc", Options{}, true},
		{"a[bc]d", "abd", Options{}, true},
		{"a[!bc]d", "axd", Options{}, true},
		{"a[[:digit:]]d", "a5d", Options{}, true},
		{"@(foo|bar)", "foo", Options{}, true},
		{"@(foo|bar)", "baz", Options{}, false},
		{"!(foo)", "bar", Options{}, true},
		{"!(foo)", "foo", Options{}, false},
		{"a!(b)c", "aac", Options{}, true},
		{"a!(b)c", "abc", Options{}, false},
		{"+(ab)", "ababab", Options{}, true},
		{"*(ab)", "", Options{}, true},
		{"?(ab)", "ab", Options{}, true},
		{"*.{js,ts}", "a.ts", Options{}, true},
		{"*.{js,ts}", "a.go", Options{}, false},
		{"a{1..3}b", "a2b", Options{}, true},
		{"a{01..10..2}b", "a05b", Options{}, true},
	}
	for _, c := range cases {
		got, err := Match(c.pattern, c.path, c.opts)
		if err != nil {
			t.Errorf("Match(%q, %q, %+v) error: %v", c.pattern, c.path, c.opts, err)
			continue
		}
		if got != c.want {
			t.Errorf("Match(%q, %q, %+v) = %v, want %v", c.pattern, c.path, c.opts, got, c.want)
		}
	}
}

func TestBracesSmoke(t *testing.T) {
	got, err := Braces("a{1..3}b")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a1b", "a2b", "a3b"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v want %v", got, want)
			break
		}
	}
}

func TestScanSmoke(t *testing.T) {
	r := Scan("src/**/*.ts")
	if r.Base != "src/" || r.Glob != "**/*.ts" || !r.IsGlob {
		t.Errorf("Scan = %+v", r)
	}
	r2 := Scan("a/b/c.txt")
	if r2.IsGlob || r2.Base != "a/b/c.txt" {
		t.Errorf("Scan(no glob) = %+v", r2)
	}
}
