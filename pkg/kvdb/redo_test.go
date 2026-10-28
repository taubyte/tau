package kvdb

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
)

// largestPut remembers the largest value written through it.
type largestPut struct {
	ds.Datastore
	max int
}

func (s *largestPut) Put(ctx context.Context, k ds.Key, v []byte) error {
	s.max = max(s.max, len(v))
	return s.Datastore.Put(ctx, k, v)
}

// An atomic commit records its writes in bounded chunks. Recorded as one
// value, a cold catch-up's whole state, the record stayed in the store's
// files after it was applied and deleted, and every lookup the store's
// index sent to its block decompressed all of it: a 100-key catch-up into
// 100k keys spent 11 of its 12 seconds in 100 lookups of pending removals.
func TestAnAtomicCommitStoresNoLargeValue(t *testing.T) {
	ctx := context.Background()
	store := &largestPut{Datastore: dssync.MutexWrap(ds.NewMapDatastore())}
	r := manualReplica(t, store, newFetchThrough(), "large: ")
	defer r.Close()
	b, err := r.Batch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2000 {
		if err := b.Put(ctx, ds.NewKey(fmt.Sprintf("/k%04d", i)), []byte("value")); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	compact(t, r)
	if store.max > 2*redoChunkBytes {
		t.Fatalf("a compaction of 2000 keys stored a %d-byte value, want at most %d", store.max, 2*redoChunkBytes)
	}
	if n, err := countPrefix(ctx, store, r.redoPrefix()); err != nil || n != 0 {
		t.Fatalf("%d chunks left after the commit applied (%v)", n, err)
	}
}

// A mark naming a record that is not whole, or that cannot be read, fails
// the open rather than applying part of it.
func TestABrokenRedoMarkFailsTheOpen(t *testing.T) {
	ctx := context.Background()
	for name, mark := range map[string][]byte{
		"undecodable":   {0xff},
		"missing chunk": binary.AppendUvarint(binary.AppendUvarint(nil, 1), 1),
	} {
		t.Run(name, func(t *testing.T) {
			inner := dssync.MutexWrap(ds.NewMapDatastore())
			if err := inner.Put(ctx, ds.NewKey("/crdttest").ChildString(redoMark), mark); err != nil {
				t.Fatal(err)
			}
			o := replicaOptions(noAutoCompact(), "broken: ")
			if r, err := NewDatastore(inner, ds.NewKey("/crdttest"), newFetchThrough(), &nullBroadcaster{}, o); err == nil {
				r.Close()
				t.Fatal("the store opened")
			}
		})
	}
}
