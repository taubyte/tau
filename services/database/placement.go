package database

import (
	"github.com/taubyte/tau/pkg/placement"
)

// placement.go binds pkg/placement to this service's one question: which nodes
// hold this database.
//
// THE COMPUTATION IS NOT HERE, and that is service-conventions.md §4 — v1's
// hoarder and patrick each grew their own in-service placement, and the note
// names porting that as the thing not to do. What is here is the part that is
// the database service's: which resource string to name, and what the target
// is.
//
// WHY COMPUTED AND NOT AGREED. Every node that holds the same membership list
// reaches the same answer with no auction, no leader and no directory — so
// "which node has this database" is answerable by a caller with no round trip,
// and a node that is down cannot make a database unfindable. v1 measured the
// alternative: replacing timed bidding with HRW took replication from 617s to
// about 8s.

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

// holders is the nodes that should hold this database, in ranking order.
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
