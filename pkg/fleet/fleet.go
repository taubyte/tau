// Package fleet is what an operator declares about a cloud as a whole, rather
// than about any project in it.
//
// It holds two kinds of number. The first is one every node must compute with
// identically: a replication floor from per-node config would make two nodes
// disagree permanently about who holds a database. The second states what the cloud
// promises: a durability barrier per node is unenforceable, since nothing compares
// two nodes' barriers.
//
// Both are published like a project: one content-addressed object with a monotone
// revision, read from the register, so a node that is behind is behind by a revision
// it can see.
package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrMalformed is a document that cannot be read. It is never a default: treating a
// corrupt declaration as silence would have every node pick its own floor.
var ErrMalformed = errors.New("fleet: this document cannot be read")

// Fleet is the operator's statement about the cloud.
type Fleet struct {
	// Replication is the floor on how many nodes hold each database. Zero means the
	// reader's default. It is the operator's number, never an author's: how many
	// machines this cloud can afford to lose.
	Replication int `json:"replication,omitempty"`

	// Storage is what the cloud promises about objects. It is not Replication: that
	// floor is per database, this one per shard over one global member set.
	//
	// omitzero, not omitempty, which never omits a struct.
	Storage Storage `json:"storage,omitzero"`

	// Retention is T for the cloud's replicated databases: how long a removal is
	// remembered, so how long a node can be away and rejoin without a rebuild. Zero
	// means the reader's default.
	//
	// Every write carries the T its author held, and what it teaches is kept for that
	// T, so nodes on different revisions still agree on expiry and lowering T forgets
	// nothing written under the old one.
	Retention Duration `json:"retention,omitzero"`
}

// Duration is a length of time written the way an operator writes one:
// "720h", "30m". Zero is omitted.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("a duration is a string such as \"720h\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Storage is what the cloud promises about objects. It is nested so Resolved can
// check Barrier <= Replicas on its own.
type Storage struct {
	// Replicas is R: how many nodes the repair loop drives a bucket's index and an
	// object's bytes towards. Zero means the reader's default. Two nodes with different
	// R compute differently sized desired sets over the same membership.
	Replicas int `json:"replicas,omitempty"`

	// Barrier is K: how many acknowledgements a write waits for before it returns.
	// Zero means the reader's default.
	//
	// K is never derived from the membership view: a node cannot tell "the fleet is one
	// node" from "I am partitioned", so an adaptive K would ack a write no second node
	// saw. A single-node cloud publishes 1.
	Barrier int `json:"barrier,omitempty"`
}

// Resolved fills the reader's defaults and refuses a pair that cannot be correct: a
// barrier above the replica target makes every write fail. It is separate from
// Decode because zero means the reader's default, so the pair cannot be judged
// before. It wraps ErrMalformed.
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

// Decode reads one. It checks shape, not the storage pair; see Resolved.
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
	if out.Retention < 0 {
		return Fleet{}, fmt.Errorf("%w: retention %s", ErrMalformed, time.Duration(out.Retention))
	}
	return out, nil
}
