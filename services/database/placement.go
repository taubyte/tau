package database

import (
	"github.com/taubyte/tau/pkg/placement"
)

// placement.go answers which nodes hold a database: one HRW over every member
// of the fleet. Every node computes the same answer from the same inputs, so a
// membership change needs no coordination.
//
// A resource's `regions` block is not read here. Placement in this build is
// one region, so every member competes for every database.

// resourceOf is what a kv database is placed by.
//
// THE ID AND NOT THE LABEL, for the reason everything else in this service uses
// the id: a label is local to whoever wrote it, and two databases called
// `tokens` in two projects must not land on the same nodes by virtue of sharing
// a word.
//
// THE MODEL IS IN THE STRING because the four models are placed independently:
// a project's kv and its vector index have no reason to share a replica set,
// and naming them apart means growing one does not move the other.
func resourceOf(at address) string {
	return "kv/" + at.project + "/" + at.environment + "/" + at.storeName()
}

// holders is every node that should hold this database.
func holders(at address, members []placement.Member, replication int) []string {
	return placement.Desired(resourceOf(at), members, replication)
}

// holds reports whether one node should hold this database.
//
// IT IS THE WHOLE MAINTENANCE LOOP IN ONE CALL. A node asks it of every
// database it currently holds — false means drop, because something else took
// the slot — and of every database it does not, where true means acquire. Both
// directions fall out of the same function, which is why a membership change
// needs no coordination: every node reaches the same conclusion about itself.
func holds(at address, members []placement.Member, replication int, id string) bool {
	return placement.Holds(resourceOf(at), members, replication, id)
}
