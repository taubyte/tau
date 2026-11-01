package database

import "context"

// Store is the four-method door a resident engine keeps its entries behind, as
// services/secrets and pkg/cache do over a kvdb.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Stores hands an engine the store one database's entries live in. A provider and not
// a store: a replicated store is a pubsub topic, so one store for all databases would
// put every database on every subscribed node (placement meaningless) and turn a local
// drop into a fleet-wide tombstone. The cost, a CRDT and topic per database, is
// bounded by what a node holds. Stores are named by Spec.Prefix.
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
