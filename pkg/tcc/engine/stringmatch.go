package engine

import "fmt"

type StringMatcher interface {
	Match(s string) bool
	String() string
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

func (e *either) String() string {
	return fmt.Sprintf("Either(%v)", e.values)
}

func Either(values ...string) StringMatcher {
	return &either{values: values}
}
