package engine

import "fmt"

type StringMatcher interface {
	Match(s string) bool
	String() string
}

// FiniteMatcher is a StringMatcher whose alternatives are known and
// enumerable, so a caller can expand a branching path into its concrete
// locations rather than resolving it against a document.
type FiniteMatcher interface {
	StringMatcher
	Values() []string
}

// SuffixMatcher is a matcher that can spell ITSELF suffixed with one value.
//
// A value that varies is written at `<key>_<value>` beside the key. When the key
// is one the document CHOSE out of a set rather than one the schema named, the
// suffix lands on whichever alternative was written — so the schema has to admit
// every alternative it could be, suffixed. Only the matcher knows what it admits,
// so only the matcher can say what its suffixed spelling is.
//
// StringMatchAll deliberately does not implement this: it admits keys nobody can
// enumerate, so there is no set to suffix.
type SuffixMatcher interface {
	StringMatcher
	Suffixed(value string) StringMatcher
}

type StringMatchAll struct{}

func (StringMatchAll) Match(string) bool {
	return true
}

func (StringMatchAll) String() string {
	return "StringMatchAll"
}

func All() StringMatcher {
	return StringMatchAll{}
}

type either struct {
	values []string
}

func (e *either) Match(s string) bool {
	for _, m := range e.values {
		if s == m {
			return true
		}
	}
	return false
}

// Values reports the alternatives this matcher admits. A FINITE matcher can be
// enumerated, which is what lets a consumer outside this package address a
// branching location (object/size or streaming/size) instead of giving up on
// it. StringMatchAll deliberately does not implement this.
func (e *either) Values() []string { return append([]string(nil), e.values...) }

// Suffixed is this switch spelled for one value: every alternative it admits,
// suffixed. The unsuffixed alternative still names the choice — an attribute that
// captures WHICH alternative was taken does not itself vary — so this matches the
// override beside it and never instead of it.
func (e *either) Suffixed(value string) StringMatcher {
	out := make([]string, 0, len(e.values))
	for _, v := range e.values {
		out = append(out, v+"_"+value)
	}
	return &either{values: out}
}

func (e *either) String() string {
	return fmt.Sprintf("Either(%v)", e.values)
}

func Either(values ...string) StringMatcher {
	return &either{values: values}
}
