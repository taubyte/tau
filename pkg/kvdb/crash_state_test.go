package kvdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
)

var errCrash = errors.New("crashed")

// A crashPoint counts a replica's datastore and blockstore writes and, at
// write at, copies both: what a process dying just before that write leaves.
// That write and every one after fail. With lose it is a power loss: the copy
// keeps only what a Sync made durable; with some too, it keeps a subset of
// the unsynced writes, and a Sync is a crash point as well.
type crashPoint struct {
	t     testing.TB
	mu    sync.Mutex
	n, at int
	store ds.Datastore
	dag   *fetchThroughDAGSvc

	lose          bool
	some          *rand.Rand
	durable       ds.Datastore
	durableBlocks *fetchThroughDAGSvc
	pending       []volatileEvent
	pendingBlocks []blockEvent

	disk    ds.Datastore
	blocks  *fetchThroughDAGSvc
	crashed bool
}

type blockEvent struct {
	nd      ipld.Node
	removed cid.Cid
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
		if c.lose {
			c.disk, c.blocks = copyStore(c.t, c.durable), copyBlocks(c.t, c.durableBlocks)
			c.keepSome()
		} else {
			c.disk, c.blocks = copyStore(c.t, c.store), copyBlocks(c.t, c.dag)
		}
		return errCrash
	}
	return nil
}

func (c *crashPoint) syncPoint() error {
	if c.some != nil {
		return c.step()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.crashed {
		return errCrash
	}
	return nil
}

func (c *crashPoint) arm(at int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n, c.at = 0, at
	if c.lose && at >= 0 {
		c.durable, c.durableBlocks = copyStore(c.t, c.store), copyBlocks(c.t, c.dag)
		c.pending, c.pendingBlocks = nil, nil
	}
}

func (c *crashPoint) wrote(e volatileEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lose && c.durable != nil {
		c.pending = append(c.pending, e)
	}
}

func (c *crashPoint) wroteBlock(e blockEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lose && c.durable != nil {
		c.pendingBlocks = append(c.pendingBlocks, e)
	}
}

// The caller holds c.mu.
func (c *crashPoint) keepSome() {
	if c.some == nil {
		return
	}
	for _, e := range c.pending {
		if c.some.IntN(2) == 0 {
			applyEvent(c.t, c.disk, e)
		}
	}
	for _, e := range c.pendingBlocks {
		if c.some.IntN(2) == 0 {
			applyBlockEvent(c.t, c.blocks, e)
		}
	}
}

func covers(prefix ds.Key, e volatileEvent) bool {
	return prefix.String() == "/" || prefix == e.key || prefix.IsAncestorOf(e.key)
}

func applyEvent(t testing.TB, d ds.Datastore, e volatileEvent) {
	t.Helper()
	ctx := context.Background()
	var err error
	switch {
	case e.del:
		err = d.Delete(ctx, e.key)
	default:
		err = d.Put(ctx, e.key, e.value)
	}
	if err != nil {
		t.Error(err)
	}
}

func applyBlockEvent(t testing.TB, d *fetchThroughDAGSvc, e blockEvent) {
	t.Helper()
	ctx := context.Background()
	var err error
	if e.nd != nil {
		err = d.DAGService.Add(ctx, e.nd)
	} else {
		err = d.DAGService.Remove(ctx, e.removed)
	}
	if err != nil {
		t.Error(err)
	}
}

func (c *crashPoint) synced(prefix ds.Key) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lose || c.durable == nil || c.crashed {
		return
	}
	kept := c.pending[:0]
	for _, e := range c.pending {
		if covers(prefix, e) {
			applyEvent(c.t, c.durable, e)
		} else {
			kept = append(kept, e)
		}
	}
	c.pending = kept
}

func (c *crashPoint) syncedBlocks() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lose || c.durable == nil || c.crashed {
		return
	}
	for _, e := range c.pendingBlocks {
		applyBlockEvent(c.t, c.durableBlocks, e)
	}
	c.pendingBlocks = nil
}

// crashStore has no batches, so a batch commits one write at a time and a
// crash can land between any two.
type crashStore struct {
	ds.Datastore
	c *crashPoint
}

func (s *crashStore) Put(ctx context.Context, k ds.Key, v []byte) error {
	if err := s.c.step(); err != nil {
		return err
	}
	s.c.wrote(volatileEvent{key: k, value: bytes.Clone(v)})
	return s.Datastore.Put(ctx, k, v)
}

func (s *crashStore) Delete(ctx context.Context, k ds.Key) error {
	if err := s.c.step(); err != nil {
		return err
	}
	s.c.wrote(volatileEvent{key: k, del: true})
	return s.Datastore.Delete(ctx, k)
}

func (s *crashStore) Sync(ctx context.Context, prefix ds.Key) error {
	if err := s.c.syncPoint(); err != nil {
		return err
	}
	s.c.synced(prefix)
	return s.Datastore.Sync(ctx, prefix)
}

type crashDAG struct {
	*fetchThroughDAGSvc
	c *crashPoint
}

func (d *crashDAG) Add(ctx context.Context, nd ipld.Node) error {
	if err := d.c.step(); err != nil {
		return err
	}
	d.c.wroteBlock(blockEvent{nd: nd})
	return d.fetchThroughDAGSvc.Add(ctx, nd)
}

// A block fetched from a remote is a write.
func (d *crashDAG) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	held := d.holds(c)
	nd, err := d.fetchThroughDAGSvc.Get(ctx, c)
	if err == nil && !held {
		d.c.wroteBlock(blockEvent{nd: nd})
	}
	return nd, err
}

func (d *crashDAG) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(cids))
	go func() {
		defer close(out)
		for _, c := range cids {
			nd, err := d.Get(ctx, c)
			out <- &ipld.NodeOption{Node: nd, Err: err}
		}
	}()
	return out
}

func (d *crashDAG) SyncBlocks(context.Context) error {
	if err := d.c.syncPoint(); err != nil {
		return err
	}
	d.c.syncedBlocks()
	return nil
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
	d.c.wroteBlock(blockEvent{removed: c})
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

func assertReach(t testing.TB, r *Datastore) {
	t.Helper()
	ctx := context.Background()
	r.gcMu.Lock()
	defer r.gcMu.Unlock()
	local, _ := r.dagService.(interface{ holds(cid.Cid) bool })
	want := map[cid.Cid]bool{}
	var stack []cid.Cid
	root := func(c cid.Cid) {
		if c.Defined() && !want[c] {
			want[c] = true
			stack = append(stack, c)
		}
	}
	err := scanStore(ctx, r.store, r.blocksPrefix(), func(_ string, v []byte) error {
		b, err := decodeBlock(v)
		root(b.state)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scanStore(ctx, r.store, r.pinPrefix(), func(_ string, v []byte) error {
		c, err := cid.Cast(v)
		root(c)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	root(r.base.elems)
	root(r.base.pending)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if local != nil && !local.holds(c) {
			t.Errorf("state block %s a root reaches is gone", c)
			continue
		}
		nd, err := r.dagService.Get(ctx, c)
		if err != nil {
			t.Fatalf("state block %s a root reaches: %s", c, err)
		}
		for _, l := range blockLinksOf(t, nd) {
			root(l)
		}
	}
	got := map[cid.Cid]bool{}
	for k := range r.gc.members {
		got[cidOf(k)] = true
	}
	if !maps.Equal(got, want) {
		for c := range want {
			if !got[c] {
				t.Errorf("%s: the roots reach it, the members do not hold it", c)
			}
		}
		for c := range got {
			if !want[c] {
				t.Errorf("%s: a member no root reaches", c)
			}
		}
	}
	if n, err := countPrefix(ctx, r.store, r.manifestPrefix()); err != nil || n != 0 || len(r.gc.manifests) != 0 {
		t.Errorf("%d manifest chunks are left (%d not drained): %v", n, len(r.gc.manifests), err)
	}
	if err := scanStore(ctx, r.store, r.intentPrefix(), func(k string, _ []byte) error {
		s, _, err := parseChunkKey(r.intentPrefix(), k)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(r.gc.sessions, func(x *gcSession) bool { return x.id == s }) {
			t.Errorf("intents %s are left of a session that is over", k)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

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
			if err := statetree.Diff(ctx, stateTree(gov, pair[0]), stateTree(wov, pair[1]), func(ch statetree.Change) error {
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

func TestACrashAtAnyWriteRecovers(t *testing.T) {
	for _, m := range crashModes {
		t.Run(m.String(), func(t *testing.T) { crashAtAnyWrite(t, m) })
	}
}

// What a crash keeps of the writes no Sync covered: all (a process dying),
// none, or some.
type crashMode int

const (
	keptAll crashMode = iota
	lostAll
	lostSome
)

var crashModes = []crashMode{keptAll, lostAll, lostSome}

func (m crashMode) String() string { return [...]string{"kept", "lost", "some lost"}[m] }

func crashAt(t testing.TB, m crashMode, at int, store ds.Datastore, dag *fetchThroughDAGSvc) *crashPoint {
	c := &crashPoint{t: t, at: -1, store: store, dag: dag, lose: m != keptAll}
	if m == lostSome {
		c.some = rand.New(rand.NewPCG(uint64(at), 1))
	}
	return c
}

func crashAtAnyWrite(t *testing.T, m crashMode) {
	ctx := context.Background()
	for at := 0; at < 20000; at++ {
		inner := dssync.MutexWrap(ds.NewMapDatastore())
		own := newFetchThrough()
		c := crashAt(t, m, at, inner, own)
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
			assertReach(t, r)
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
			assertReach(t, r)
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
		assertReach(t, re)
		compact(t, re)
		assertTreesMatch(t, re)
		assertReach(t, re)
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

type stateFault struct{ op, prefix string }

func stateFaults() []stateFault {
	keyed := []string{"Get", "Put", "Delete", "Has", "Query", "BatchPut", "BatchDelete"}
	prefixes := []string{"/j/", "/i/", "/g/", "/p/", "/x/", "/sr", "/w", "/b/", "/s/e/", "/s/t/", "/c/"}
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

			// Reopened with no repair loop of its own to race the walk below.
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
			assertReach(t, r)
			assertNoLeak(t, r, own.fetchThroughDAGSvc)
		})
	}
}

func TestACrashInAResumedFetchRecovers(t *testing.T) {
	for _, m := range crashModes {
		t.Run(m.String(), func(t *testing.T) { crashInAResumedFetch(t, m) })
	}
}

func crashInAResumedFetch(t *testing.T, m crashMode) {
	ctx := context.Background()
	peer, peerDag := widePeer(t, 100, 4)
	defer peer.Close()
	per := len(stateClosure(t, peer))/3 + 1
	for at := 0; at < 20000; at++ {
		inner := dssync.MutexWrap(ds.NewMapDatastore())
		own := newFetchThrough()
		c := crashAt(t, m, at, inner, own)
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
			assertReach(t, r)
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
		assertReach(t, re)
		syncReplicaHeads(t, peer, re)
		if err := re.Repair(ctx); err != nil {
			t.Fatalf("crash at write %d: %v", at, err)
		}
		compact(t, re)
		if got := queryAll(t, re); !sameKV(got, after) {
			t.Fatalf("crash at write %d: caught up again, the replica holds %d keys, want %d", at, len(got), len(after))
		}
		assertTreesMatch(t, re)
		assertReach(t, re)
		assertNoLeak(t, re, c.blocks)
		if t.Failed() {
			t.Fatalf("crash at write %d", at)
		}
		re.Close()
	}
	t.Fatal("the run never ended")
}

func TestAStateStoreFaultInAResumedFetchIsSurvived(t *testing.T) {
	ctx := context.Background()
	for _, f := range stateFaults() {
		t.Run(f.op+f.prefix, func(t *testing.T) {
			store := newFaultyDatastore(ds.NewMapDatastore())
			own := &removeFailing{fetchThroughDAGSvc: newFetchThrough()}
			r := manualReplica(t, store, own, "faulty: ")
			peer, peerDag := widePeer(t, 200, 6)
			defer peer.Close()
			link := newRationed(peerDag.DAGService)
			own.setRemotes(link)
			for i := range 10 {
				put(t, r, fmt.Sprintf("/r%02d", i), fmt.Sprintf("v%d", i))
			}
			compact(t, r)
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
			assertReach(t, r)
			assertNoLeak(t, r, own.fetchThroughDAGSvc)
		})
	}
}

// A volatileStore keeps unsynced writes in no order: a power loss keeps any
// subset of them and every write a Sync covered. It records each write and
// Sync while on.
type volatileStore struct {
	ds.Datastore
	mu     sync.Mutex
	on     bool
	events []volatileEvent
}

// A Put, a Delete, or with sync a Sync of key; with nd or removed, a block
// written or removed; with blocks, a Sync of the blocks.
type volatileEvent struct {
	key       ds.Key
	value     []byte
	del, sync bool

	nd      ipld.Node
	removed cid.Cid
	blocks  bool
}

func (e volatileEvent) block() bool { return e.nd != nil || e.removed.Defined() }

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

// A block is durable once the blocks are synced or, kept in the datastore,
// once all of it is.
func unsynced(events []volatileEvent, n int) []int {
	var out []int
	for i := range n {
		e := events[i]
		if e.sync || e.blocks {
			continue
		}
		covered := false
		for _, s := range events[i+1 : n] {
			if e.block() {
				covered = covered || s.blocks || s.sync && s.key.String() == "/"
			} else {
				covered = covered || s.sync && covers(s.key, e)
			}
		}
		if !covered {
			out = append(out, i)
		}
	}
	return out
}

// In a store that keeps a prefix of its writes, a Sync of any key makes every
// write before it durable.
func unsyncedTail(events []volatileEvent, n int) []int {
	var out []int
	for i := range n {
		if events[i].sync || events[i].blocks {
			out = out[:0]
			continue
		}
		out = append(out, i)
	}
	return out
}

func afterLoss(t testing.TB, base ds.Datastore, events []volatileEvent, n int, lost map[int]bool) ds.Datastore {
	t.Helper()
	disk := copyStore(t, base)
	for i, e := range events[:n] {
		if !e.sync && !e.blocks && !e.block() && !lost[i] {
			applyEvent(t, disk, e)
		}
	}
	return disk
}

func blocksAfterLoss(t testing.TB, base *fetchThroughDAGSvc, events []volatileEvent, n int, lost map[int]bool) *fetchThroughDAGSvc {
	t.Helper()
	blocks := copyBlocks(t, base)
	for i, e := range events[:n] {
		if e.block() && !lost[i] {
			applyBlockEvent(t, blocks, blockEvent{nd: e.nd, removed: e.removed})
		}
	}
	return blocks
}

// volatileDAG keeps its blocks in its store's datastore, synced with all of
// it; syncingVolatileDAG keeps them apart, synced by SyncBlocks.
type volatileDAG struct {
	*fetchThroughDAGSvc
	s *volatileStore
}

func (d *volatileDAG) Add(ctx context.Context, nd ipld.Node) error {
	d.s.record(volatileEvent{nd: nd})
	return d.fetchThroughDAGSvc.Add(ctx, nd)
}

func (d *volatileDAG) AddMany(ctx context.Context, nds []ipld.Node) error {
	for _, nd := range nds {
		if err := d.Add(ctx, nd); err != nil {
			return err
		}
	}
	return nil
}

// A block fetched from a remote is a write.
func (d *volatileDAG) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	held := d.holds(c)
	nd, err := d.fetchThroughDAGSvc.Get(ctx, c)
	if err == nil && !held {
		d.s.record(volatileEvent{nd: nd})
	}
	return nd, err
}

func (d *volatileDAG) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(cids))
	go func() {
		defer close(out)
		for _, c := range cids {
			nd, err := d.Get(ctx, c)
			out <- &ipld.NodeOption{Node: nd, Err: err}
		}
	}()
	return out
}

func (d *volatileDAG) Remove(ctx context.Context, c cid.Cid) error {
	d.s.record(volatileEvent{removed: c})
	return d.fetchThroughDAGSvc.Remove(ctx, c)
}

func (d *volatileDAG) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	for _, c := range cids {
		if err := d.Remove(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

type syncingVolatileDAG struct{ *volatileDAG }

// A prefixStore loses only a suffix of the unsynced writes, as pebble's log
// does; loggedDAG writes its blocks to it, as a node's DAG does.
type prefixStore struct{ *volatileStore }

func (prefixStore) KeepsPrefix() bool { return true }

type loggedDAG struct {
	*volatileDAG
	log prefixStore
}

func (d loggedDAG) BlockLog() ds.Datastore { return d.log }

func (d syncingVolatileDAG) SyncBlocks(context.Context) error {
	d.s.record(volatileEvent{blocks: true})
	return nil
}

func TestAWriteSurvivesAnyLoss(t *testing.T) {
	t.Run("blocks apart", func(t *testing.T) { writeSurvivesAnyLoss(t, blocksApart) })
	t.Run("blocks in the datastore", func(t *testing.T) { writeSurvivesAnyLoss(t, blocksIn) })
	t.Run("a shared log", func(t *testing.T) { writeSurvivesAnyLoss(t, sharedLog) })
}

type blockPlace int

const (
	blocksApart blockPlace = iota
	blocksIn
	sharedLog
)

func (p blockPlace) String() string {
	return [...]string{"blocks apart", "blocks in the datastore", "a shared log"}[p]
}

func writeSurvivesAnyLoss(t *testing.T, place blockPlace) {
	inner := dssync.MutexWrap(ds.NewMapDatastore())
	vs := &volatileStore{Datastore: inner}
	own := newFetchThrough()
	vd := &volatileDAG{fetchThroughDAGSvc: own, s: vs}
	var (
		d     ipld.DAGService = vd
		store ds.Datastore    = vs
	)
	switch place {
	case blocksApart:
		d = syncingVolatileDAG{vd}
	case sharedLog:
		log := prefixStore{vs}
		d, store = loggedDAG{volatileDAG: vd, log: log}, log
	}
	r := manualReplica(t, store, d, "volatile: ")
	if r.logged != (place == sharedLog) {
		t.Fatalf("a replica over %v orders by a shared log: %v", place, r.logged)
	}
	peerDag := newFetchThrough()
	peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
	defer peer.Close()
	own.setRemotes(peerDag.DAGService)
	put(t, r, "/a", "old")
	put(t, r, "/b", "old")
	compact(t, r)
	put(t, peer, "/c", "peer")
	base, baseBlocks := copyStore(t, inner), copyBlocks(t, own)
	vs.mu.Lock()
	vs.on = true
	vs.mu.Unlock()
	put(t, r, "/a", "new")
	vs.mu.Lock()
	putEvents := slices.Clone(vs.events)
	vs.mu.Unlock()
	assertPutSyncs(t, place, putEvents)
	del(t, r, "/b")
	syncReplicaHeads(t, peer, r)
	vs.mu.Lock()
	vs.on = false
	events := vs.events
	vs.mu.Unlock()
	closeStore(t, r)
	forgetIncarnation(r.incarnationKey())
	states := []map[string][]byte{
		{"/a": []byte("old"), "/b": []byte("old")},
		{"/a": []byte("new"), "/b": []byte("old")},
		{"/a": []byte("new")},
		{"/a": []byte("new"), "/c": []byte("peer")},
	}

	rng := rand.New(rand.NewPCG(3, 4))
	runs := 0
	for n := 0; n <= len(events); n++ {
		u := unsynced(events, n)
		losses := 1 << min(len(u), 10)
		if place == sharedLog {
			u = unsyncedTail(events, n)
			losses = len(u) + 1
		}
		for s := range losses {
			lost := map[int]bool{}
			for b, i := range u {
				switch {
				case place == sharedLog:
					lost[i] = b >= s
				case len(u) > 10:
					lost[i] = rng.IntN(2) == 0
				default:
					lost[i] = s&(1<<b) != 0
				}
			}
			disk, blocks := afterLoss(t, base, events, n, lost), blocksAfterLoss(t, baseBlocks, events, n, lost)
			blocks.setRemotes(peerDag.DAGService)
			re, err := NewDatastore(disk, ds.NewKey("crdttest"), blocks, &nullBroadcaster{}, onManual(replicaOptions(noAutoCompact(), "reopened: ")))
			if err != nil {
				t.Fatalf("power off after event %d, losing %v of %v: the replica does not open: %v", n, lost, u, err)
			}
			got := queryAll(t, re)
			if !slices.ContainsFunc(states, func(s map[string][]byte) bool { return sameKV(got, s) }) {
				t.Errorf("the replica holds %q, none of %q", got, states)
			}
			assertNoLeak(t, re, blocks)
			assertReach(t, re)
			compact(t, re)
			assertTreesMatch(t, re)
			assertNoLeak(t, re, blocks)
			if t.Failed() {
				t.Fatalf("power off after event %d, losing %v of %v", n, lost, u)
			}
			closeStore(t, re)
			forgetIncarnation(re.incarnationKey())
			runs++
		}
	}
	t.Logf("%d events, %d losses", len(events), runs)
}

// In a shared log a put syncs once, for its journal marks, after its block,
// so a head is never broadcast naming a block a power loss can take.
func assertPutSyncs(t *testing.T, place blockPlace, events []volatileEvent) {
	t.Helper()
	syncs, block, synced := 0, -1, false
	for i, e := range events {
		switch {
		case e.nd != nil && block < 0:
			block = i
		case e.sync || e.blocks:
			syncs++
			synced = synced || block >= 0
		}
	}
	want := [...]int{blocksApart: 4, blocksIn: 3, sharedLog: 1}[place]
	if syncs != want || block < 0 || !synced {
		t.Fatalf("a put over %v syncs %d times, a block at %d, synced after it: %v; want %d Syncs, one after the block", place, syncs, block, synced, want)
	}
}

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

func assertHoldsExactly(t testing.TB, r *Datastore, d *fetchThroughDAGSvc) {
	t.Helper()
	ctx := context.Background()
	assertReach(t, r)
	r.gcMu.Lock()
	defer r.gcMu.Unlock()
	ch, err := d.bs.AllKeysChan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for k := range ch {
		c := cid.NewCidV1(cid.Raw, k.Hash())
		indexed, err := r.store.Has(ctx, r.blockKey(c))
		if err != nil {
			t.Fatal(err)
		}
		if indexed || r.gc.member(keyOf(c)) || r.gc.holds(keyOf(c)) {
			continue
		}
		t.Errorf("block %s is held, and no root reaches it, nothing indexes it and no session holds it", c)
	}
}

func abandonFetches(ctx context.Context, r *Datastore) {
	r.fetchMu.Lock()
	var dead []*stateFetch
	for _, f := range r.fetches {
		if f.users == 0 {
			dead = append(dead, f)
		}
	}
	for _, f := range dead {
		r.dropFetchLocked(f)
	}
	r.fetchMu.Unlock()
	for _, f := range dead {
		r.releaseFetch(ctx, f)
	}
}

func TestTheCollectorKeepsExactlyWhatIsReached(t *testing.T) {
	for seed := range uint64(16) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { collectorRun(t, seed, 200) })
	}
}

func collectorRun(t *testing.T, seed uint64, steps int) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(seed, 99))
	inner := dssync.MutexWrap(ds.NewMapDatastore())
	own := newFetchThrough()
	c := &crashPoint{t: t, at: -1, store: inner, dag: own}
	r := manualReplica(t, &crashStore{Datastore: inner, c: c}, &crashDAG{fetchThroughDAGSvc: own, c: c}, "r: ")
	peerDag := newFetchThrough()
	peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
	defer peer.Close()
	link := newRationed(peerDag.DAGService)
	own.setRemotes(link)
	peerDag.setRemotes(own.DAGService)
	var pins []*statePin
	defer func() { r.Close() }()

	op := func(kind int) error {
		switch kind {
		case 0:
			for range 1 + rng.IntN(6) {
				k := fmt.Sprintf("/r%02d", rng.IntN(40))
				if rng.IntN(4) == 0 {
					if err := r.Delete(ctx, ds.NewKey(k)); err != nil {
						return err
					}
				} else if err := r.Put(ctx, ds.NewKey(k), []byte(fmt.Sprint(rng.Uint32()))); err != nil {
					return err
				}
			}
		case 1:
			for range 1 + rng.IntN(20) {
				k := ds.NewKey(fmt.Sprintf("/p%02d", rng.IntN(60)))
				if rng.IntN(4) == 0 {
					if err := peer.Delete(ctx, k); err != nil {
						t.Fatal(err)
					}
				} else if err := peer.Put(ctx, k, []byte(fmt.Sprint(rng.Uint32()))); err != nil {
					t.Fatal(err)
				}
			}
			compact(t, peer)
		case 2:
			_, err := r.Compact(ctx)
			return err
		case 3:
			link.refill(1 << 30)
			hs, _ := peer.heads.List()
			for _, h := range hs {
				if err := r.handleBlock(ctx, h); err != nil {
					return err
				}
			}
		case 4:
			link.refill(1 + rng.IntN(4))
			hs, _ := peer.heads.List()
			for _, h := range hs {
				if err := r.handleBlock(ctx, h); err != nil {
					t.Logf("partial fetch: %v", err)
				}
			}
			if rng.IntN(2) == 0 {
				abandonFetches(ctx, r)
			}
		case 5:
			r.gcMu.Lock()
			var states []cid.Cid
			for _, lr := range r.gc.roots {
				if lr.kind == stateRootKind {
					states = append(states, lr.c)
				}
			}
			r.gcMu.Unlock()
			if len(states) == 0 {
				return nil
			}
			slices.SortFunc(states, func(a, b cid.Cid) int { return strings.Compare(a.KeyString(), b.KeyString()) })
			lease := time.Hour
			if rng.IntN(2) == 0 {
				lease = 0
			}
			p, err := r.pinState(ctx, states[rng.IntN(len(states))], lease)
			if errors.Is(err, errTooManyPins) {
				return nil
			}
			if err != nil {
				return err
			}
			pins = append(pins, p)
		case 6:
			if len(pins) == 0 {
				return nil
			}
			i := rng.IntN(len(pins))
			if err := pins[i].Release(ctx); err != nil {
				return err
			}
			pins = slices.Delete(pins, i, i+1)
		}
		return nil
	}

	for step := range steps {
		kind := rng.IntN(7)
		crash := rng.IntN(5) == 0
		if crash {
			c.arm(rng.IntN(120))
		}
		err := op(kind)
		if crash {
			c.mu.Lock()
			crashed := c.crashed
			c.mu.Unlock()
			if crashed {
				t.Logf("step %d: crash in op %d: %v", step, kind, err)
				r.Close()
				if err := c.disk.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
					t.Fatal(err)
				}
				forgetIncarnation(r.incarnationKey())
				own = c.blocks
				own.setRemotes(link)
				c = &crashPoint{t: t, at: -1, store: c.disk, dag: own}
				r = manualReplica(t, &crashStore{Datastore: c.store, c: c}, &crashDAG{fetchThroughDAGSvc: own, c: c}, "reopened: ")
				pins = nil
				err = nil
			} else {
				c.arm(-1)
			}
		}
		if err != nil {
			t.Fatalf("step %d, op %d: %v", step, kind, err)
		}
		if _, err := r.reclaim(ctx); err != nil {
			t.Fatalf("step %d, op %d: reclaim: %v", step, kind, err)
		}
		assertHoldsExactly(t, r, own)
		assertTreesMatch(t, r)
		if t.Failed() {
			t.Fatalf("step %d, op %d", step, kind)
		}
	}
}
