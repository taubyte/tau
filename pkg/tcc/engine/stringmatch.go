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

func (e *either) String() string {
	return fmt.Sprintf("Either(%v)", e.values)
}

func Either(values ...string) StringMatcher {
	return &either{values: values}
}
