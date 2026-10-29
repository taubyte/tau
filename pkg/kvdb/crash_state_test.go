package kvdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
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

// A stateFault fails op, on the keys under prefix if it names one.
type stateFault struct{ op, prefix string }

// stateFaults is every kind of operation on every kind of key a state lives
// under, and each operation that names no key.
func stateFaults() []stateFault {
	keyed := []string{"Get", "Put", "Delete", "Has", "Query", "BatchPut", "BatchDelete"}
	prefixes := []string{"/j/", "/n/", "/x/", "/sr", "/w", "/b/", "/s/e/", "/s/t/", "/c/"}
	var faults []stateFault
	for _, p := range prefixes {
		for _, op := range keyed {
			faults = append(faults, stateFault{op, p})
		}
	}
	for _, op := range []string{"Batch", "Commit", "Sync"} {
		faults = append(faults, stateFault{op, ""})
	}
	return append(faults, stateFault{"none", ""})
}

// TestAStateStoreFaultIsSurvived fails, in turn, each kind of operation on
// each kind of key a state lives under while a replica compacts, merges a
// peer's snapshot that shares part of its state and compacts again. Once
// the store heals and the replica reopens and does it all again, it holds
// the peer's state, its trees are its records, every reference count is its
// roots', and it holds no block nothing indexes or owns.
func TestAStateStoreFaultIsSurvived(t *testing.T) {
	ctx := context.Background()
	for _, f := range stateFaults() {
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

// TestACrashInAResumedFetchRecovers kills a replica at each write, in turn,
// of a catch-up that goes on from an attempt that fetched part of a peer's
// state, with maintenance and its reclaim between attempts, and of the
// compaction after it. What it left on disk holds the state of before the
// merge or of after it, its trees are its records, every reference count is
// its roots', and once it catches up again and compacts it holds the peer's
// state and no block that nothing indexes or owns.
func TestACrashInAResumedFetchRecovers(t *testing.T) {
	ctx := context.Background()
	// The peer only serves its state, so one does for every crash.
	peerDag := newFetchThrough()
	peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
	defer peer.Close()
	seedKeys(t, peer, 60, "p")
	compact(t, peer)
	per := len(stateClosure(t, peer))/3 + 1
	for at := 0; at < 20000; at++ {
		inner := dssync.MutexWrap(ds.NewMapDatastore())
		own := newFetchThrough()
		c := &crashPoint{t: t, at: -1, store: inner, dag: own}
		r := manualReplica(t, &crashStore{Datastore: inner, c: c}, &crashDAG{fetchThroughDAGSvc: own, c: c}, "crashing: ")
		link := newRationed(peerDag.DAGService)
		own.setRemotes(link)

		for i := range 4 {
			put(t, r, fmt.Sprintf("/r%02d", i), fmt.Sprintf("v%d", i))
		}
		compact(t, r)
		before := queryAll(t, r)
		after := maps.Clone(before)
		maps.Copy(after, queryAll(t, peer))
		link.refill(per)
		syncHeadsLogged(t, peer, r)
		if !r.IsDirty(ctx) {
			t.Fatal("the first attempt fetched the whole state")
		}

		c.arm(at)
		err := func() error {
			if _, err := attemptCatchUp(t, r, peer, link, per, 50); err != nil {
				return err
			}
			_, err := r.Compact(ctx)
			return err
		}()
		if !c.crashed {
			if err != nil {
				t.Fatal(err)
			}
			if got := queryAll(t, r); !sameKV(got, after) {
				t.Fatalf("without a crash the replica holds %d keys, want %d", len(got), len(after))
			}
			assertTreesMatch(t, r)
			assertCounts(t, r)
			assertNoLeak(t, r, own)
			if at < 50 {
				t.Fatalf("the run wrote only %d times", at)
			}
			t.Logf("%d writes, %d state blocks fetched %d at a time", at, len(stateClosure(t, peer)), per)
			r.Close()
			return
		}
		r.Close()

		if err := c.disk.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
			t.Fatal(err)
		}
		forgetIncarnation(r.incarnationKey())
		c.blocks.setRemotes(peerDag.DAGService)
		re := manualReplica(t, c.disk, c.blocks, "reopened: ")
		got := queryAll(t, re)
		if !sameKV(got, before) && !sameKV(got, after) {
			t.Fatalf("crash at write %d: the replica holds %d keys, neither the %d of before nor the %d of after", at, len(got), len(before), len(after))
		}
		assertTreesMatch(t, re)
		assertCounts(t, re)
		syncReplicaHeads(t, peer, re)
		if err := re.Repair(ctx); err != nil {
			t.Fatalf("crash at write %d: %v", at, err)
		}
		compact(t, re)
		if got := queryAll(t, re); !sameKV(got, after) {
			t.Fatalf("crash at write %d: caught up again, the replica holds %d keys, want %d", at, len(got), len(after))
		}
		assertTreesMatch(t, re)
		assertCounts(t, re)
		assertNoLeak(t, re, c.blocks)
		if t.Failed() {
			t.Fatalf("crash at write %d", at)
		}
		re.Close()
	}
	t.Fatal("the run never ended")
}

// TestAStateStoreFaultInAResumedFetchIsSurvived fails, in turn, each kind of
// operation on each kind of key a state lives under while a replica goes on
// fetching a peer's state that one attempt fetched part of, with maintenance
// and its reclaim between attempts, and compacts. Once the store heals and
// the replica reopens and catches up again, it holds the peer's state, its
// trees are its records, every reference count is its roots', and it holds
// no block nothing indexes or owns.
func TestAStateStoreFaultInAResumedFetchIsSurvived(t *testing.T) {
	ctx := context.Background()
	for _, f := range stateFaults() {
		t.Run(f.op+f.prefix, func(t *testing.T) {
			store := newFaultyDatastore(ds.NewMapDatastore())
			own := &removeFailing{fetchThroughDAGSvc: newFetchThrough()}
			r := manualReplica(t, store, own, "faulty: ")
			peerDag := newFetchThrough()
			peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
			defer peer.Close()
			link := newRationed(peerDag.DAGService)
			own.setRemotes(link)
			for i := range 10 {
				put(t, r, fmt.Sprintf("/r%02d", i), fmt.Sprintf("v%d", i))
			}
			compact(t, r)
			seedKeys(t, peer, 120, "p")
			compact(t, peer)
			want := queryAll(t, r)
			maps.Copy(want, queryAll(t, peer))
			per := len(stateClosure(t, peer))/3 + 1
			link.refill(per)
			syncHeadsLogged(t, peer, r)
			if !r.IsDirty(ctx) {
				t.Fatal("the first attempt fetched the whole state")
			}

			prefix := ds.NewKey("crdttest").String() + f.prefix
			store.SetFail(func(op string, k ds.Key) error {
				if op == f.op && (f.prefix == "" || strings.HasPrefix(k.String(), prefix)) {
					return errFault
				}
				return nil
			})
			own.fail.Store(f.prefix == "/b/")
			_, err := attemptCatchUp(t, r, peer, link, per, 20)
			_, cerr := r.Compact(ctx)
			t.Logf("with the fault: %v", errors.Join(err, cerr))
			store.SetFail(nil)
			own.fail.Store(false)

			closeStore(t, r)
			if err := store.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
				t.Fatal(err)
			}
			r = manualReplica(t, store, own, "healed: ")
			defer r.Close()
			if _, err := attemptCatchUp(t, r, peer, link, per, 20); err != nil {
				t.Fatalf("healed: %v", err)
			}
			compact(t, r)
			if got := queryAll(t, r); !sameKV(got, want) {
				t.Fatalf("healed, the replica holds %d keys, want %d", len(got), len(want))
			}
			assertTreesMatch(t, r)
			assertCounts(t, r)
			assertNoLeak(t, r, own.fetchThroughDAGSvc)
		})
	}
}

// A volatileStore is a datastore that keeps what it was not asked to sync
// in memory, in no particular order: a power loss keeps any subset of the
// writes no Sync has covered since, and every write a Sync covered. It
// records each write and each Sync, in order, while recording is on.
type volatileStore struct {
	ds.Datastore
	mu     sync.Mutex
	on     bool
	events []volatileEvent
}

// A volatileEvent is a Put, a Delete, or with sync set a Sync of key.
type volatileEvent struct {
	key       ds.Key
	value     []byte
	del, sync bool
}

func (s *volatileStore) record(e volatileEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.on {
		s.events = append(s.events, e)
	}
}

func (s *volatileStore) Put(ctx context.Context, k ds.Key, v []byte) error {
	s.record(volatileEvent{key: k, value: bytes.Clone(v)})
	return s.Datastore.Put(ctx, k, v)
}

func (s *volatileStore) Delete(ctx context.Context, k ds.Key) error {
	s.record(volatileEvent{key: k, del: true})
	return s.Datastore.Delete(ctx, k)
}

func (s *volatileStore) Sync(ctx context.Context, prefix ds.Key) error {
	s.record(volatileEvent{key: prefix, sync: true})
	return s.Datastore.Sync(ctx, prefix)
}

// unsynced is the writes among the first n events no Sync among them
// covers.
func unsynced(events []volatileEvent, n int) []int {
	var out []int
	for i := range n {
		e := events[i]
		if e.sync {
			continue
		}
		covered := false
		for _, s := range events[i+1 : n] {
			covered = covered || s.sync && (s.key.String() == "/" || s.key == e.key || s.key.IsAncestorOf(e.key))
		}
		if !covered {
			out = append(out, i)
		}
	}
	return out
}

// afterLoss is base with the first n events applied, but for the unsynced
// writes lost names.
func afterLoss(t testing.TB, base ds.Datastore, events []volatileEvent, n int, lost map[int]bool) ds.Datastore {
	t.Helper()
	ctx := context.Background()
	disk := copyStore(t, base)
	for i, e := range events[:n] {
		var err error
		switch {
		case e.sync || lost[i]:
		case e.del:
			err = disk.Delete(ctx, e.key)
		default:
			err = disk.Put(ctx, e.key, e.value)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return disk
}

// TestAnAtomicCommitSurvivesAnyLoss powers a replica off after each write
// and Sync of an atomic commit, keeping every subset of the writes no Sync
// covered: the replica reopens and holds all of the commit's writes or none.
// Without a Sync between applying the writes and recording the commit as
// applied, and another before its record is removed, a loss could keep the
// mark and lose a write, or lose the mark and keep the record's removal,
// and the store then would not open.
func TestAnAtomicCommitSurvivesAnyLoss(t *testing.T) {
	ctx := context.Background()
	inner := dssync.MutexWrap(ds.NewMapDatastore())
	vs := &volatileStore{Datastore: inner}
	r := manualReplica(t, vs, newFetchThrough(), "volatile: ")
	ns := r.namespace.ChildString("zz")
	if err := r.store.Put(ctx, ns.ChildString("gone"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Put(ctx, ns.ChildString("changed"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	old := map[string]string{"gone": "old", "changed": "old"}
	want := map[string]string{"changed": "new", "added": "new", "more": "new"}
	base := copyStore(t, inner)
	vs.mu.Lock()
	vs.on = true
	vs.mu.Unlock()
	tx := newTxn(r.store)
	tx.del(ns.ChildString("gone"))
	for k, v := range want {
		tx.put(ns.ChildString(k), []byte(v))
	}
	r.gcMu.Lock()
	err := r.commitAtomic(ctx, tx)
	r.gcMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	vs.mu.Lock()
	vs.on = false
	events := vs.events
	vs.mu.Unlock()
	closeStore(t, r)
	forgetIncarnation(r.incarnationKey())

	rng := rand.New(rand.NewPCG(1, 2))
	runs := 0
	for n := 0; n <= len(events); n++ {
		u := unsynced(events, n)
		subsets := 1 << len(u)
		if len(u) > 10 {
			subsets = 1 << 10
		}
		for s := range subsets {
			lost := map[int]bool{}
			for b, i := range u {
				if len(u) > 10 {
					lost[i] = rng.IntN(2) == 0
				} else {
					lost[i] = s&(1<<b) != 0
				}
			}
			disk := afterLoss(t, base, events, n, lost)
			re, err := NewDatastore(disk, ds.NewKey("crdttest"), newFetchThrough(), &nullBroadcaster{}, onManual(replicaOptions(noAutoCompact(), "reopened: ")))
			if err != nil {
				t.Fatalf("power off after event %d, losing %v of %v: the replica does not open: %v", n, lost, u, err)
			}
			got := map[string]string{}
			if err := scanStore(ctx, re.store, ns, func(k string, v []byte) error {
				got[strings.TrimPrefix(k, ns.String()+"/")] = string(v)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, old) && !maps.Equal(got, want) {
				t.Fatalf("power off after event %d, losing %v of %v: the replica holds %v, neither %v nor %v", n, lost, u, got, old, want)
			}
			closeStore(t, re)
			forgetIncarnation(re.incarnationKey())
			runs++
		}
	}
	t.Logf("%d events, %d losses", len(events), runs)
}

func onManual(o *Options) *Options {
	o.manual = true
	return o
}
