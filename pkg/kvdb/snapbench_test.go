package kvdb

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipfs/boxo/bitswap"
	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	blockstore "github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/helpers"
	"github.com/taubyte/tau/p2p/keypair"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/pebbleds"
)

// The snapshot benchmarks measure what a snapshot costs: to write after a
// small change, to hold, to fetch for a peer one snapshot behind, under a
// steady write stream, and when only time has passed. Blocks and bytes are
// counted where the replica's DAG service sees them.
// Benchmark names and workloads do not depend on how a snapshot is laid
// out, so runs before and after a change to it compare with benchstat.

// snapReplica is one replica, as the benchmarks drive it.
type snapReplica interface {
	// PutAll writes n keys in one batch.
	PutAll(ctx context.Context, n int, kv func(i int) (string, []byte)) error
	Put(ctx context.Context, key string, value []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	// Compact folds the replica's state into a snapshot.
	Compact(ctx context.Context) error
	// Maintain is one pass of what time does to the replica.
	Maintain(ctx context.Context) error
	// CatchUp merges every head of from, fetching what it lacks through
	// its DAG service.
	CatchUp(ctx context.Context, from snapReplica) error
	Close() error
}

// snapEnv is what a replica is opened with. Every block the replica
// writes or fetches goes through dag, where the benchmarks count it; dir is
// the replica's own, for its datastore; opts carries the clock, the
// retention and CompactAfterBlocks.
type snapEnv struct {
	name string
	dir  string
	// store, when set, is the store the replica runs on, else one opened
	// at dir.
	store *pebbleds.Store
	dag   ipld.DAGService
	opts  *Options
}

const (
	snapValueSize = 100
	snapKeyWidth  = len("/k/") + 10
)

var (
	snapSizes   = []int{1_000, 10_000, 100_000}
	snapChanges = []int{1, 10, 100}
)

// stalledAttempts is how many repairs in a row that fetch no state block
// end a catch-up.
const stalledAttempts = 3

// snapStore is a Datastore over its own pebble store, which counts what it
// writes.
type snapStore struct {
	r  *Datastore
	st *pebbleds.Store
}

func openSnap(tb testing.TB, env snapEnv) snapReplica {
	tb.Helper()
	pst := env.store
	if pst == nil {
		var err error
		if pst, err = pebbleds.Open(env.dir); err != nil {
			tb.Fatal(err)
		}
	}
	r, err := NewDatastore(pst, ds.NewKey("snapbench"), env.dag, &nullBroadcaster{}, env.opts)
	if err != nil {
		pst.Close()
		tb.Fatal(err)
	}
	return &snapStore{r: r, st: pst}
}

func (f *snapStore) PutAll(ctx context.Context, n int, kv func(i int) (string, []byte)) error {
	bt, err := f.r.Batch(ctx)
	if err != nil {
		return err
	}
	for i := range n {
		k, v := kv(i)
		if err := bt.Put(ctx, ds.RawKey(k), v); err != nil {
			return err
		}
	}
	return bt.Commit(ctx)
}

func (f *snapStore) Put(ctx context.Context, key string, value []byte) error {
	return f.r.Put(ctx, ds.RawKey(key), value)
}

func (f *snapStore) Get(ctx context.Context, key string) ([]byte, error) {
	return f.r.Get(ctx, ds.RawKey(key))
}

func (f *snapStore) Compact(ctx context.Context) error {
	_, err := f.r.Compact(ctx)
	return err
}

func (f *snapStore) Maintain(ctx context.Context) error { return f.r.maintain(ctx) }

// CatchUp walks from's heads, then repairs as the repair loop would until
// nothing is missing: a state larger than one fetch attempt arrives over
// several. It gives up after stalledAttempts repairs in a row that fetch
// no state block.
func (f *snapStore) CatchUp(ctx context.Context, from snapReplica) error {
	hs, _ := from.(*snapStore).r.heads.List()
	var err error
	for _, h := range hs {
		err = errors.Join(err, f.r.handleBlock(ctx, h))
	}
	for stalled := 0; f.r.IsDirty(ctx); {
		before := f.r.stateFetched(ctx)
		if err = f.r.Repair(ctx); err == nil {
			break
		}
		if f.r.stateFetched(ctx) > before {
			stalled = 0
		} else if stalled++; stalled == stalledAttempts {
			return err
		}
	}
	return err
}

func (f *snapStore) Close() error { return errors.Join(f.r.Close(), f.st.Close()) }

// dagMeter counts the blocks passing through a DAG service, and their bytes.
type dagMeter struct {
	ipld.DAGService
	// log is the store the blocks are written to, when it is the
	// replica's own, as a node's are.
	log ds.Datastore

	added, addedBytes   atomic.Int64
	got, gotBytes, miss atomic.Int64
	calls               atomic.Int64
	removed             atomic.Int64
}

func (m *dagMeter) BlockLog() ds.Datastore { return m.log }

func (m *dagMeter) countAdd(nd ipld.Node) {
	m.added.Add(1)
	m.addedBytes.Add(int64(len(nd.RawData())))
}

func (m *dagMeter) countGet(nd blocks.Block, err error) {
	if err != nil {
		m.miss.Add(1)
		return
	}
	m.got.Add(1)
	m.gotBytes.Add(int64(len(nd.RawData())))
}

// WriteBlock writes b, a node, through the DAG service's BlockWriter or its
// Add, counted as an add.
func (m *dagMeter) WriteBlock(ctx context.Context, b blocks.Block) error {
	var err error
	if w, ok := m.DAGService.(BlockWriter); ok {
		err = w.WriteBlock(ctx, b)
	} else {
		err = m.DAGService.Add(ctx, b.(ipld.Node))
	}
	if err != nil {
		return err
	}
	m.added.Add(1)
	m.addedBytes.Add(int64(len(b.RawData())))
	return nil
}

func (m *dagMeter) Add(ctx context.Context, nd ipld.Node) error {
	if err := m.DAGService.Add(ctx, nd); err != nil {
		return err
	}
	m.countAdd(nd)
	return nil
}

func (m *dagMeter) AddMany(ctx context.Context, nds []ipld.Node) error {
	if err := m.DAGService.AddMany(ctx, nds); err != nil {
		return err
	}
	for _, nd := range nds {
		m.countAdd(nd)
	}
	return nil
}

func (m *dagMeter) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	return m.get(ctx, m.DAGService, c)
}

func (m *dagMeter) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	return m.getMany(ctx, m.DAGService, cids)
}

func (m *dagMeter) ReadBlock(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	m.calls.Add(1)
	b, err := readBlock(ctx, m.DAGService, c)
	m.countGet(b, err)
	return b, err
}

// get and getMany count one call each, and every block they return.
func (m *dagMeter) get(ctx context.Context, ng ipld.NodeGetter, c cid.Cid) (ipld.Node, error) {
	m.calls.Add(1)
	nd, err := ng.Get(ctx, c)
	m.countGet(nd, err)
	return nd, err
}

func (m *dagMeter) getMany(ctx context.Context, ng ipld.NodeGetter, cids []cid.Cid) <-chan *ipld.NodeOption {
	m.calls.Add(1)
	in := ng.GetMany(ctx, cids)
	out := make(chan *ipld.NodeOption, len(cids))
	go func() {
		defer close(out)
		for o := range in {
			m.countGet(o.Node, o.Err)
			out <- o
		}
	}()
	return out
}

func (m *dagMeter) Remove(ctx context.Context, c cid.Cid) error {
	if err := m.DAGService.Remove(ctx, c); err != nil {
		return err
	}
	m.removed.Add(1)
	return nil
}

// DeleteBlocks removes cids' blocks through the DAG service's BlockRemover
// or its RemoveMany, counted as removed.
func (m *dagMeter) DeleteBlocks(ctx context.Context, cids []cid.Cid) error {
	var err error
	if r, ok := m.DAGService.(BlockRemover); ok {
		err = r.DeleteBlocks(ctx, cids)
	} else {
		err = m.DAGService.RemoveMany(ctx, cids)
	}
	if err != nil {
		return err
	}
	m.removed.Add(int64(len(cids)))
	return nil
}

func (m *dagMeter) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	if err := m.DAGService.RemoveMany(ctx, cids); err != nil {
		return err
	}
	m.removed.Add(int64(len(cids)))
	return nil
}

// meterReading is a dagMeter's counters at one moment.
type meterReading struct{ added, addedBytes, got, gotBytes, miss int64 }

func (m *dagMeter) read() meterReading {
	return meterReading{m.added.Load(), m.addedBytes.Load(), m.got.Load(), m.gotBytes.Load(), m.miss.Load()}
}

func (r meterReading) since(o meterReading) meterReading {
	return meterReading{r.added - o.added, r.addedBytes - o.addedBytes, r.got - o.got, r.gotBytes - o.gotBytes, r.miss - o.miss}
}

func (r *meterReading) add(o meterReading) {
	r.added += o.added
	r.addedBytes += o.addedBytes
	r.got += o.got
	r.gotBytes += o.gotBytes
	r.miss += o.miss
}

// snapNode is a replica with its own blockstore, metered twice: own counts
// what the replica asks of its DAG service, serve what peers fetch from it.
type snapNode struct {
	snapReplica
	local *fetchThroughDAGSvc
	own   *dagMeter
	serve *dagMeter
}

func newSnapNode(b *testing.B, name string, opts *Options) *snapNode {
	b.Helper()
	// A node's DAG: its blocks in its own store, under the identity layer,
	// written past the block service.
	pst, err := pebbleds.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	node := newFetchThroughOn(b, blockstore.NewIdStore(pst.Blockstore()))
	local := node.fetchThroughDAGSvc
	n := &snapNode{local: local, own: &dagMeter{DAGService: node, log: pst}, serve: &dagMeter{DAGService: local.DAGService}}
	n.snapReplica = openSnap(b, snapEnv{name: name, store: pst, dag: n.own, opts: replicaOptions(opts, name)})
	b.Cleanup(func() {
		if err := n.Close(); err != nil {
			b.Error(err)
		}
	})
	return n
}

// dsWritten is how many bytes n has written to its datastore.
func (n *snapNode) dsWritten() int64 { return n.snapReplica.(*snapStore).st.Written() }

// fetchFrom makes peer the only replica n fetches from.
func (n *snapNode) fetchFrom(peer *snapNode) { n.local.setRemotes(peer.serve) }

// held is how many blocks n's blockstore holds, and their bytes.
func (n *snapNode) held(b *testing.B) (int64, int64) {
	b.Helper()
	ctx := context.Background()
	ch, err := n.local.bs.AllKeysChan(ctx)
	if err != nil {
		b.Fatal(err)
	}
	var blocks, bytes int64
	for c := range ch {
		size, err := n.local.bs.GetSize(ctx, c)
		if err != nil {
			b.Fatal(err)
		}
		blocks++
		bytes += int64(size)
	}
	return blocks, bytes
}

// snapWorkload is a state of n keys with values of snapValueSize random
// bytes, and the changes made to it, all drawn from one fixed seed. Keys and
// values live in flat byte arenas, so the harness hands the collector no
// pointers to trace.
type snapWorkload struct {
	n      int
	keys   []byte
	values []byte
	change []byte
	last   int
	rng    *rand.ChaCha8
}

func newSnapWorkload(n, maxChange int) *snapWorkload {
	var seed [32]byte
	copy(seed[:], fmt.Sprintf("snapbench/%d", n))
	w := &snapWorkload{
		n:      n,
		keys:   make([]byte, n*snapKeyWidth),
		values: make([]byte, n*snapValueSize),
		change: make([]byte, max(maxChange, 1)*snapValueSize),
		rng:    rand.NewChaCha8(seed),
	}
	for i := range n {
		k := w.keys[i*snapKeyWidth : (i+1)*snapKeyWidth]
		copy(k, "/k/")
		for j, v := len(k)-1, i; j >= 3; j, v = j-1, v/10 {
			k[j] = byte('0' + v%10)
		}
	}
	w.rng.Read(w.values)
	return w
}

func (w *snapWorkload) key(i int) string {
	return string(w.keys[i*snapKeyWidth : (i+1)*snapKeyWidth])
}

func (w *snapWorkload) value(i int) []byte {
	return w.values[i*snapValueSize : (i+1)*snapValueSize]
}

func (w *snapWorkload) seed(b *testing.B, r snapReplica) {
	b.Helper()
	if err := r.PutAll(context.Background(), w.n, func(i int) (string, []byte) { return w.key(i), w.value(i) }); err != nil {
		b.Fatal(err)
	}
}

// changeKeys gives k distinct keys, spread over the state from a random
// start, a new value each, one write per key.
func (w *snapWorkload) changeKeys(b *testing.B, r snapReplica, k int) {
	b.Helper()
	ctx := context.Background()
	start, stride := int(w.rng.Uint64()%uint64(w.n)), max(w.n/k, 1)
	w.rng.Read(w.change[:k*snapValueSize])
	for j := range k {
		w.last = (start + j*stride) % w.n
		if err := r.Put(ctx, w.key(w.last), w.change[j*snapValueSize:(j+1)*snapValueSize]); err != nil {
			b.Fatal(err)
		}
	}
	copy(w.value(w.last), w.change[(k-1)*snapValueSize:k*snapValueSize])
}

// check fails unless r holds the last value the workload wrote.
func (w *snapWorkload) check(b *testing.B, r snapReplica) {
	b.Helper()
	got, err := r.Get(context.Background(), w.key(w.last))
	if err != nil {
		b.Fatal(err)
	}
	if string(got) != string(w.value(w.last)) {
		b.Fatalf("%s holds %x, want %x", w.key(w.last), got, w.value(w.last))
	}
}

func mustSnap(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}

func reportPer(b *testing.B, total int64, per int, unit string) {
	b.ReportMetric(float64(total)/float64(per), unit)
}

// BenchmarkSnapshotCompact: k keys change, then the replica compacts. An op
// is the compaction; written-* is what it wrote, snapshot-* what the
// replica holds after the last one.
func BenchmarkSnapshotCompact(b *testing.B) {
	for _, n := range snapSizes {
		for _, k := range snapChanges {
			b.Run(fmt.Sprintf("keys=%d/changed=%d", n, k), func(b *testing.B) {
				ctx := context.Background()
				r := newSnapNode(b, "a", newSimTime().options(0))
				w := newSnapWorkload(n, k)
				w.seed(b, r)
				mustSnap(b, r.Compact(ctx))
				var total meterReading
				var dsTotal int64
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					b.StopTimer()
					w.changeKeys(b, r, k)
					before, dsBefore := r.own.read(), r.dsWritten()
					b.StartTimer()
					mustSnap(b, r.Compact(ctx))
					b.StopTimer()
					total.add(r.own.read().since(before))
					dsTotal += r.dsWritten() - dsBefore
					w.check(b, r)
					b.StartTimer()
				}
				b.StopTimer()
				blocks, bytes := r.held(b)
				reportPer(b, dsTotal, b.N, "ds-written-B/op")
				reportPer(b, total.addedBytes, b.N, "written-B/op")
				reportPer(b, total.added, b.N, "written-blocks/op")
				b.ReportMetric(float64(bytes), "snapshot-B")
				b.ReportMetric(float64(blocks), "snapshot-blocks")
			})
		}
	}
}

// BenchmarkSnapshotCatchUp: replica b is one snapshot behind a, with k keys
// changed since. An op is b merging a's heads; fetched-* is what it fetched
// from a, missed-blocks what it asked for and a no longer held.
func BenchmarkSnapshotCatchUp(b *testing.B) {
	for _, n := range snapSizes {
		for _, k := range snapChanges {
			b.Run(fmt.Sprintf("keys=%d/changed=%d", n, k), func(b *testing.B) {
				ctx := context.Background()
				st := newSimTime()
				a, lag := newSnapNode(b, "a", st.options(0)), newSnapNode(b, "b", st.options(0))
				lag.fetchFrom(a)
				w := newSnapWorkload(n, k)
				w.seed(b, a)
				mustSnap(b, a.Compact(ctx))
				mustSnap(b, lag.CatchUp(ctx, a.snapReplica))
				var total meterReading
				var dsTotal int64
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					b.StopTimer()
					w.changeKeys(b, a, k)
					mustSnap(b, a.Compact(ctx))
					before, dsBefore := a.serve.read(), lag.dsWritten()
					b.StartTimer()
					mustSnap(b, lag.CatchUp(ctx, a.snapReplica))
					b.StopTimer()
					total.add(a.serve.read().since(before))
					dsTotal += lag.dsWritten() - dsBefore
					w.check(b, lag)
					b.StartTimer()
				}
				b.StopTimer()
				reportPer(b, dsTotal, b.N, "ds-written-B/op")
				reportPer(b, total.gotBytes, b.N, "fetched-B/op")
				reportPer(b, total.got, b.N, "fetched-blocks/op")
				reportPer(b, total.miss, b.N, "missed-blocks/op")
			})
		}
	}
}

// BenchmarkSnapshotSteady: a stream of single-key overwrites with
// CompactAfterBlocks at its default, long enough for three compactions at
// the default trigger. An op is the whole stream; the metrics are per user
// write, user-B being the key and value it carries.
func BenchmarkSnapshotSteady(b *testing.B) {
	for _, n := range snapSizes {
		b.Run(fmt.Sprintf("keys=%d", n), func(b *testing.B) {
			ctx := context.Background()
			o := newSimTime().options(0)
			o.CompactAfterBlocks = DefaultOptions().CompactAfterBlocks
			writes := 3 * max(o.CompactAfterBlocks, n)
			r := newSnapNode(b, "a", o)
			w := newSnapWorkload(n, 1)
			w.seed(b, r)
			mustSnap(b, r.Compact(ctx))
			before := r.own.read()
			var heldSum, heldMax int64
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for range writes {
					w.last = int(w.rng.Uint64() % uint64(n))
					v := w.value(w.last)
					w.rng.Read(v)
					mustSnap(b, r.Put(ctx, w.key(w.last), v))
					held := r.own.added.Load() - r.own.removed.Load()
					heldSum += held
					heldMax = max(heldMax, held)
				}
			}
			b.StopTimer()
			w.check(b, r)
			total := r.own.read().since(before)
			ops := b.N * writes
			blocks, bytes := r.held(b)
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(ops), "ns/write")
			reportPer(b, total.addedBytes, ops, "written-B/write")
			reportPer(b, total.added, ops, "written-blocks/write")
			b.ReportMetric(float64(total.addedBytes)/float64(ops*(snapKeyWidth+snapValueSize)), "amplification")
			reportPer(b, heldSum, ops, "held-blocks-mean")
			b.ReportMetric(float64(heldMax), "held-blocks-max")
			b.ReportMetric(float64(blocks), "held-blocks-end")
			b.ReportMetric(float64(bytes), "held-B-end")
		})
	}
}

// BenchmarkSnapshotExpiry: nothing is written, and time passes half a
// retention per op, past the point where the replica must refresh what it
// holds before peers stop following links to it. A key carries no ttl of its
// own: every block carries the retention it was written under. An op is one
// maintenance pass; written-* is what it rewrote.
func BenchmarkSnapshotExpiry(b *testing.B) {
	for _, n := range snapSizes {
		b.Run(fmt.Sprintf("keys=%d", n), func(b *testing.B) {
			ctx := context.Background()
			st := newSimTime()
			o := st.options(0)
			r := newSnapNode(b, "a", o)
			w := newSnapWorkload(n, 1)
			w.seed(b, r)
			mustSnap(b, r.Compact(ctx))
			var total meterReading
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				st.advance(o.Retention/2 + time.Second)
				before := r.own.read()
				b.StartTimer()
				mustSnap(b, r.Maintain(ctx))
				b.StopTimer()
				total.add(r.own.read().since(before))
				w.check(b, r)
				b.StartTimer()
			}
			b.StopTimer()
			blocks, bytes := r.held(b)
			reportPer(b, total.addedBytes, b.N, "written-B/op")
			reportPer(b, total.added, b.N, "written-blocks/op")
			b.ReportMetric(float64(bytes), "snapshot-B")
			b.ReportMetric(float64(blocks), "snapshot-blocks")
		})
	}
}

// sessionMeter is a dagMeter over a DAG service that makes sessions, as a
// node's does; the replica walks a branch through one, and its gets are
// counted with the rest.
type sessionMeter struct {
	*dagMeter
	sessions SessionDAGService
}

func (s *sessionMeter) Session(ctx context.Context) ipld.NodeGetter {
	return &getterMeter{NodeGetter: s.sessions.Session(ctx), m: s.dagMeter}
}

type getterMeter struct {
	ipld.NodeGetter
	m *dagMeter
}

func (g *getterMeter) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	return g.m.get(ctx, g.NodeGetter, c)
}

func (g *getterMeter) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	return g.m.getMany(ctx, g.NodeGetter, cids)
}

// netSnapNode is a replica on a libp2p node of its own, listening on
// loopback TCP, its DAG service the node's: blockstore, bitswap and
// merkledag, as a running node has them.
type netSnapNode struct {
	snapReplica
	node   taupeer.Node
	meter  *dagMeter
	cancel context.CancelFunc
}

func newNetSnapNode(b *testing.B, name string, opts *Options) *netSnapNode {
	b.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	repo := b.TempDir()
	recordReprovided(b, repo)
	node, err := taupeer.New(ctx, repo, keypair.NewRaw(), nil, []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, false)
	if err != nil {
		cancel()
		b.Fatal(err)
	}
	if err := node.WaitForSwarm(5 * time.Second); err != nil {
		node.Close()
		cancel()
		b.Fatal(err)
	}
	m := &dagMeter{DAGService: node.DAG()}
	n := &netSnapNode{node: node, meter: m, cancel: cancel}
	n.snapReplica = openSnap(b, snapEnv{name: name, dir: b.TempDir(), dag: &sessionMeter{dagMeter: m, sessions: node.DAG()}, opts: replicaOptions(opts, name)})
	return n
}

// recordReprovided records in the node repository at repo a reprovide
// done now. A node announces every block it holds to the DHT a minute after
// it starts, unless it did within its interval; that sweep allocated about
// 60 MB at 100k keys, no part of a catch-up, and landed in a timed one or
// not depending on how long the setup took, so a run measured it or not.
func recordReprovided(b *testing.B, repo string) {
	b.Helper()
	st, err := helpers.NewDatastore(repo)
	if err != nil {
		b.Fatal(err)
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	err = st.Put(context.Background(), ds.NewKey("/repro/reprovide/lastreprovide"), []byte(now))
	if err = errors.Join(err, st.Close()); err != nil {
		b.Fatal(err)
	}
}

func (n *netSnapNode) close(b *testing.B) {
	if err := n.Close(); err != nil {
		b.Error(err)
	}
	n.node.Close()
	n.cancel()
}

// connect connects n to peer and returns once identify has told n that
// peer speaks bitswap.
func (n *netSnapNode) connect(b *testing.B, peer *netSnapNode) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := n.node.Peer().Connect(ctx, peercore.AddrInfo{ID: peer.node.ID(), Addrs: peer.node.Peer().Addrs()}); err != nil {
		b.Fatal(err)
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		speaks, err := n.node.Peer().Peerstore().SupportsProtocols(peer.node.ID(), bsnet.ProtocolBitswap)
		if err != nil {
			b.Fatal(err)
		}
		if len(speaks) > 0 {
			return
		}
		select {
		case <-ctx.Done():
			b.Fatalf("identify never told %s that %s speaks bitswap", n.node.ID(), peer.node.ID())
		case <-tick.C:
		}
	}
}

func (n *netSnapNode) bitswap(b *testing.B) *bitswap.Stat {
	b.Helper()
	bs, ok := n.node.DAG().Exchange().(*bitswap.Bitswap)
	if !ok {
		b.Fatalf("the node's exchange is a %T, not bitswap", n.node.DAG().Exchange())
	}
	st, err := bs.Stat()
	if err != nil {
		b.Fatal(err)
	}
	return st
}

// netReading is what a catch-up cost on the wire: what the lagging node
// received, what the serving node was asked, and the gets the replica made.
type netReading struct {
	blocks, bytes, dups, wants, replies, calls int64
}

func readNet(b *testing.B, lag, src *netSnapNode) netReading {
	ls, ss := lag.bitswap(b), src.bitswap(b)
	return netReading{
		blocks:  int64(ls.BlocksReceived),
		bytes:   int64(ls.DataReceived),
		dups:    int64(ls.DupBlksReceived),
		wants:   int64(ss.MessagesReceived),
		replies: int64(ls.MessagesReceived),
		calls:   lag.meter.calls.Load(),
	}
}

func (r *netReading) add(after, before netReading) {
	r.blocks += after.blocks - before.blocks
	r.bytes += after.bytes - before.bytes
	r.dups += after.dups - before.dups
	r.wants += after.wants - before.wants
	r.replies += after.replies - before.replies
	r.calls += after.calls - before.calls
}

func (r netReading) report(b *testing.B) {
	reportPer(b, r.bytes, b.N, "fetched-B/op")
	reportPer(b, r.blocks, b.N, "fetched-blocks/op")
	reportPer(b, r.dups, b.N, "dup-blocks/op")
	reportPer(b, r.wants, b.N, "want-msgs/op")
	reportPer(b, r.replies, b.N, "reply-msgs/op")
	reportPer(b, r.calls, b.N, "get-calls/op")
}

// BenchmarkSnapshotCatchUpNet is BenchmarkSnapshotCatchUp between two libp2p
// nodes over loopback TCP, every fetch going through bitswap. warm: b is one
// snapshot behind a, k keys changed since. cold: b is empty, a new node each
// op. fetched-* is what b's bitswap received, want-msgs the bitswap messages
// a received from b, reply-msgs those b received from a, get-calls the Get
// and GetMany calls b's replica made, sessions included.
func BenchmarkSnapshotCatchUpNet(b *testing.B) {
	for _, n := range snapSizes {
		for _, k := range snapChanges {
			b.Run(fmt.Sprintf("warm/keys=%d/changed=%d", n, k), func(b *testing.B) {
				ctx := context.Background()
				st := newSimTime()
				a, lag := newNetSnapNode(b, "a", st.options(0)), newNetSnapNode(b, "b", st.options(0))
				b.Cleanup(func() { lag.close(b); a.close(b) })
				lag.connect(b, a)
				w := newSnapWorkload(n, k)
				w.seed(b, a)
				mustSnap(b, a.Compact(ctx))
				mustSnap(b, lag.CatchUp(ctx, a.snapReplica))
				var total netReading
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					b.StopTimer()
					w.changeKeys(b, a, k)
					mustSnap(b, a.Compact(ctx))
					before := readNet(b, lag, a)
					b.StartTimer()
					mustSnap(b, lag.CatchUp(ctx, a.snapReplica))
					b.StopTimer()
					total.add(readNet(b, lag, a), before)
					w.check(b, lag)
					b.StartTimer()
				}
				b.StopTimer()
				total.report(b)
			})
		}
		b.Run(fmt.Sprintf("cold/keys=%d", n), func(b *testing.B) {
			ctx := context.Background()
			st := newSimTime()
			a := newNetSnapNode(b, "a", st.options(0))
			b.Cleanup(func() { a.close(b) })
			w := newSnapWorkload(n, 1)
			w.seed(b, a)
			mustSnap(b, a.Compact(ctx))
			var total netReading
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				b.StopTimer()
				lag := newNetSnapNode(b, fmt.Sprintf("b%d", i), st.options(0))
				lag.connect(b, a)
				before := readNet(b, lag, a)
				b.StartTimer()
				mustSnap(b, lag.CatchUp(ctx, a.snapReplica))
				b.StopTimer()
				total.add(readNet(b, lag, a), before)
				w.check(b, lag)
				lag.close(b)
				b.StartTimer()
			}
			b.StopTimer()
			total.report(b)
		})
	}
}
