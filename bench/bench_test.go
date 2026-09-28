// Package bench measures globcompat against doublestar (the closest
// maintained Go glob library covering globstar patterns) on the same
// pattern/path pairs. It is a separate module so the benchmark's dependency
// on doublestar never becomes a dependency of globcompat itself, which is
// stdlib-only.
//
// Run with: go test -bench=. -benchmem ./...
package bench

import (
	"testing"

	gc "github.com/__GH_OWNER__/globcompat"
	"github.com/bmatcuk/doublestar/v4"
)

var cases = []struct {
	name, pattern, path string
}{
	{"literal", "src/index.js", "src/index.js"},
	{"star", "src/*.js", "src/index.js"},
	{"globstar", "src/**/*.ts", "src/components/ui/button/index.ts"},
	{"brace", "src/**/*.{ts,tsx,js,jsx}", "src/components/ui/Button.tsx"},
	{"extglob", "src/**/!(*.test).ts", "src/components/button.ts"},
	{"nomatch", "src/**/*.go", "src/components/ui/button/index.ts"},
}

func BenchmarkGlobcompat(b *testing.B) {
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			m, err := gc.Compile(c.pattern, gc.Options{})
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Match(c.path)
			}
		})
	}
}

func BenchmarkDoublestar(b *testing.B) {
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			if _, err := doublestar.Match(c.pattern, c.path); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				doublestar.Match(c.pattern, c.path)
			}
		})
	}
}
