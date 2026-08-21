package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// A KEY MAY NOT CONTAIN "/".
//
// The compiled configuration is an OBJECT, and "/" is what separates one level of it
// from the next — in a TNS path, in a lowering's `At(path…)`, in every walk over the
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
// A KEY SHOULD BE A VARIABLE NAME, the same rule a resource's name follows —
// `dead_letter`, not `dead-letter`. The regex below still admits "-" because 84 of v2's
// attributes are a Path spelled into their name (`engine-kind` for
// Path("engine", Either(...))) and each dissolves into segments as it is converted to
// Key(...). Tighten it to varNameRegex the moment the last one is gone; the compound
// leaves that needed it — max_attempts, on_error, on_overlap, visibility_timeout,
// zone_label, dead_letter, image_pull_secret — are already renamed.
//
// The one exemption is Tag, which exists to say the compiled key differs from what this
// vocabulary would call it — including differing from a name, because the format it is
// matching is somebody else's. v1's `cert-file` and `repository-id` are that. A Tag is
// still refused a "/": that separator is structural and belongs to nobody.
//
// REFUSED AT CONSTRUCTION, loudly, like DefineList's unsupported terms: a schema that
// cannot express a legal object is a build failure, not a config that compiles into a
// shape nothing can merge.
var keyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// mustNotSpellAPath refuses the one character that separates levels of the object. It is
// all a Tag is held to: what it names is a foreign wire format, not this vocabulary.
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
