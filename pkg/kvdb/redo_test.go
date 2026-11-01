package kvdb

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
)

type largestPut struct {
	ds.Datastore
	max int
}

func (s *largestPut) Put(ctx context.Context, k ds.Key, v []byte) error {
	s.max = max(s.max, len(v))
	return s.Datastore.Put(ctx, k, v)
}

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
