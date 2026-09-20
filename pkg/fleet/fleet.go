// Package fleet is what an operator declares about a cloud as a whole, rather
// than about any project in it.
//
// # Why it is a published document and not each node's config
//
// It carries two kinds of number, and each has its own reason for being here.
//
// THE FIRST IS A NUMBER EVERY NODE MUST COMPUTE WITH IDENTICALLY. A replication
// floor read from per-node config makes two nodes disagree about who should
// hold a database — permanently, unlike every other placement input, which
// heals as views converge — and makes what an operator is billed for depend on
// which node is asked.
//
// THE SECOND IS A NUMBER THAT STATES WHAT THIS CLOUD PROMISES. A durability
// barrier is not a quantity two nodes compute a shared answer from; each waits
// for its own count. But a guarantee that varies per node is not a guarantee
// the cloud can state, and it is silently unenforceable: nothing ever compares
// two nodes' barriers, so a node left behind at a bring-up value acks writes
// alone forever and no test, peer or caller can see it.
//
// So both are published the way a project is: one content-addressed object, one
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

	// Storage is what the cloud promises about objects. It is NOT Replication
	// under another name and must never be collapsed into it: that floor is
	// per pinned region and multiplies by them, this one is per shard over one
	// global member set — and the two buy different things with different
	// money, kv rows against object bytes.
	//
	// omitzero AND NOT omitempty, because omitempty has never omitted a struct
	// — a cloud that said nothing about objects would publish "storage":{} and
	// an operator reading the document could not tell that from one that had.
	Storage Storage `json:"storage,omitzero"`
}

// Storage is what the cloud promises about objects.
//
// THE TWO ARE ONE FIELD IN TWO HALVES, which is why they are nested rather than
// spelled flat beside Replication: Barrier <= Replicas is a relation between
// them, and a nested value is something Resolved can check without knowing what
// else the document holds.
type Storage struct {
	// Replicas is R: how many nodes the repair loop drives a bucket's index
	// and an object's bytes towards. Zero means the reader's default.
	//
	// IT IS THE FIRST KIND OF NUMBER. Two nodes with different R compute
	// different desired sets over the same membership — a permanently
	// different-sized set, which HRW's cycle-freedom argument does not rescue
	// because that argument assumes one R over one member set. It also decides
	// which shards a node opens at all, and it is the multiplier on what a
	// tenant's bytes cost.
	Replicas int `json:"replicas,omitempty"`

	// Barrier is K: how many acknowledgements a write waits for before it
	// returns. Zero means the reader's default.
	//
	// K IS NEVER INFERRED, and publishing it is not inference. What is refused
	// is DERIVING K FROM THE MEMBERSHIP VIEW: a service cannot tell "the fleet
	// is one node" from "I am partitioned from the other two", so a K that
	// adapted to what a node can currently see would ack a write during a
	// partition that no second node ever saw. A published constant cannot tell
	// those apart either, which is exactly the property wanted — a single-node
	// cloud still WRITES 1, it has just written it once for the cloud instead
	// of once per machine.
	//
	// IT IS THE SECOND KIND OF NUMBER. Two nodes with different K do not
	// disagree about any shared quantity; each waits for its own. They disagree
	// about what the cloud promised, and nothing in the cloud can notice.
	Barrier int `json:"barrier,omitempty"`
}

// Resolved fills the reader's defaults and refuses a pair that cannot be
// correct. A barrier above the replica target makes every write fail.
//
// IT IS A SEPARATE STEP FROM Decode BECAUSE ZERO MEANS THE READER'S DEFAULT.
// {Replicas: 0, Barrier: 5} is valid field by field and is R=3, K=5 once a
// reader fills it in — so the pair cannot be judged until somebody says what
// their zeros are, and the service whose numbers they are is the one that
// knows. That is also why this package holds no default of its own.
//
// It wraps ErrMalformed so a reader needs no second rule: a document that
// resolves to an impossible pair is refused exactly like one that will not
// parse, and the reader keeps the last value it read and has nothing if it had
// nothing.
func (s Storage) Resolved(defaultReplicas, defaultBarrier int) (Storage, error) {
	if s.Replicas == 0 {
		s.Replicas = defaultReplicas
	}
	if s.Barrier == 0 {
		s.Barrier = defaultBarrier
	}
	if s.Replicas < 1 {
		return Storage{}, fmt.Errorf("%w: storage replicas %d: a target below one places nothing", ErrMalformed, s.Replicas)
	}
	if s.Barrier < 1 {
		return Storage{}, fmt.Errorf("%w: storage barrier %d: a write that waits for no acknowledgement promises nothing", ErrMalformed, s.Barrier)
	}
	if s.Barrier > s.Replicas {
		return Storage{}, fmt.Errorf("%w: storage barrier %d exceeds replicas %d: every write would fail", ErrMalformed, s.Barrier, s.Replicas)
	}
	return s, nil
}

// Encode renders a fleet document. JSON because it is read by operators as
// often as by nodes, and it is a handful of fields that will grow one at a time.
func Encode(f Fleet) ([]byte, error) { return json.Marshal(f) }

// Decode reads one.
//
// IT CHECKS SHAPE AND NOT THE STORAGE PAIR. Every negative here is wrong at any
// reader's defaults; Barrier against Replicas is not, because a zero is the
// reader's number and not this package's. See Resolved.
func Decode(b []byte) (Fleet, error) {
	var out Fleet
	if err := json.Unmarshal(b, &out); err != nil {
		return Fleet{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if out.Replication < 0 {
		return Fleet{}, fmt.Errorf("%w: replication %d", ErrMalformed, out.Replication)
	}
	if out.Storage.Replicas < 0 {
		return Fleet{}, fmt.Errorf("%w: storage replicas %d", ErrMalformed, out.Storage.Replicas)
	}
	if out.Storage.Barrier < 0 {
		return Fleet{}, fmt.Errorf("%w: storage barrier %d", ErrMalformed, out.Storage.Barrier)
	}
	return out, nil
}
