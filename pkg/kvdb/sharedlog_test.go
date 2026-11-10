package kvdb

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/taubyte/tau/pkg/pebbleds"
)

type prefixOnly struct{ ds.Datastore }

func (prefixOnly) KeepsPrefix() bool { return false }

type loggingDAG struct {
	ipld.DAGService
	log ds.Datastore
}

func (d loggingDAG) BlockLog() ds.Datastore { return d.log }

type loggingSyncingDAG struct{ loggingDAG }

func (loggingSyncingDAG) SyncBlocks(context.Context) error { return nil }

func TestAStoreAndADAGShareALog(t *testing.T) {
	pst, err := pebbleds.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer pst.Close()
	other := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	type testCase struct {
		name  string
		store ds.Datastore
		dags  ipld.DAGService
		want  bool
	}
	for _, c := range []testCase{
		{"pebbleds and its blocks", pst, loggingDAG{dag, pst}, true},
		{"blocks in another store", pst, loggingDAG{dag, other}, false},
		{"blocks kept apart until synced", pst, loggingSyncingDAG{loggingDAG{dag, pst}}, false},
		{"a DAG that names no log", pst, dag, false},
		{"a store that keeps no prefix", other, loggingDAG{dag, other}, false},
		{"a store that says it keeps none", prefixOnly{other}, loggingDAG{dag, prefixOnly{other}}, false},
	} {
		if got := sharesLog(c.store, c.dags); got != c.want {
			t.Errorf("%s: shares a log %v, want %v", c.name, got, c.want)
		}
	}
}

// pebbleNode is a node's DAG over its pebbleds store, which it names as its
// log. onBlock, when set, runs after each block it writes.
type pebbleNode struct {
	nodeFetchThrough
	log     *pebbleds.Store
	onBlock func()
}

func (n *pebbleNode) BlockLog() ds.Datastore { return n.log }

func (n *pebbleNode) WriteBlock(ctx context.Context, b blocks.Block) error {
	err := n.nodeFetchThrough.WriteBlock(ctx, b)
	if n.onBlock != nil {
		n.onBlock()
	}
	return err
}

func openPebbleNode(t testing.TB, fs vfs.FS) *pebbleNode {
	t.Helper()
	pst, err := pebbleds.Open("db", pebbleds.WithPebbleOptions(&pebble.Options{FS: fs}))
	if err != nil {
		t.Fatal(err)
	}
	return &pebbleNode{nodeFetchThrough: newFetchThroughOn(t, blockstore.NewIdStore(pst.Blockstore())), log: pst}
}

// Power is lost as each block is written and after each step, keeping none,
// some or all of what was not synced; each copy must reopen whole.
func TestASharedLogSurvivesAPowerLossOnPebble(t *testing.T) {
	fs := vfs.NewCrashableMem()
	n := openPebbleNode(t, fs)
	r := manualReplica(t, n.log, n, "pebble: ")
	if !r.logged {
		t.Fatal("a replica over a node's store and DAG does not order by its log")
	}
	peerDag := newFetchThrough()
	peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
	defer peer.Close()
	n.setRemotes(peerDag.DAGService)
	put(t, r, "/a", "old")
	put(t, r, "/b", "old")
	compact(t, r)
	put(t, peer, "/d", "peer")

	states := []map[string][]byte{{"/a": []byte("old"), "/b": []byte("old")}}
	type step struct {
		name string
		do   func()
		then map[string][]byte
	}
	steps := []step{
		{"put", func() { put(t, r, "/a", "new") }, map[string][]byte{"/a": []byte("new"), "/b": []byte("old")}},
		{"delete", func() { del(t, r, "/b") }, map[string][]byte{"/a": []byte("new")}},
		{"put", func() { put(t, r, "/c", "x") }, map[string][]byte{"/a": []byte("new"), "/c": []byte("x")}},
		{"merge", func() { syncReplicaHeads(t, peer, r) }, map[string][]byte{"/a": []byte("new"), "/c": []byte("x"), "/d": []byte("peer")}},
	}
	type crash struct {
		fs   *vfs.MemFS
		at   string
		may  []map[string][]byte
		seed uint64
	}
	var crashes []crash
	rng := rand.New(rand.NewPCG(5, 6))
	for i, s := range steps {
		states = append(states, s.then)
		may := states[max(0, i-1):]
		powerOff := func(at string) {
			for _, keep := range []int{0, 50, 100} {
				seed := rng.Uint64()
				crashes = append(crashes, crash{
					fs:   fs.CrashClone(vfs.CrashCloneCfg{UnsyncedDataPercent: keep, RNG: rand.New(rand.NewPCG(seed, 0))}),
					at:   fmt.Sprintf("%s %d %s, keeping %d%% of what was not synced", s.name, i, at, keep),
					may:  may,
					seed: seed,
				})
			}
		}
		n.onBlock = func() { powerOff("as a block is written") }
		s.do()
		n.onBlock = nil
		powerOff("once done")
	}
	closeStore(t, r)
	if err := n.log.Close(); err != nil {
		t.Fatal(err)
	}
	forgetIncarnation(r)

	for _, c := range crashes {
		re := openPebbleNode(t, c.fs)
		re.setRemotes(peerDag.DAGService)
		reopened, err := NewDatastore(re.log, ds.NewKey("crdttest"), re, &nullBroadcaster{}, onManual(replicaOptions(noAutoCompact(), "reopened: ")))
		if err != nil {
			t.Fatalf("power off in %s: the replica does not open: %v", c.at, err)
		}
		got := queryAll(t, reopened)
		if !slices.ContainsFunc(c.may, func(s map[string][]byte) bool { return sameKV(got, s) }) {
			t.Errorf("the replica holds %q, none of %q", got, c.may)
		}
		assertNoLeak(t, reopened, re.fetchThroughDAGSvc)
		assertReach(t, reopened)
		compact(t, reopened)
		assertTreesMatch(t, reopened)
		assertNoLeak(t, reopened, re.fetchThroughDAGSvc)
		if t.Failed() {
			t.Fatalf("power off in %s (seed %d)", c.at, c.seed)
		}
		closeStore(t, reopened)
		forgetIncarnation(reopened)
		if err := re.log.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d power losses", len(crashes))
}
