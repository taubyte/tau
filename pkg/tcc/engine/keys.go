package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// A KEY MAY NOT CONTAIN "/".
//
// The compiled configuration is an OBJECT, and "/" is what separates one level of it
// from the next — in a register path, in a lowering's `At(path…)`, in every walk over the
// tree. A key with a slash in it is a key pretending to be a path: it reads as
// hierarchy everywhere it is printed and is one opaque leaf everywhere it is stored,
// so a CRDT can only replace it whole and two writers touching different halves of it
// collide over the same cell.
//
// This is the whole reason `Path(...)` exists. Nesting is declared with segments —
// `Path("execution", "call")` — and the engine builds the levels. A vocabulary that
// wants `execution` to hold `call` says so with Path and never by spelling a key
// "execution/call".
//
// A KEY IS A VARIABLE NAME, the same rule a resource's name follows — `dead_letter`,
// not `dead-letter`. Held strictly on the segments of a `Key(...)`, which is what the
// compiled object is addressed by.
//
// A bare string name is held only to the "/" ban. It is an IDENTIFIER — what ShowWhen
// and KindFrom call the attribute — and it is the compiled key only when nothing else
// says otherwise.
//
// REFUSED AT CONSTRUCTION, loudly, like DefineList's unsupported terms: a schema that
// cannot express a legal object is a build failure, not a config that compiles into a
// shape nothing can merge.
var keyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// mustNotSpellAPath refuses the one character that separates levels of the object.
func mustNotSpellAPath(what, key string) {
	if key != "" && !strings.Contains(key, "/") {
		return
	}
	panic(fmt.Sprintf("%s %q is not a usable key: a key may not be empty or contain \"/\" — that separates levels of the compiled object, so declare the nesting with Path(...) instead", what, key))
}

// mustBeKey refuses a key that cannot be one level of an object.
func mustBeKey(what, key string) {
	if keyRegex.MatchString(key) {
		return
	}
	hint := ""
	for _, c := range key {
		if c == '/' {
			hint = ` — "/" separates levels of the compiled object, so declare the nesting with Path(...) instead`
			break
		}
	}
	panic(fmt.Sprintf("%s %q is not a usable key: it must match %s%s", what, key, keyRegex.String(), hint))
}
