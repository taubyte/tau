package kvdb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
)

// brokenBlockDAG fails GetMany for one block the way boxo does: a plain error
// wrapping nothing. With wait set it first blocks until the context ends.
type brokenBlockDAG struct {
	ipld.DAGService
	block   cid.Cid
	wait    bool
	reached chan struct{}
	once    sync.Once
}

func (b *brokenBlockDAG) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	if !slices.Contains(cids, b.block) {
		return b.DAGService.GetMany(ctx, cids)
	}
	out := make(chan *ipld.NodeOption, 1)
	go func() {
		defer close(out)
		b.once.Do(func() { close(b.reached) })
		if b.wait {
			<-ctx.Done()
		}
		out <- &ipld.NodeOption{Err: errors.New("failed to fetch all nodes")}
	}()
	return out
}

const shutdownTestKeys = 12

// writeChain publishes a linear history and returns its head and every block
// from the head down.
func writeChain(t *testing.T, dag ipld.DAGService) (Head, []cid.Cid) {
	t.Helper()
	ctx := t.Context()
	w, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("w"), dag, nil, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	for i := range shutdownTestKeys {
		if err := w.Put(ctx, ds.NewKey(fmt.Sprint(i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	heads, _, err := w.heads.List(ctx)
	if err != nil || len(heads) != 1 {
		t.Fatalf("want one head, got %v, %v", heads, err)
	}
	var chain []cid.Cid
	for c := heads[0].Cid; ; {
		chain = append(chain, c)
		n, err := dag.Get(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(n.Links()) == 0 {
			return heads[0], chain
		}
		c = n.Links()[0].Cid
	}
}

func openReader(t *testing.T, store ds.Datastore, dag ipld.DAGService) *Datastore {
	t.Helper()
	d, err := NewDatastore(store, ds.NewKey("r"), dag, nil, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// interruptWalk stops a reader while its walk waits on a block in the middle
// of the chain, leaving processed parents above an unfetched child.
func interruptWalk(t *testing.T, store ds.Datastore, dag ipld.DAGService) (Head, *Datastore) {
	t.Helper()
	head, chain := writeChain(t, dag)
	gate := &brokenBlockDAG{DAGService: dag, block: chain[len(chain)/2], wait: true, reached: make(chan struct{})}
	d := openReader(t, store, gate)

	walked := make(chan struct{})
	go func() {
		defer close(walked)
		_ = d.handleBranch(d.ctx, head, head.Cid)
	}()
	select {
	case <-gate.reached:
	case <-time.After(30 * time.Second):
		t.Fatal("walk never reached the blocked child")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	<-walked
	return head, d
}

func TestShutdownDuringWalkIsNotDamage(t *testing.T) {
	_, d := interruptWalk(t, dssync.MutexWrap(ds.NewMapDatastore()), newTestDagsync())
	if d.IsDirty(context.Background()) {
		t.Fatal("a walk cancelled by shutdown must not mark the store dirty")
	}
}

func TestRealFetchFailureMarksDirty(t *testing.T) {
	dag := newTestDagsync()
	head, chain := writeChain(t, dag)
	gate := &brokenBlockDAG{DAGService: dag, block: chain[len(chain)/2], reached: make(chan struct{})}
	d := openReader(t, dssync.MutexWrap(ds.NewMapDatastore()), gate)
	defer func() { _ = d.Close() }()

	ctx := t.Context()
	if err := d.handleBranch(ctx, head, head.Cid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !d.IsDirty(ctx) {
		if time.Now().After(deadline) {
			t.Fatal("a failed fetch with a live context must mark the store dirty")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInterruptedWalkConvergesAfterReopen(t *testing.T) {
	ctx := t.Context()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newTestDagsync()
	head, _ := interruptWalk(t, store, dag)

	d := openReader(t, store, dag)
	defer func() { _ = d.Close() }()

	missing := func() int {
		n := 0
		for i := range shutdownTestKeys {
			if ok, err := d.Has(ctx, ds.NewKey(fmt.Sprint(i))); err != nil || !ok {
				n++
			}
		}
		return n
	}
	isHead := func() bool { _, ok := d.heads.Get(ctx, head.Cid); return ok }
	deadline := time.Now().Add(30 * time.Second)
	for missing() > 0 || !isHead() {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d keys missing, head present: %t", missing(), shutdownTestKeys, isHead())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if d.IsDirty(ctx) {
		t.Fatal("resuming an interrupted walk must not need a repair")
	}
	res, err := store.Query(ctx, query.Query{Prefix: d.unfinishedPrefix().String(), KeysOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	left, err := res.Rest()
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("resumed walks left %d records behind", len(left))
	}
}
