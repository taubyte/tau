package database

import "context"

// store.go is where a resident engine's entries live, and who hands it one.

// Store is the four-method door a resident engine keeps its entries behind —
// the same one services/secrets and pkg/cache keep over a kvdb. The three
// resident engines each declared it identically, which is the same
// triplication Spec.Prefix replaced.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Stores hands an engine the store ONE database's entries live in.
//
// A PROVIDER AND NOT A STORE, and the reason is replication rather than tidiness.
// A replicated store is a pubsub topic, so one store for every database on a node
// is one topic carrying every database's rows. Two things follow, and the second
// is why this changed:
//
//   - every node subscribed to that topic holds every database's rows, which
//     makes placement a decoration: computing who should hold a database means
//     nothing when they all do;
//   - a Delete on that store is a tombstone EVERY replica applies. So a node
//     dropping a copy it no longer holds deletes the copy the node that just
//     acquired it is holding, and there is no local-only drop to reach for.
//     Moving a database would destroy it.
//
// The cost is what the engines' own comment said it was: a CRDT, a broadcaster
// and a topic per database. That cost is bounded by what a node HOLDS rather
// than by how many databases exist, which is the thing placement decides.
//
// Spec.Prefix() is what a store is named after: it already names a database
// uniquely and already refuses a separator in any segment.
type Stores func(ctx context.Context, spec Spec) (Store, error)

// OneStore is every database in one store.
//
// IT IS FOR A TEST AND FOR A NODE THAT REPLICATES NOTHING, and it is spelled out
// rather than left as the default so that choosing it is visible. On a replicated
// node it is the arrangement Stores exists to end — see there.
func OneStore(s Store) Stores {
	return func(context.Context, Spec) (Store, error) {
		if s == nil {
			return nil, ErrNoStore
		}
		return s, nil
	}
}
