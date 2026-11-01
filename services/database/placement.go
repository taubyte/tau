package database

import (
	"github.com/taubyte/tau/pkg/placement"
)

// Which nodes hold a database: one HRW over every member, so every node computes the
// same answer with no coordination. `regions` is not read: placement here is one
// region.

// resourceOf is what a kv database is placed by: the id, not the label, and the model,
// so a project's models get independent replica sets.
func resourceOf(at address) string {
	return "kv/" + at.project + "/" + at.environment + "/" + at.storeName()
}

// holders is every node that should hold this database.
func holders(at address, members []placement.Member, replication int) []string {
	return placement.Desired(resourceOf(at), members, replication)
}

// holds reports whether one node should hold this database: asked of what a node
// holds (false means drop) and of what it does not (true means acquire).
func holds(at address, members []placement.Member, replication int, id string) bool {
	return placement.Holds(resourceOf(at), members, replication, id)
}
