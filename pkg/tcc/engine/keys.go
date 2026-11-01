package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// A key may not contain "/": the compiled configuration is an object and "/"
// separates its levels, so a slashed key reads as hierarchy but is stored as one
// leaf a CRDT can only replace whole. Nesting is declared with Path. A Key(...)
// segment is held to a variable name; a bare string name, an identifier, only
// to the "/" ban. Refused at construction.
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
