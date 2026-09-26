package engine

import (
	"regexp"
	"testing"
)

// matches is what the store does with this pattern: kvdb's FilterKeyRegEx calls
// MatchString, which is unanchored. Spelled out here rather than imported so the
// test states the property the pattern must hold under, not the implementation
// that happens to hold it today.
func matches(t *testing.T, pattern, key string) bool {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("pattern %q does not compile: %v", pattern, err)
	}
	return re.MatchString(key)
}

// TestALOOKUPISROOTED. The filter is unanchored, so before the `^` a pattern was
// satisfied by any key that CONTAINED it -- and a stored path is free to carry
// text that looks like the store's own prefix. A lookup that answers with a key
// from somewhere else in the tree is not a scoping subtlety, it is a wrong
// answer.
func TestARegExLookupOnlyMatchesFromTheRoot(t *testing.T) {
	pattern := regExkeyFromPath([]string{"projects", "shop"})

	if !matches(t, pattern, "/tns/v1/projects/shop/functions/api") {
		t.Fatal("a real key under the looked-up path did not match")
	}

	// The key this test exists for: the prefix text appears, but not at the root.
	buried := "/tns/v1/projects/other/assets/tns/v1/projects/shop/leaked"
	if matches(t, pattern, buried) {
		t.Fatalf("a lookup answered with a key that merely contains the path: %s", buried)
	}
}

// TestTHEPREFIXISLITERAL. Prefix names path segments, not patterns. It is
// alphanumeric today so quoting changes nothing -- which is exactly why the
// quoting has to be asserted rather than eyeballed: the day a prefix segment
// carries a `.`, an unquoted one silently starts matching any character.
func TestTheStorePrefixIsMatchedLiterally(t *testing.T) {
	defer func(saved []string) { Prefix = saved }(Prefix)
	Prefix = []string{"tns", "v1.2"}

	pattern := regExkeyFromPath([]string{"projects"})

	if !matches(t, pattern, "/tns/v1.2/projects/shop") {
		t.Fatal("the literal prefix did not match its own key")
	}
	if matches(t, pattern, "/tns/v1X2/projects/shop") {
		t.Fatal("an unquoted `.` in the prefix matched any character")
	}
}

// TestTHECALLERSSEGMENTSSTAYPATTERNS -- the whole point of the RegEx flag, and
// the thing a blanket QuoteMeta here would have quietly removed. taucorder's
// regex lookup sends fragments on purpose.
func TestTheCallersSegmentsAreStillPatterns(t *testing.T) {
	pattern := regExkeyFromPath([]string{"projects", "[a-z]+", "functions"})

	if !matches(t, pattern, "/tns/v1/projects/shop/functions/api") {
		t.Fatal("a caller's pattern segment stopped being a pattern")
	}
	if matches(t, pattern, "/tns/v1/projects/SHOP9/functions/api") {
		t.Fatal("the pattern matched something it does not describe")
	}
}

// Prefix is a package-level slice and these build on top of it; append must not
// write into its array or one lookup would rewrite another's.
func TestBuildingAPatternDoesNotMutateThePrefix(t *testing.T) {
	before := append([]string{}, Prefix...)

	regExkeyFromPath([]string{"a", "b", "c"})
	keyFromPath([]string{"d", "e", "f"})

	if len(Prefix) != len(before) {
		t.Fatalf("Prefix changed length: %v -> %v", before, Prefix)
	}
	for i := range before {
		if Prefix[i] != before[i] {
			t.Fatalf("Prefix was written through: %v -> %v", before, Prefix)
		}
	}
}
