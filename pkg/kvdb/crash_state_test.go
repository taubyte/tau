package kvdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"

	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/taubyte/tau/pkg/kvdb/mst"
)

var errCrash = errors.New("crashed")

// A crashPoint counts the writes of one replica, to its datastore and to
// its blockstore, and at the one numbered at takes a copy of both, which is
// what a process that died just before that write leaves on disk. That
// write and every one after it fail.
type crashPoint struct {
	t     testing.TB
	mu    sync.Mutex
	n, at int
	store ds.Datastore
	dag   *fetchThroughDAGSvc

	disk    ds.Datastore
	blocks  *fetchThroughDAGSvc
	crashed bool
}

func (c *crashPoint) step() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.crashed {
		return errCrash
	}
	c.n++
	if c.at >= 0 && c.n-1 == c.at {
		c.crashed = true
		c.disk, c.blocks = copyStore(c.t, c.store), copyBlocks(c.t, c.dag)
		return errCrash
	}
	return nil
}

func (c *crashPoint) arm(at int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n, c.at = 0, at
}

// crashStore has no batches of its own, so a batch commits as the writes
// it holds, one at a time, and a crash can land between any two.
type crashStore struct {
	ds.Datastore
	c *crashPoint
}

func (s *crashStore) Put(ctx context.Context, k ds.Key, v []byte) error {
	if err := s.c.step(); err != nil {
		return err
	}
	return s.Datastore.Put(ctx, k, v)
}

func (s *crashStore) Delete(ctx context.Context, k ds.Key) error {
	if err := s.c.step(); err != nil {
		return err
	}
	return s.Datastore.Delete(ctx, k)
}

type crashDAG struct {
	*fetchThroughDAGSvc
	c *crashPoint
}

func (d *crashDAG) Add(ctx context.Context, nd ipld.Node) error {
	if err := d.c.step(); err != nil {
		return err
	}
	return d.fetchThroughDAGSvc.Add(ctx, nd)
}

func (d *crashDAG) AddMany(ctx context.Context, nds []ipld.Node) error {
	for _, nd := range nds {
		if err := d.Add(ctx, nd); err != nil {
			return err
		}
	}
	return nil
}

func (d *crashDAG) Remove(ctx context.Context, c cid.Cid) error {
	if err := d.c.step(); err != nil {
		return err
	}
	return d.fetchThroughDAGSvc.Remove(ctx, c)
}

func (d *crashDAG) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	for _, c := range cids {
		if err := d.Remove(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func manualReplica(t testing.TB, store ds.Datastore, dag ipld.DAGService, name string) *Datastore {
	t.Helper()
	o := replicaOptions(noAutoCompact(), name)
	o.manual = true
	r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// assertCounts checks every reference count against the roots: one per
// indexed snapshot's state and per base tree, and one per link from a block
// they reach.
func assertCounts(t testing.TB, r *Datastore) {
	t.Helper()
	ctx := context.Background()
	want := map[cid.Cid]uint64{}
	var stack []cid.Cid
	ref := func(c cid.Cid) {
		if c.Defined() {
			if want[c] == 0 {
				stack = append(stack, c)
			}
			want[c]++
		}
	}
	prefix := r.blocksPrefix()
	err := scanStore(ctx, r.store, prefix, func(_ string, v []byte) error {
		b, err := decodeBlock(v)
		ref(b.state)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ref(r.base.elems)
	ref(r.base.pending)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nd, err := r.dagService.Get(ctx, c)
		if err != nil {
			t.Fatalf("owned state block %s: %s", c, err)
		}
		for _, l := range nd.Links() {
			ref(l.Cid)
		}
	}
	if got := owned(t, r); !maps.Equal(got, want) {
		t.Errorf("reference counts are %v, the roots make them %v", got, want)
	}
}

// assertTreesMatch checks that the base with the journal applied is the
// state the store holds, record for record.
func assertTreesMatch(t testing.TB, r *Datastore) {
	t.Helper()
	ctx := context.Background()
	r.mu.Lock()
	defer r.mu.Unlock()
	gov, got, err := r.buildState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	elems, pending, ccs := map[string][]byte{}, map[string][]byte{}, map[string][]byte{}
	if err := scanStore(ctx, r.store, r.set.elemsPrefix(), func(k string, v []byte) error {
		el, err := r.set.parseElem(k, v)
		elems[string(elemTreeKey(el.key, el.dot))] = elemValue(el)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := scanStore(ctx, r.store, r.set.pendingPrefix(), func(k string, v []byte) error {
		p, err := r.set.parsePending(k, v)
		pending[string(pendingTreeKey(p))] = pendingValue(p)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for rep, k := range r.cc.inc {
		ccs[string(contextTreeKey(rep, k))] = contextValue(k)
	}
	wov := newOverlay(mdutils.Mock())
	if want := putState(t, wov, elems, pending, ccs); got != want {
		t.Errorf("the trees the journal gives are %s, the store holds %s", got, want)
		gs, err := loadStateRoot(ctx, gov, got)
		if err != nil {
			t.Fatal(err)
		}
		ws, err := loadStateRoot(ctx, wov, want)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]cid.Cid{{gs.elems, ws.elems}, {gs.pending, ws.pending}, {gs.ctx, ws.ctx}} {
			if err := mst.Diff(ctx, stateTree(gov, pair[0]), stateTree(wov, pair[1]), func(ch mst.Change) error {
				t.Errorf("  %d %x: journal gives %x, store holds %x", ch.Kind, ch.Key, ch.Old, ch.New)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		t.Errorf("  base %s %s; journal gives %s %s %s; store holds %s %s %s", r.base.elems, r.base.pending, gs.elems, gs.pending, gs.ctx, ws.elems, ws.pending, ws.ctx)
		scanStore(ctx, r.store, r.namespace.ChildString(journalNs), func(k string, _ []byte) error {
			t.Errorf("  mark %s", k)
			return nil
		})
	}
}

// TestACrashAtAnyWriteRecovers kills a replica at each write, in turn, of a
// compaction, the merge of a peer's snapshot that shares most of its state,
// and a second compaction with what they purge and reclaim, and reopens
// what it left on disk: it holds the state of before that step or of after
// it, its trees are its records, every reference count is its roots', and a
// clean compaction leaves no block that nothing indexes or owns.
func TestACrashAtAnyWriteRecovers(t *testing.T) {
	ctx := context.Background()
	for at := 0; at < 20000; at++ {
		inner := dssync.MutexWrap(ds.NewMapDatastore())
		own := newFetchThrough()
		c := &crashPoint{t: t, at: -1, store: inner, dag: own}
		r := manualReplica(t, &crashStore{Datastore: inner, c: c}, &crashDAG{fetchThroughDAGSvc: own, c: c}, "crashing: ")
		peerDag := newFetchThrough()
		peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
		peerDag.setRemotes(own.DAGService)
		own.setRemotes(peerDag.DAGService)

		for i := range 30 {
			put(t, r, fmt.Sprintf("/k%02d", i), fmt.Sprintf("v%d", i))
		}
		del(t, r, "/k03")
		compact(t, r)
		syncReplicaHeads(t, r, peer)
		put(t, r, "/k04", "again")
		del(t, r, "/k05")
		before := queryAll(t, r)
		put(t, peer, "/p", "peer")
		del(t, peer, "/k07")
		compact(t, peer)
		if at == 0 {
			assertTreesMatch(t, r)
			assertCounts(t, r)
			if t.Failed() {
				t.Fatal("before any crash")
			}
		}

		c.arm(at)
		err := func() error {
			if _, err := r.Compact(ctx); err != nil {
				return err
			}
			hs, _ := peer.heads.List()
			for _, h := range hs {
				if err := r.handleBlock(ctx, h); err != nil {
					return err
				}
			}
			_, err := r.Compact(ctx)
			return err
		}()
		after := maps.Clone(before)
		after["/p"] = []byte("peer")
		delete(after, "/k07")
		if !c.crashed {
			if err != nil {
				t.Fatal(err)
			}
			if got := queryAll(t, r); !sameKV(got, after) {
				t.Fatalf("without a crash the replica holds %v, want %v", got, after)
			}
			assertTreesMatch(t, r)
			assertCounts(t, r)
			if at < 50 {
				t.Fatalf("the run wrote only %d times", at)
			}
			r.Close()
			peer.Close()
			return
		}
		r.Close()

		if err := c.disk.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
			t.Fatal(err)
		}
		forgetIncarnation(r.incarnationKey())
		c.blocks.setRemotes(peerDag.DAGService)
		t.Logf("crash at write %d: %v", at, err)
		re := manualReplica(t, c.disk, c.blocks, "reopened: ")
		got := queryAll(t, re)
		if !sameKV(got, before) && !sameKV(got, after) {
			t.Fatalf("crash at write %d: the replica holds %v, which is neither %v nor %v", at, got, before, after)
		}
		assertTreesMatch(t, re)
		assertCounts(t, re)
		compact(t, re)
		assertTreesMatch(t, re)
		assertCounts(t, re)
		assertNoLeak(t, re, c.blocks)
		if t.Failed() {
			t.Fatalf("crash at write %d", at)
		}
		re.Close()
		peer.Close()
	}
	t.Fatal("the run never ended")
}

func sameKV(a, b map[string][]byte) bool { return maps.EqualFunc(a, b, bytes.Equal) }

// TestAStateStoreFaultIsSurvived fails, in turn, each kind of operation on
// each kind of key a state lives under while a replica compacts, merges a
// peer's snapshot that shares part of its state and compacts again. Once
// the store heals and the replica reopens and does it all again, it holds
// the peer's state, its trees are its records, every reference count is its
// roots', and it holds no block nothing indexes or owns.
func TestAStateStoreFaultIsSurvived(t *testing.T) {
	ctx := context.Background()
	keyed := []string{"Get", "Put", "Delete", "Has", "Query", "BatchPut", "BatchDelete"}
	prefixes := []string{"/j/", "/n/", "/x/", "/sr", "/w", "/b/", "/s/e/", "/s/t/", "/c/"}
	type fault struct{ op, prefix string }
	var faults []fault
	for _, p := range prefixes {
		for _, op := range keyed {
			faults = append(faults, fault{op, p})
		}
	}
	for _, op := range []string{"Batch", "Commit", "Sync"} {
		faults = append(faults, fault{op, ""})
	}
	faults = append(faults, fault{"none", ""})
	for _, f := range faults {
		t.Run(f.op+f.prefix, func(t *testing.T) {
			store := newFaultyDatastore(ds.NewMapDatastore())
			own := &removeFailing{fetchThroughDAGSvc: newFetchThrough()}
			r := manualReplica(t, store, own, "faulty: ")
			peerDag := newFetchThrough()
			peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
			defer peer.Close()
			peerDag.setRemotes(own.DAGService)
			own.setRemotes(peerDag.DAGService)
			for i := range 30 {
				put(t, r, fmt.Sprintf("/k%02d", i), fmt.Sprintf("v%d", i))
			}
			del(t, r, "/k03")
			compact(t, r)
			syncReplicaHeads(t, r, peer)
			put(t, r, "/k04", "again")
			del(t, r, "/k05")
			want := queryAll(t, r)
			want["/p"] = []byte("peer")
			delete(want, "/k07")
			put(t, peer, "/p", "peer")
			del(t, peer, "/k07")
			compact(t, peer)
			window := func(r *Datastore) error {
				_, err := r.Compact(ctx)
				hs, _ := peer.heads.List()
				for _, h := range hs {
					err = errors.Join(err, r.handleBlock(ctx, h))
				}
				_, cerr := r.Compact(ctx)
				return errors.Join(err, cerr)
			}

			prefix := ds.NewKey("crdttest").String() + f.prefix
			store.SetFail(func(op string, k ds.Key) error {
				if op == f.op && (f.prefix == "" || strings.HasPrefix(k.String(), prefix)) {
					return errFault
				}
				return nil
			})
			own.fail.Store(f.prefix == "/b/")
			t.Logf("with the fault: %v", window(r))
			store.SetFail(nil)
			own.fail.Store(false)

			// Reopened as after a crash, with no repair loop of its own to
			// race the walk below for the peer's head.
			closeStore(t, r)
			if err := store.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
				t.Fatal(err)
			}
			r = manualReplica(t, store, own, "healed: ")
			defer r.Close()
			if err := window(r); err != nil {
				t.Fatalf("healed: %v", err)
			}
			if got := queryAll(t, r); !sameKV(got, want) {
				t.Fatalf("healed, the replica holds %v, want %v", got, want)
			}
			assertTreesMatch(t, r)
			assertCounts(t, r)
			assertNoLeak(t, r, own.fetchThroughDAGSvc)
		})
	}
}
