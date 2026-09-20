package database

import (
	"context"

	"github.com/taubyte/tau/pkg/database"
)

// holdings.go is the seam between deciding to let go of a database and actually
// letting go of it.
//
// WHY IT IS NOT database.Stores. That provider ANSWERS WITH a store, and asking
// it creates one — which is right for serving a request and exactly wrong for
// the two questions here. "Do you hold this?" must not make the answer yes, and
// "stop holding this" is not an open at all.
//
// WHY IT IS NOT ON database.Store EITHER. Those four methods are what an engine
// needs, and a map satisfies them; holding is a fact about a NODE, not about a
// store, and an engine has no business knowing whether the node it runs on is
// one of a database's replicas.

// Holdings is what this node holds on disk, and the only thing that can let go.
type Holdings interface {
	// Held reports whether this node has a copy of one database, WITHOUT
	// opening it.
	//
	// THE WITHOUT IS THE POINT. A node that answered by opening would become a
	// holder because somebody asked, and would then answer a digest that looks
	// converged — which is how a node that placement never chose ends up
	// authorising another node's drop.
	Held(ctx context.Context, spec database.Spec) bool

	// Destroy removes this node's copy, locally, publishing nothing. It is the
	// one operation that ends a replica: closing leaves the bytes, and deleting
	// through the store broadcasts a tombstone every replica applies.
	Destroy(ctx context.Context, spec database.Spec) error

	// OnDisk is every database this node holds, which is what the maintenance
	// sweep walks.
	//
	// ON DISK AND NOT WARM. A restarted node holds everything it held before
	// and has opened none of it; a sweep over what is merely open would leave
	// those copies unexamined, answering stale digests and freezing every move
	// that needs them.
	OnDisk(ctx context.Context) []database.Spec
}
