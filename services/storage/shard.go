package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/cespare/xxhash/v2"

	"github.com/taubyte/tau/pkg/placement"
)

// The index is sharded by a cloud constant, not instanced per bucket: a shard is one
// store plus one gossip topic, so topic count tracks Shards rather than the number of
// buckets ever declared. A node opens a shard only if HRW desires it and the catalog
// says it is occupied, so most nodes on a large fleet hold no index. Sharding is by
// bucket, so a large tenant spreads; a bucket's instances stay together so the
// instance limit is countable.
//
// Shards is a one-way door: changing it re-homes every bucket. It bounds per-node
// topics at Shards*R/N and the smallest rebalanceable unit; it leans large because
// the catalog gate makes empty shards free. 256 at R=3 is 8 per node at 96 nodes.
const Shards = 256

// shardOf is the bucket's shard. It hashes the bucket's KEY — the encoded
// identity, not the raw fields — so two tenants whose raw names concatenate
// alike still land by their encoded, boundary-safe form.
func shardOf(b Bucket) (uint16, error) {
	if !b.Valid() {
		return 0, ErrNoAddress
	}
	return uint16(xxhash.Sum64String(b.key()) % Shards), nil
}

// occupiedShards is which shards the catalog puts a bucket in. It is the
// catalog's whole contribution to this decision: which shards EXIST.
func occupiedShards(buckets []Address) (map[uint16]struct{}, error) {
	out := make(map[uint16]struct{}, len(buckets))
	for _, a := range buckets {
		s, err := shardOf(a.Bucket())
		if err != nil {
			return nil, err
		}
		out[s] = struct{}{}
	}
	return out, nil
}

// desiredShards is which shards this node opens: HRW-desired for it AND
// non-empty in the catalog's view. Both conditions, and neither alone.
func desiredShards(buckets []Address, stable []placement.Member, r int, self string) (map[uint16]struct{}, error) {
	occupied, err := occupiedShards(buckets)
	if err != nil {
		return nil, err
	}
	out := make(map[uint16]struct{}, len(occupied))
	for s := range occupied {
		if placement.Holds(indexResource(s), stable, r, self) {
			out[s] = struct{}{}
		}
	}
	return out, nil
}

// ShardOpener makes and unmakes a shard's store. shard.go decides WHICH shards
// a node holds; an opener decides HOW one is built — and production's builds a
// *kvdb.Datastore over the node's own store and DAG with a pubsub broadcaster,
// which is why this is an interface and not a function in this file.
type ShardOpener interface {
	Open(ctx context.Context, shard uint16) (Datastore, error)
	Close(ctx context.Context, shard uint16, store Datastore) error
}

// shardSet is the shards this node currently has open. It is the lazy half of
// "opened lazily": desiredShards computes the answer, and this applies it.
type shardSet struct {
	opener ShardOpener

	mu   sync.Mutex
	open map[uint16]Datastore
}

func newShardSet(opener ShardOpener) (*shardSet, error) {
	if opener == nil {
		return nil, errors.New("storage: no shard opener")
	}
	return &shardSet{opener: opener, open: map[uint16]Datastore{}}, nil
}

// reconcile brings the open set to `desired`. It opens before it closes, and closes
// nothing if an open failed, since a dropped last-reachable shard reads as "not
// found". A failed close still drops the shard from the set.
func (s *shardSet) reconcile(ctx context.Context, desired map[uint16]struct{}) (opened, closed []uint16, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for shard := range desired {
		if _, have := s.open[shard]; have {
			continue
		}
		store, oerr := s.opener.Open(ctx, shard)
		if oerr != nil {
			return opened, nil, fmt.Errorf("storage: opening shard %d: %w", shard, oerr)
		}
		s.open[shard] = store
		opened = append(opened, shard)
	}

	var errs []error
	for shard, store := range s.open {
		if _, want := desired[shard]; want {
			continue
		}
		if cerr := s.opener.Close(ctx, shard, store); cerr != nil {
			errs = append(errs, fmt.Errorf("storage: closing shard %d: %w", shard, cerr))
		}
		delete(s.open, shard)
		closed = append(closed, shard)
	}

	sort.Slice(opened, func(i, j int) bool { return opened[i] < opened[j] })
	sort.Slice(closed, func(i, j int) bool { return closed[i] < closed[j] })
	return opened, closed, errors.Join(errs...)
}

// index returns the index for a bucket, if this node holds its shard.
func (s *shardSet) index(b Bucket) (index, bool, error) {
	shard, err := shardOf(b)
	if err != nil {
		return index{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, ok := s.open[shard]
	if !ok {
		return index{}, false, nil
	}
	return index{store: store}, true, nil
}

// held lists the open shards, ordered.
func (s *shardSet) held() []uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint16, 0, len(s.open))
	for shard := range s.open {
		out = append(out, shard)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// closeAll drops every open shard. It is what a service shutdown calls.
func (s *shardSet) closeAll(ctx context.Context) error {
	_, _, err := s.reconcile(ctx, nil)
	return err
}
