package kvdb

// Fault-injection coverage for heads.go's error branches (Item H): most of
// heads.go's "if err != nil" checks wrap calls into the underlying
// ds.Datastore and are otherwise unreachable with an always-succeeding
// in-memory store.

import (
	"context"
	"errors"
	"testing"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	log "github.com/ipfs/go-log/v2"
)

func newTestHeadsWithStore(t *testing.T, store ds.Datastore) *heads {
	t.Helper()
	hh, err := newHeads(context.Background(), store, headsTestNS, headsTestDagsNS, log.Logger("crdt-test"))
	if err != nil {
		t.Fatal(err)
	}
	return hh
}

func randCid(t *testing.T, seed string) cid.Cid {
	t.Helper()
	pref := cid.Prefix{Version: 1, Codec: cid.DagProtobuf, MhType: 0x12, MhLength: -1}
	c, err := pref.Sum([]byte(seed))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestNewHeadsPrimeCacheError checks that newHeads propagates a failure
// from primeCache (via primeCacheNs's initial Query).
func TestNewHeadsPrimeCacheError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	fd.SetFail(failAlways("Query"))

	_, err := newHeads(context.Background(), fd, ds.NewKey("heads"), ds.NewKey("dagheads"), log.Logger("crdt-test"))
	if err == nil {
		t.Fatal("expected newHeads to fail when primeCache's Query fails")
	}
	if !errors.Is(err, errFault) {
		t.Fatalf("expected the error to wrap errFault, got %v", err)
	}
}

// TestNewHeadsPrimeCacheDagsNsError checks that a Query failure specifically
// in primeCacheDagsNs (the second of the two primeCache* calls) is also
// propagated.
func TestNewHeadsPrimeCacheDagsNsError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	calls := 0
	fd.SetFail(func(op string, key ds.Key) error {
		if op != "Query" {
			return nil
		}
		calls++
		if calls == 2 { // let primeCacheNs's Query succeed, fail the second (dags) one.
			return errFault
		}
		return nil
	})

	_, err := newHeads(context.Background(), fd, ds.NewKey("heads"), ds.NewKey("dagheads"), log.Logger("crdt-test"))
	if err == nil {
		t.Fatal("expected newHeads to fail when primeCacheDagsNs's Query fails")
	}
	if !errors.Is(err, errFault) {
		t.Fatalf("expected the error to wrap errFault, got %v", err)
	}
}

// TestHeadsReplaceErrors exercises every error branch in Replace: the
// Batch() call itself, the write() of the new head, and the Commit().
func TestHeadsReplaceErrors(t *testing.T) {
	old := Head{Cid: randCid(t, "old")}
	newH := Head{Cid: randCid(t, "new")}

	t.Run("Batch fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		hh := newTestHeadsWithStore(t, fd)
		if err := hh.Add(context.Background(), old); err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("Batch"))
		if err := hh.Replace(context.Background(), old, newH); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault from a failing Batch(), got %v", err)
		}
	})

	t.Run("write fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		hh := newTestHeadsWithStore(t, fd)
		if err := hh.Add(context.Background(), old); err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("BatchPut"))
		if err := hh.Replace(context.Background(), old, newH); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault from a failing write(), got %v", err)
		}
	})

	t.Run("Commit fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		hh := newTestHeadsWithStore(t, fd)
		if err := hh.Add(context.Background(), old); err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("Commit"))
		if err := hh.Replace(context.Background(), old, newH); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault from a failing Commit(), got %v", err)
		}
	})
}

// TestHeadsAddError checks Add's write() error branch.
func TestHeadsAddError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	hh := newTestHeadsWithStore(t, fd)
	fd.SetFail(failAlways("Put"))

	if err := hh.Add(context.Background(), Head{Cid: randCid(t, "x")}); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from a failing Put(), got %v", err)
	}
}

// TestHeadsDeleteDAGErrors exercises DeleteDAG's Batch()/delete()/Commit()
// error branches.
func TestHeadsDeleteDAGErrors(t *testing.T) {
	h1 := Head{Cid: randCid(t, "d1"), HeadValue: HeadValue{DAGName: "dagX"}}

	t.Run("Batch fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		hh := newTestHeadsWithStore(t, fd)
		if err := hh.Add(context.Background(), h1); err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("Batch"))
		if _, err := hh.DeleteDAG(context.Background(), "dagX"); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("delete fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		hh := newTestHeadsWithStore(t, fd)
		if err := hh.Add(context.Background(), h1); err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("BatchDelete"))
		if _, err := hh.DeleteDAG(context.Background(), "dagX"); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("Commit fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		hh := newTestHeadsWithStore(t, fd)
		if err := hh.Add(context.Background(), h1); err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("Commit"))
		if _, err := hh.DeleteDAG(context.Background(), "dagX"); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})
}

// TestHeadsPrimeCacheNsDecodeErrors checks primeCacheNs's two decode-error
// branches (a key that doesn't decode to a CIDv1, and a value that isn't a
// valid Uvarint) by writing malformed entries directly under the heads
// namespace before priming.
func TestHeadsPrimeCacheNsDecodeErrors(t *testing.T) {
	t.Run("bad cid key", func(t *testing.T) {
		store := ds.NewMapDatastore()
		// Not a valid multihash-derived key.
		if err := store.Put(context.Background(), ds.NewKey("heads").ChildString("not-a-cid"), []byte{1}); err != nil {
			t.Fatal(err)
		}
		_, err := newHeads(context.Background(), store, ds.NewKey("heads"), ds.NewKey("dagheads"), log.Logger("crdt-test"))
		if err == nil {
			t.Fatal("expected newHeads to fail decoding a malformed head key")
		}
	})

	t.Run("bad height varint", func(t *testing.T) {
		store := ds.NewMapDatastore()
		c := randCid(t, "badheight")
		key := ds.NewKey("heads").Child(dshelp.MultihashToDsKey(c.Hash()))
		// An empty value fails binary.Uvarint decoding (n <= 0).
		if err := store.Put(context.Background(), key, []byte{}); err != nil {
			t.Fatal(err)
		}
		_, err := newHeads(context.Background(), store, ds.NewKey("heads"), ds.NewKey("dagheads"), log.Logger("crdt-test"))
		if err == nil {
			t.Fatal("expected newHeads to fail decoding a malformed height")
		}
	})
}

// TestHeadsPrimeCacheDagsNsDecodeErrors mirrors
// TestHeadsPrimeCacheNsDecodeErrors for the dagNs side, and also the
// "bad head key" (wrong path depth) tolerate-and-skip branch.
func TestHeadsPrimeCacheDagsNsDecodeErrors(t *testing.T) {
	t.Run("bad head key shape is skipped, not fatal", func(t *testing.T) {
		store := ds.NewMapDatastore()
		// Only one path component under the dags namespace instead of
		// the expected <dagName>/<cid>.
		if err := store.Put(context.Background(), ds.NewKey("dagheads").ChildString("onlyonepart"), []byte{1}); err != nil {
			t.Fatal(err)
		}
		hh, err := newHeads(context.Background(), store, ds.NewKey("heads"), ds.NewKey("dagheads"), log.Logger("crdt-test"))
		if err != nil {
			t.Fatalf("expected malformed dag head keys to be skipped, not fatal, got %v", err)
		}
		n, err := hh.Len(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("expected 0 heads primed from a malformed key, got %d", n)
		}
	})

	t.Run("bad height varint", func(t *testing.T) {
		store := ds.NewMapDatastore()
		c := randCid(t, "daghbadheight")
		key := ds.NewKey("dagheads").ChildString("someDag").Child(dshelp.MultihashToDsKey(c.Hash()))
		if err := store.Put(context.Background(), key, []byte{}); err != nil {
			t.Fatal(err)
		}
		_, err := newHeads(context.Background(), store, ds.NewKey("heads"), ds.NewKey("dagheads"), log.Logger("crdt-test"))
		if err == nil {
			t.Fatal("expected newHeads to fail decoding a malformed dag-head height")
		}
	})
}
