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
	refuses(t, "a leading digit", func() { String(Key("x", "2fast")) })
	refuses(t, "a dot", func() { String(Key("execution", "call.x")) })
	refuses(t, "a space", func() { String(Key("max attempts")) })
	refuses(t, "empty", func() { String("") })

	// a bare name is an IDENTIFIER, held only to the "/" ban
	String("git-provider")

	// a KEY segment is a name, strictly: it addresses a level of the compiled object
	refuses(t, "a dashed key segment", func() { String(Key("execution", "max-attempts")) })

	// a key is a NAME, the same rule a resource's name follows: compound ones join
	// with "_", and a path is declared with Key(...) rather than spelled into one
	for _, ok := range []string{"id", "max_attempts", "useRegex", "webSocket", "_x"} {
		String(ok) // must not panic
	}
	String(Key("route", "handler", "name")) // and this is how a nested one is said
}

// NESTING IS DECLARED, and the key IS the nesting. The identity is those segments
// joined — the engine writes that "/", a vocabulary never does — and where the value is
// AUTHORED defaults to the same place, so Path is declared only when the two differ.
func TestTheKeyIsTheNesting(t *testing.T) {
	a := String(Key("execution", "call"))
	assert.Equal(t, a.Name, "execution/call")
	assert.DeepEqual(t, a.Segments, []string{"execution", "call"})
	assert.Equal(t, len(a.Path), 2, "authored where it compiles, unless Path says otherwise")

	// read from execution.call, compile onto a flat `call`
	b := String("call", Path("execution", "call"))
	assert.Equal(t, b.Name, "call")
	assert.Equal(t, len(b.Segments), 0)
	assert.Equal(t, len(b.Path), 2)
}
