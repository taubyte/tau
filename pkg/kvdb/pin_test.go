package kvdb

import (
	"context"
	"errors"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
)

func TestAPinKeepsAStateForItsLeaseOnly(t *testing.T) {
	ctx := context.Background()
	st := newSimTime()
	own := newFetchThrough()
	o := replicaOptions(st.options(0), "pinned: ")
	o.CompactAfterBlocks = 0
	o.manual = true
	r, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("crdttest"), own, &nullBroadcaster{}, o)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seedKeys(t, r, 200, "k")
	compact(t, r)
	old := stateOf(t, r)
	if _, err := r.pinState(ctx, stateBlocksOf(t, r, old)[1], time.Minute); err == nil {
		t.Fatal("a block no snapshot names as its state was pinned")
	}
	pin, err := r.pinState(ctx, old, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		put(t, r, "/k"+string(rune('a'+i%26)), "changed")
	}
	compact(t, r)
	if _, err := r.reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	assertReach(t, r)
	closure := stateBlocksOf(t, r, old)
	for _, c := range closure {
		if !own.holds(c) {
			t.Fatalf("pinned state block %s is gone", c)
		}
		if _, err := pin.Get(ctx, c); err != nil {
			t.Fatalf("reading %s through the pin: %v", c, err)
		}
	}
	held := own.blockCount(t)

	st.advance(time.Minute)
	if _, err := pin.Get(ctx, old); !errors.Is(err, errPinExpired) {
		t.Fatalf("reading through an expired pin: %v", err)
	}
	if _, err := r.reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	assertReach(t, r)
	if r.reaches(old) {
		t.Fatal("an expired pin still keeps its state")
	}
	if n := own.blockCount(t); n >= held {
		t.Fatalf("an expired pin's state was not removed: %d blocks held, %d before", n, held)
	}
	assertHoldsExactly(t, r, own)
	if err := pin.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPinsAreBoundedAndEndWithTheProcess(t *testing.T) {
	ctx := context.Background()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	own := newFetchThrough()
	r := manualReplica(t, store, own, "pins: ")
	seedKeys(t, r, 50, "k")
	compact(t, r)
	root := stateOf(t, r)
	var pins []*statePin
	for range maxStatePins {
		p, err := r.pinState(ctx, root, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		pins = append(pins, p)
	}
	if _, err := r.pinState(ctx, root, time.Hour); !errors.Is(err, errTooManyPins) {
		t.Fatalf("pin %d: %v", maxStatePins+1, err)
	}
	if err := pins[0].Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pins[0].Get(ctx, root); !errors.Is(err, errPinExpired) {
		t.Fatalf("reading through a released pin: %v", err)
	}
	if _, err := r.pinState(ctx, root, time.Hour); err != nil {
		t.Fatalf("a released pin's place: %v", err)
	}
	put(t, r, "/k0001", "changed")
	compact(t, r)
	assertReach(t, r)
	if !r.reaches(root) {
		t.Fatal("a pinned state went with its snapshot")
	}

	// Closed as after a crash, with the pins still recorded.
	r.Close()
	if err := store.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
		t.Fatal(err)
	}
	forgetIncarnation(r)
	re := manualReplica(t, store, own, "reopened: ")
	defer re.Close()
	if n, err := countPrefix(ctx, re.store, re.pinPrefix()); err != nil || n != 0 {
		t.Fatalf("%d pins are recorded after a restart: %v", n, err)
	}
	if re.reaches(root) {
		t.Fatal("a pin outlived its process")
	}
	assertReach(t, re)
	assertHoldsExactly(t, re, own)
}

func stateBlocksOf(t testing.TB, r *Datastore, c cid.Cid) []cid.Cid {
	t.Helper()
	ctx := context.Background()
	out := []cid.Cid{c}
	for i := 0; i < len(out); i++ {
		nd, err := r.dagService.Get(ctx, out[i])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, blockLinksOf(t, nd)...)
	}
	return out
}
