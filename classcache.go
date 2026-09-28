package globcompat

import (
	"regexp"
	"sync"
)

// classCache memoizes the single-rune-testing regexp built for each bracket
// expression, keyed by its rendered RE2 form. It is only used by the
// backtracking matcher (bt.go); the RE2 fast path inlines renderClass
// directly into the compiled pattern.
var classCache sync.Map // map[string]*regexp.Regexp

func compileClassOnce(cls classSpec) *regexp.Regexp {
	key := renderClass(cls)
	if v, ok := classCache.Load(key); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile("^" + key + "$")
	if err != nil {
		classCache.Store(key, (*regexp.Regexp)(nil))
		return nil
	}
	classCache.Store(key, re)
	return re
}
