// Package regex compiles a pattern once and keeps it, for config-supplied patterns
// matched per request, message or item: compile-and-match costs about 20x a match
// against a compiled pattern.
//
// The working set is the distinct matchers deployed, bounded anyway: over the cap it
// stops remembering rather than stops working. It is not for patterns built from
// inbound text, which would be an unbounded key space.
package regex

import (
	"regexp"
	"sync"
)

// max is the number of distinct patterns remembered.
const max = 4096

var (
	mu    sync.RWMutex
	cache = map[string]*regexp.Regexp{}
)

// Compile is regexp.Compile, memoized by pattern. The returned *regexp.Regexp is
// shared; a caller that wants Longest must compile its own, since that mutates.
func Compile(pattern string) (*regexp.Regexp, error) {
	mu.RLock()
	re, ok := cache[pattern]
	mu.RUnlock()
	if ok {
		return re, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		// A pattern that does not compile is not remembered, so one project's typo cannot
		// evict another's matcher.
		return nil, err
	}

	mu.Lock()
	if len(cache) < max {
		cache[pattern] = re
	}
	mu.Unlock()
	return re, nil
}

// MatchString is regexp.MatchString with the compile kept.
func MatchString(pattern, s string) (bool, error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}
