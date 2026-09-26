// Package regex compiles a pattern once and keeps it.
//
// # Why this exists
//
// Six places in this tree matched a CONFIG-SUPPLIED pattern with regexp.Match,
// which is the one-shot form: it compiles the pattern, matches, and throws the
// compiled program away. Every one of them sat on a path that runs per request,
// per message or per stored item, so the compile was paid again for every call
// -- a gateway recompiled every candidate route's pattern on every HTTP request.
//
// Measured on the pattern shapes this tree actually matches: 2867ns to
// compile-and-match against 129ns to match something already compiled. The
// engine was never the problem; throwing the compile away was.
//
// # What it is bounded by
//
// Patterns come from a project's own configuration, so the working set is the
// number of distinct matchers deployed to a node, not the number of requests.
// The cap is here anyway, because "configuration cannot grow without bound" is
// an assumption about deployment and not a property of this code: over the cap
// it stops REMEMBERING rather than stops working, which degrades to exactly the
// behaviour every one of these call sites had before.
//
// SO IT IS NOT FOR A PATTERN BUILT FROM INBOUND TEXT. A caller that splices a
// request into a pattern would be handing this an unbounded key space, and the
// cap would turn into a cache that never hits. Those callers compile their own.
package regex

import (
	"regexp"
	"sync"
)

// max is the number of distinct patterns remembered.
//
// It is generous on purpose: a compiled pattern is small, and the point of the
// number is to have one, not to be tight. A node serving more distinct matchers
// than this is past the point where the cache was the interesting question.
const max = 4096

var (
	mu    sync.RWMutex
	cache = map[string]*regexp.Regexp{}
)

// Compile is regexp.Compile, memoized by pattern.
//
// The returned *regexp.Regexp is SHARED, which is safe because a compiled
// regexp is safe for concurrent use and nothing here mutates one. A caller that
// wants to call Longest must compile its own -- that mutates.
func Compile(pattern string) (*regexp.Regexp, error) {
	mu.RLock()
	re, ok := cache[pattern]
	mu.RUnlock()
	if ok {
		return re, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		// A pattern that does not compile is not remembered: it costs a compile
		// each time it is asked for, and the alternative is letting a typo in
		// one project's config evict another project's working matcher.
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
//
// It is a drop-in for the one-shot form at every call site that had one, so the
// error a caller already handled means the same thing it did: the pattern is
// not a pattern.
func MatchString(pattern, s string) (bool, error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}
