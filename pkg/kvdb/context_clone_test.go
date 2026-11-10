package kvdb

import (
	"slices"
	"testing"
)

// A CONTEXT CHANGED AFTER IT IS CLONED leaves the clone as it was: the two share
// entries until one writes, and the writer copies first, whichever it is.
func TestACloneAndItsParentChangeApart(t *testing.T) {
	parent := newCausalContext()
	parent.add(dot{replica: "r", counter: 1}, 0, 1, 100)
	clone := parent.clone()

	parent.add(dot{replica: "r", counter: 2}, 1, 2, 100)
	if got := clone.inc["r"].spans; !slices.Equal(got, []span{{lo: 0, hi: 1}}) {
		t.Fatalf("the parent's write reached its clone: %v", got)
	}

	clone.add(dot{replica: "r", counter: 5}, 4, 5, 100)
	if got := parent.inc["r"].spans; !slices.Equal(got, []span{{lo: 0, hi: 2}}) {
		t.Fatalf("the clone's write reached its parent: %v", got)
	}
}
