package database

import (
	"context"

	"github.com/taubyte/tau/pkg/database"
)

// Holdings is not database.Stores, which creates a store when asked, and not on
// database.Store, since holding is a fact about a node, not an engine.

// Holdings is what this node holds on disk, and the only thing that can let go.
type Holdings interface {
	// Held reports whether this node has a copy of one database, without opening it: a
	// node answering by opening would become a holder because it was asked.
	Held(ctx context.Context, spec database.Spec) bool

	// Destroy removes this node's copy, locally, publishing nothing. It is the
	// one operation that ends a replica: closing leaves the bytes, and deleting
	// through the store broadcasts a tombstone every replica applies.
	Destroy(ctx context.Context, spec database.Spec) error

	// OnDisk is every database this node holds on disk, not merely open: a restarted node
	// has opened nothing.
	OnDisk(ctx context.Context) []database.Spec
}
