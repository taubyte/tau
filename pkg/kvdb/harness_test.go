package kvdb

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	blockstore "github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/boxo/ipld/merkledag"
	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	dstest "github.com/ipfs/go-datastore/test"
	ipld "github.com/ipfs/go-ipld-format"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

// TestMain installs a tracer provider before any replica starts. The
// blockservice under every replica's DAG starts a span for each block it
// gets, adds or deletes, and with no provider installed otel answers each of
// those through one mutex for the whole process. Fifteen replicas walking
// and purging at once queued a thousand goroutines on it: a compaction
// purging its blocks held store.mu for up to 20s under -race on a loaded
// host, the broadcast reader waited on store.mu, and every peer's broadcast
// to it timed out; fetches waited on it past DAGSyncerTimeout and were left
// to the next repair, a minute later.
func TestMain(m *testing.M) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	flag.Parse()
	installOracle()
	os.Exit(m.Run())
}

// fetchThroughDAGSvc is one replica's own blockstore that, on a local miss,
// fetches the block from its remotes and keeps a copy, the way a bitswap
// session finds a block on whichever peer has it. A block this replica
// purged is gone from its own store, whoever else still has it.
type fetchThroughDAGSvc struct {
	ipld.DAGService // this replica's own blocks
	bs              blockstore.Blockstore

	mu      sync.Mutex
	remotes []ipld.DAGService
}

func newFetchThrough() *fetchThroughDAGSvc {
	bserv := mdutils.Bserv()
	return &fetchThroughDAGSvc{DAGService: merkledag.NewDAGService(bserv), bs: bserv.Blockstore()}
}

func (f *fetchThroughDAGSvc) setRemotes(r ...ipld.DAGService) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remotes = r
}

func (f *fetchThroughDAGSvc) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	nd, err := f.DAGService.Get(ctx, c)
	if err == nil {
		return nd, nil
	}
	f.mu.Lock()
	remotes := append([]ipld.DAGService(nil), f.remotes...)
	f.mu.Unlock()
	for _, r := range remotes {
		rnd, rerr := r.Get(ctx, c)
		if rerr != nil {
			continue
		}
		if addErr := f.DAGService.Add(ctx, rnd); addErr != nil {
			return nil, addErr
		}
		return rnd, nil
	}
	return nil, err
}

func (f *fetchThroughDAGSvc) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(cids))
	go func() {
		defer close(out)
		for _, c := range cids {
			nd, err := f.Get(ctx, c)
			out <- &ipld.NodeOption{Node: nd, Err: err}
		}
	}()
	return out
}

// blockCount is how many blocks this replica holds itself.
func (f *fetchThroughDAGSvc) blockCount(t testing.TB) int {
	t.Helper()
	ch, err := f.bs.AllKeysChan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for range ch {
		n++
	}
	return n
}

func (f *fetchThroughDAGSvc) holds(c cid.Cid) bool {
	has, err := f.bs.Has(context.Background(), c)
	return err == nil && has
}

// countingDAG counts every block its replica asks for, whoever serves it.
type countingDAG struct {
	*fetchThroughDAGSvc

	mu      sync.Mutex
	fetched map[cid.Cid]int
}

func newCountingDAG() *countingDAG {
	return &countingDAG{fetchThroughDAGSvc: newFetchThrough(), fetched: map[cid.Cid]int{}}
}

func (c *countingDAG) count(cids ...cid.Cid) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range cids {
		c.fetched[id]++
	}
}

func (c *countingDAG) Get(ctx context.Context, id cid.Cid) (ipld.Node, error) {
	c.count(id)
	return c.fetchThroughDAGSvc.Get(ctx, id)
}

func (c *countingDAG) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	c.count(cids...)
	return c.fetchThroughDAGSvc.GetMany(ctx, cids)
}

// take returns the fetches since the last take.
func (c *countingDAG) take() map[cid.Cid]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.fetched
	c.fetched = map[cid.Cid]int{}
	return out
}

// from points replica r's fetches at src only.
func from(dagsyncs []*fetchThroughDAGSvc, r, src int) {
	dagsyncs[r].setRemotes(dagsyncs[src].DAGService)
}

func replicaOptions(opts *Options, name string) *Options {
	o := DefaultOptions()
	if opts != nil {
		cp := *opts
		o = &cp
	}
	o.Logger = &testLogger{name: name, l: DefaultOptions().Logger}
	o.DAGSyncerTimeout = time.Second
	if len(o.ReplicaID) == 0 {
		o.ReplicaID = []byte(name)
	}
	return o
}

// makeNReplicasSeparateStores makes n replicas that do not broadcast and run
// nothing on their own (manual): tests drive every exchange with
// syncReplicaHeads, and every repair, so the order is theirs. A repair loop
// of their own would walk blocks a test's Repair then skips as already being
// fetched, and the test would read a state still merging. Each replica has
// its own blockstore; replica i > 0 fetches from replica 0 until a test
// points it elsewhere.
func makeNReplicasSeparateStores(t testing.TB, n int, opts *Options) ([]*Datastore, []*fetchThroughDAGSvc, func()) {
	t.Helper()
	return makeSeparateStoresWith(t, n, func(int) *Options { return opts })
}

// makeSeparateStoresWith is makeNReplicasSeparateStores with options of each
// replica's own.
func makeSeparateStoresWith(t testing.TB, n int, optsFor func(i int) *Options) ([]*Datastore, []*fetchThroughDAGSvc, func()) {
	t.Helper()
	dagsyncs := make([]*fetchThroughDAGSvc, n)
	replicas := make([]*Datastore, n)
	for i := range replicas {
		dagsyncs[i] = newFetchThrough()
		o := replicaOptions(optsFor(i), fmt.Sprintf("sr#%d: ", i))
		o.manual = true
		var err error
		replicas[i], err = NewDatastore(makeStore(t, i), ds.NewKey("crdttest"), dagsyncs[i], &nullBroadcaster{}, o)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < n; i++ {
		from(dagsyncs, i, 0)
	}
	closeReplicas := func() {
		for i, r := range replicas {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
			os.RemoveAll(storeFolder(i))
		}
	}
	return replicas, dagsyncs, closeReplicas
}

// syncReplicaHeads walks, on "to", every head of "from": exactly what "to"
// does when it hears them broadcast, without the broadcaster's timing.
func syncReplicaHeads(t testing.TB, from, to *Datastore) {
	t.Helper()
	heads, _ := from.heads.List()
	for _, h := range heads {
		if err := to.handleBlock(context.Background(), h); err != nil {
			t.Fatal(err)
		}
	}
}

// hear delivers src's heartbeat to dst, heads left out: its fleet time, its
// rejoin state and what it has merged of who wrote lately. Tests that walk
// heads directly exchange heartbeats this way, as replicas in contact do every
// rebroadcast interval.
func hear(t testing.TB, dst, src *Datastore) {
	t.Helper()
	data, err := src.encodeBroadcast(context.Background(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := dst.decodeBroadcast(data)
	if err != nil {
		t.Fatal(err)
	}
	dst.handleBroadcast(context.Background(), msg)
}

// lastDot is the dot of r's last write.
func lastDot(r *Datastore) dot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return dot{replica: r.replica, counter: r.counter}
}

// waitCovered waits until every replica covers every dot.
//
// Replication runs on the replicas' own goroutines: a broadcast is heard, a
// walk fetches and merges, and how long that takes is the machine's business,
// longer under load. The tests that use this assert what replication converges
// to, so they wait for the merge itself, woken by every change of a replica's
// context, and give up only at the test's own deadline. A fixed sleep in its
// place asserted a latency nothing promises, and failed under load.
func waitCovered(t *testing.T, replicas []*Datastore, dots ...dot) {
	t.Helper()
	var deadline <-chan time.Time
	if dl, ok := t.Deadline(); ok {
		timer := time.NewTimer(time.Until(dl) * 9 / 10)
		defer timer.Stop()
		deadline = timer.C
	}
	for i, r := range replicas {
		for _, d := range dots {
			for {
				changed := r.contextChanged()
				if r.covered(d) {
					break
				}
				select {
				case <-changed:
				case <-deadline:
					t.Fatalf("replica %d never covered %s", i, d)
				}
			}
		}
	}
}

// syncHeadsLogged is syncReplicaHeads for a source whose heads may name a
// block nobody holds: the failure is logged and the state is what is judged.
func syncHeadsLogged(t testing.TB, src, dst *Datastore) {
	t.Helper()
	heads, _ := src.heads.List()
	for _, h := range heads {
		if err := dst.handleBlock(context.Background(), h); err != nil {
			t.Logf("syncing head %s: %s", h.Cid, err)
		}
	}
}

// queryAll returns every key/value currently in the datastore.
func queryAll(t testing.TB, r *Datastore) map[string][]byte {
	t.Helper()
	res, err := r.Query(context.Background(), query.Query{})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	out := map[string][]byte{}
	for e := range res.Next() {
		if e.Error != nil {
			t.Fatal(e.Error)
		}
		out[e.Key] = append([]byte(nil), e.Value...)
	}
	return out
}

func assertSameKV(t testing.TB, a, b map[string][]byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("state mismatch: %d keys vs %d keys (a=%v b=%v)", len(a), len(b), a, b)
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok {
			t.Fatalf("state mismatch: key %s missing on the other side", k)
		}
		if !bytes.Equal(v, bv) {
			t.Fatalf("state mismatch for key %s: %q vs %q", k, v, bv)
		}
	}
}

func headCIDs(heads []Head) []cid.Cid {
	out := make([]cid.Cid, len(heads))
	for i, h := range heads {
		out[i] = h.Cid
	}
	return out
}

func sameCids(a, b []cid.Cid) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[cid.Cid]bool{}
	for _, c := range a {
		seen[c] = true
	}
	for _, c := range b {
		if !seen[c] {
			return false
		}
	}
	return true
}

func valueOf(t testing.TB, r *Datastore, k ds.Key) string {
	t.Helper()
	v, err := r.Get(context.Background(), k)
	if err == ds.ErrNotFound {
		return "<deleted>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(v)
}

// crdtState is the whole replicated state of a replica, written out: its
// elements with their dots and priorities, its pending removals, its causal
// context and its values. Two replicas that merged the same deltas hold the
// same crdtState.
func crdtState(t testing.TB, r *Datastore) string {
	t.Helper()
	ctx := context.Background()
	var lines []string
	add := func(prefix ds.Key, label string) {
		err := scanStore(ctx, r.store, prefix, func(k string, v []byte) error {
			lines = append(lines, fmt.Sprintf("%s %s=%x", label, strings.TrimPrefix(k, prefix.String()), v))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	add(r.set.elemsPrefix(), "elem")
	add(r.set.pendingPrefix(), "pending")
	add(r.set.namespace.ChildString(keysNs), "value")
	sort.Strings(lines)
	r.mu.Lock()
	cc := r.cc.toPB()
	r.mu.Unlock()
	lines = append(lines, "context "+cc.String())
	hs, _ := r.heads.List()
	var heads []string
	for _, h := range hs {
		heads = append(heads, h.Cid.String())
	}
	sort.Strings(heads)
	lines = append(lines, "heads "+strings.Join(heads, " "))
	return strings.Join(lines, "\n")
}

func assertSameState(t testing.TB, names []string, replicas ...*Datastore) {
	t.Helper()
	want := crdtState(t, replicas[0])
	for i, r := range replicas[1:] {
		if got := crdtState(t, r); got != want {
			t.Fatalf("replica %s diverged from %s:\n--- %s\n%s\n--- %s\n%s", names[i+1], names[0], names[0], want, names[i+1], got)
		}
	}
}

// namespaceKeys is how many datastore entries a replica holds.
func namespaceKeys(t testing.TB, r *Datastore) int {
	t.Helper()
	n, err := countPrefix(context.Background(), r.store, r.namespace)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// sortedStore is the tests' in-memory datastore: a map datastore that also
// keeps its keys in order, so a prefix query reads only the keys under the
// prefix where a map store reads every key it holds. Its results come in no
// particular order, as a map store's do: the order a query returns is nothing
// kvdb may rely on.
type sortedStore struct {
	mu   sync.RWMutex
	keys []string
	vals map[string][]byte
}

func newSortedStore() *sortedStore { return &sortedStore{vals: map[string][]byte{}} }

func (s *sortedStore) put(k string, v []byte) {
	if _, ok := s.vals[k]; !ok {
		s.keys = slices.Insert(s.keys, sort.SearchStrings(s.keys, k), k)
	}
	s.vals[k] = v
}

func (s *sortedStore) del(k string) {
	if _, ok := s.vals[k]; !ok {
		return
	}
	delete(s.vals, k)
	i := sort.SearchStrings(s.keys, k)
	s.keys = slices.Delete(s.keys, i, i+1)
}

func (s *sortedStore) Put(_ context.Context, k ds.Key, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(k.String(), v)
	return nil
}

func (s *sortedStore) Delete(_ context.Context, k ds.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.del(k.String())
	return nil
}

func (s *sortedStore) Get(_ context.Context, k ds.Key) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.vals[k.String()]
	if !ok {
		return nil, ds.ErrNotFound
	}
	return v, nil
}

func (s *sortedStore) Has(_ context.Context, k ds.Key) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.vals[k.String()]
	return ok, nil
}

func (s *sortedStore) GetSize(_ context.Context, k ds.Key) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.vals[k.String()]
	if !ok {
		return -1, ds.ErrNotFound
	}
	return len(v), nil
}

// Query narrows to the keys under the prefix, cleaned as the naive query
// cleans it, and leaves everything else, the prefix check included, to it.
func (s *sortedStore) Query(_ context.Context, q query.Query) (query.Results, error) {
	s.mu.RLock()
	lo, hi := 0, len(s.keys)
	if p := q.Prefix; p != "" {
		if p[0] != '/' {
			p = "/" + p
		}
		if p = path.Clean(p); p != "/" {
			p += "/"
			lo = sort.SearchStrings(s.keys, p)
			hi = lo + sort.Search(len(s.keys)-lo, func(i int) bool { return !strings.HasPrefix(s.keys[lo+i], p) })
		}
	}
	entries := make([]query.Entry, 0, hi-lo)
	for _, k := range s.keys[lo:hi] {
		e := query.Entry{Key: k, Size: len(s.vals[k])}
		if !q.KeysOnly {
			e.Value = s.vals[k]
		}
		entries = append(entries, e)
	}
	s.mu.RUnlock()
	rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	return query.NaiveQueryApply(q, query.ResultsWithEntries(q, entries)), nil
}

func (s *sortedStore) Sync(context.Context, ds.Key) error { return nil }

func (s *sortedStore) Close() error { return nil }

func (s *sortedStore) Batch(context.Context) (ds.Batch, error) { return &sortedBatch{s: s}, nil }

// TestTheSortedStoreIsADatastore holds the tests' store to the suite every
// datastore is held to, the map store it stands in for included.
func TestTheSortedStoreIsADatastore(t *testing.T) {
	dstest.SubtestAll(t, newSortedStore())
}

// sortedBatch applies its writes in order, all under one lock, as a map
// store's batch does under its mutex.
type sortedBatch struct {
	s   *sortedStore
	ops []sortedOp
}

type sortedOp struct {
	key   string
	value []byte
	del   bool
}

func (b *sortedBatch) Put(_ context.Context, k ds.Key, v []byte) error {
	b.ops = append(b.ops, sortedOp{key: k.String(), value: v})
	return nil
}

func (b *sortedBatch) Delete(_ context.Context, k ds.Key) error {
	b.ops = append(b.ops, sortedOp{key: k.String(), del: true})
	return nil
}

func (b *sortedBatch) Commit(context.Context) error {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	for _, op := range b.ops {
		if op.del {
			b.s.del(op.key)
		} else {
			b.s.put(op.key, op.value)
		}
	}
	b.ops = nil
	return nil
}

// blockLinksOf is the links of nd, one of kvdb's blocks.
func blockLinksOf(t testing.TB, nd ipld.Node) []cid.Cid {
	t.Helper()
	links, err := blockLinks(nd, nil)
	if err != nil {
		t.Fatal(err)
	}
	return links
}
