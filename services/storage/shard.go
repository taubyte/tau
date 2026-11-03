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
//
// A SHARD IN USE IS NEVER CLOSED UNDER ITS USER, and neither is one used after the
// view a reconcile decided from. A write opens its bucket's shard before the bucket
// is anywhere a reconcile reads, so a reconcile that read before the write would
// otherwise close the store the write is appending to. The next reconcile reads the
// bucket and keeps it, or, if placement moved it away, closes it once it is idle.
type shardSet struct {
	opener ShardOpener

	mu    sync.Mutex
	open  map[uint16]Datastore
	users map[uint16]int
	// used is the generation each shard was last held at; clock is the latest.
	used  map[uint16]uint64
	clock uint64
}

func newShardSet(opener ShardOpener) (*shardSet, error) {
	if opener == nil {
		return nil, errors.New("storage: no shard opener")
	}
	return &shardSet{opener: opener, open: map[uint16]Datastore{}, users: map[uint16]int{}, used: map[uint16]uint64{}}, nil
}

// mark is the generation a reconcile decides from: taken before it reads what it
// decides with.
func (s *shardSet) mark() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// reconcile brings the open set to `desired`, as decided from what was read after
// since. It opens before it closes, and closes nothing if an open failed, since a
// dropped last-reachable shard reads as "not found". A shard held now, or since, is
// left open for a later reconcile. A failed close still drops the shard from the set.
func (s *shardSet) reconcile(ctx context.Context, desired map[uint16]struct{}, since uint64) (opened, closed []uint16, err error) {
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
	for shard := range s.open {
		if _, want := desired[shard]; want || s.users[shard] > 0 || s.used[shard] > since {
			continue
		}
		if cerr := s.drop(ctx, shard); cerr != nil {
			errs = append(errs, cerr)
		}
		closed = append(closed, shard)
	}

	sort.Slice(opened, func(i, j int) bool { return opened[i] < opened[j] })
	sort.Slice(closed, func(i, j int) bool { return closed[i] < closed[j] })
	return opened, closed, errors.Join(errs...)
}

// drop closes one shard and forgets it. The caller holds mu.
func (s *shardSet) drop(ctx context.Context, shard uint16) error {
	store := s.open[shard]
	delete(s.open, shard)
	delete(s.used, shard)
	if err := s.opener.Close(ctx, shard, store); err != nil {
		return fmt.Errorf("storage: closing shard %d: %w", shard, err)
	}
	return nil
}

// hold is the index for a bucket, open until release is called; ok is false when
// this node has not opened its shard. With open, a shard not yet open is opened now,
// for a write that reached a holder before the catalog named its bucket.
func (s *shardSet) hold(ctx context.Context, b Bucket, open bool) (i index, release func(), ok bool, err error) {
	shard, err := shardOf(b)
	if err != nil {
		return index{}, nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, have := s.open[shard]
	if !have && open {
		store, err = s.opener.Open(ctx, shard)
		if err != nil {
			return index{}, nil, false, fmt.Errorf("storage: opening shard %d: %w", shard, err)
		}
		s.open[shard], have = store, true
	}
	if !have {
		return index{}, nil, false, nil
	}
	s.users[shard]++
	s.clock++
	s.used[shard] = s.clock
	var once sync.Once
	return index{store: store}, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.users[shard]--; s.users[shard] == 0 {
				delete(s.users, shard)
			}
		})
	}, true, nil
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

// closeAll drops every open shard, held or not. It is what a service shutdown calls.
func (s *shardSet) closeAll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for shard := range s.open {
		if err := s.drop(ctx, shard); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
