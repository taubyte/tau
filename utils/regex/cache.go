// Package regex compiles a pattern once and keeps it, for config-supplied patterns
// matched per request, message or item: compile-and-match costs about 20x a match
// against a compiled pattern. Whoever matches holds the Cache.
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

// Cache keeps compiled patterns. Its zero value is ready, and it is safe for
// concurrent use.
type Cache struct {
	mu   sync.RWMutex
	kept map[string]*regexp.Regexp
}

// Compile is regexp.Compile, memoized by pattern. The returned *regexp.Regexp is
// shared; a caller that wants Longest must compile its own, since that mutates.
func (c *Cache) Compile(pattern string) (*regexp.Regexp, error) {
	c.mu.RLock()
	re, ok := c.kept[pattern]
	c.mu.RUnlock()
	if ok {
		return re, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		// A pattern that does not compile is not remembered, so one project's typo cannot
		// evict another's matcher.
		return nil, err
	}

	c.mu.Lock()
	if c.kept == nil {
		c.kept = map[string]*regexp.Regexp{}
	}
	if len(c.kept) < max {
		c.kept[pattern] = re
	}
	c.mu.Unlock()
	return re, nil
}

// MatchString is regexp.MatchString with the compile kept.
func (c *Cache) MatchString(pattern, s string) (bool, error) {
	re, err := c.Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}
