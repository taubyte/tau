package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/cespare/xxhash/v2"

	"github.com/taubyte/tau/pkg/v2/placement"
)

// shard.go is why this service does not have one replicated database per
// bucket.
//
// THE INDEX IS SHARDED BY A CLOUD CONSTANT, NOT INSTANCED PER BUCKET. A shard
// is one store plus one gossip topic. There are Shards of them, always, and a
// bucket lands in one by hashing its address. So TOPIC COUNT STOPS TRACKING
// BUCKET POPULATION: it tracks Shards, which does not move when a tenant
// creates its ten-thousandth bucket.
//
// The alternative — a database and a topic per bucket instance — is what a
// design reaches for first because it gives perfect tenancy isolation, and it
// is a wall rather than a cost: gossipsub topic count, subscription churn and
// mesh maintenance all scale with the number of buckets a cloud has ever
// declared, and none of them degrade gracefully.
//
// ── AND A NODE OPENS A SHARD ONLY IF IT IS BOTH DESIRED AND OCCUPIED ─────
//
// desiredShards takes TWO inputs and needs both. HRW says which shards this
// node would hold; THE CATALOG says which shards exist at all. A three-node
// dev cloud with five buckets opens about five shards, not two hundred and
// fifty-six, because two hundred and fifty-one of them contain nothing.
//
// The direction that falls out of this is the right one: on a large fleet most
// nodes hold no index at all and only bytes, and the per-node index cost peaks
// at Shards*R/N — so THE INDEX COST IS HIGHEST EXACTLY WHERE THE DATA IS
// SMALLEST.
//
// ── SHARDING BY BUCKET, NOT BY PROJECT ───────────────────────────────────
//
// A whale project's buckets spread across shards instead of piling into one.
// Project-sharding would co-locate a tenant — better locality for a
// cross-bucket listing, which this service does not offer and never will
// (§"across buckets: nothing") — at the price of a hotspot proportional to the
// largest tenant. For data at rest, spreading wins.
//
// A bucket's INSTANCES stay together, because the shard is chosen from the
// bucket's key and not the instance's: an instance limit is counted per
// bucket, and counting it would otherwise be a cross-shard read.
//
// ── Shards IS A ONE-WAY DOOR ─────────────────────────────────────────────
//
// Changing it re-homes every bucket in the cloud at once. It stays fixed until
// index range-splitting lands, which is the one structure in this design that
// would earn a consensus group (doc.go says why, and why it is deferred).
//
// HOW TO CHOOSE IT, so the next person does not have to re-derive it: Shards
// bounds the per-node topic count at Shards*R/N and bounds the SMALLEST unit
// that can be rebalanced. Too small and one shard is a hotspot no membership
// change can relieve; too large and a small cloud pays subscription overhead
// for empty shards — which the catalog gate already answers, which is why the
// constant leans large. 256 with R=3 is 768 shard-replicas over a fleet: 8 per
// node at 96 nodes, 77 at 10, and 256 on a single-node cloud that has 256
// occupied shards, which a single-node cloud does not.
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

// reconcile brings the open set to `desired`: opens what is missing, closes
// what is no longer wanted.
//
// IT OPENS BEFORE IT CLOSES, and it closes nothing if an open failed. The
// window in the other order is a node that has dropped a shard it still holds
// the only reachable copy of, and the failure is silent — a read against it
// returns "not found" rather than an error.
//
// A CLOSE FAILURE IS REPORTED AND THE SHARD IS STILL DROPPED from the open
// set: the alternative is a store this node believes it holds and has already
// stopped reading.
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
