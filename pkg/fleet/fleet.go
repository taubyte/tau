// Package fleet is what an operator declares about a cloud as a whole, rather
// than about any project in it.
//
// # Why it is a published document and not each node's config
//
// Everything here is a number every node must compute with IDENTICALLY. A
// replication floor read from per-node config makes two nodes disagree about
// who should hold a database — permanently, unlike every other placement input,
// which heals as views converge — and makes what an operator is billed for
// depend on which node is asked.
//
// So it is published the way a project is: one content-addressed object, one
// monotone revision, read by every node from the register it already polls. A
// node behind is behind by a REVISION it can see and name, which is the whole
// difference between a disagreement that resolves and one that does not.
package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrMalformed is a document that cannot be read. It is an error and never a
// default: a fleet whose declaration is corrupt has not said nothing, and
// treating it as silence would have every node quietly pick its own floor —
// the disagreement this package exists to end.
var ErrMalformed = errors.New("fleet: this document cannot be read")

// Fleet is the operator's statement about the cloud.
type Fleet struct {
	// Replication is the floor on how many nodes hold each database, in EACH
	// region it is pinned to. Zero means the reader's default.
	//
	// AN OPERATOR'S NUMBER AND NEVER AN AUTHOR'S: it is how many machines this
	// cloud can afford to lose, and an author writing it per database would be
	// spending somebody else's hardware. What an author controls is the regions
	// — which multiply it.
	Replication int `json:"replication,omitempty"`
}

// Encode renders a fleet document. JSON because it is read by operators as
// often as by nodes, and it is a handful of fields that will grow one at a time.
func Encode(f Fleet) ([]byte, error) { return json.Marshal(f) }

// Decode reads one.
func Decode(b []byte) (Fleet, error) {
	var out Fleet
	if err := json.Unmarshal(b, &out); err != nil {
		return Fleet{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if out.Replication < 0 {
		return Fleet{}, fmt.Errorf("%w: replication %d", ErrMalformed, out.Replication)
	}
	return out, nil
}
