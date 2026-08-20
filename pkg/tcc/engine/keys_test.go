package engine

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// "/" IS THE ONE SYMBOL A KEY MAY NOT CONTAIN, because it is what separates one level
// of the compiled object from the next. A key that spells its own hierarchy reads as a
// path everywhere it is printed and is one opaque leaf everywhere it is stored.
//
// Refused at construction, so a vocabulary that would compile into an unmergeable shape
// is a build failure rather than a config nobody can merge.
func refuses(t *testing.T, what string, build func()) {
	t.Helper()
	defer func() {
		r := recover()
		assert.Assert(t, r != nil, "%s was accepted", what)
		msg, _ := r.(string)
		assert.Assert(t, strings.Contains(msg, "not a usable key"), "wrong panic: %v", r)
	}()
	build()
}

func TestAKeyMayNotSpellItsOwnPath(t *testing.T) {
	refuses(t, "a slashed attribute", func() { String("execution/call") })
	refuses(t, "a slashed tag", func() { String("call", Tag("execution/call")) })
	refuses(t, "a slashed group", func() { DefineGroup("route/handler") })
	refuses(t, "a slashed list", func() { DefineList("route/steps", nil) })
	refuses(t, "a slashed leaf", func() { Leaf("a/b") })

	// the message says where nesting actually comes from
	defer func() {
		msg, _ := recover().(string)
		assert.Assert(t, strings.Contains(msg, "Path(...)"), "no hint toward Path: %q", msg)
	}()
	String("execution/call")
}

func TestAKeyIsAName(t *testing.T) {
	refuses(t, "a leading digit", func() { String("2fast") })
	refuses(t, "a dot", func() { String("execution.call") })
	refuses(t, "a space", func() { String("max attempts") })
	refuses(t, "empty", func() { String("") })

	// what both vocabularies actually use: names, and compound names with "-"
	for _, ok := range []string{"id", "max-attempts", "useRegex", "webSocket", "_x", "route-handler-name"} {
		String(ok) // must not panic
	}
}

// Nesting is declared, and this is the shape it is declared in.
func TestNestingIsDeclaredWithPath(t *testing.T) {
	a := String("execution-call", Path("execution", "call"))
	assert.Equal(t, a.Name, "execution-call")
	assert.Equal(t, len(a.Path), 2)
}
