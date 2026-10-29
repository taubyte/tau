package kvdb

import (
	"context"
	"fmt"
	"sync"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	ipld "github.com/ipfs/go-ipld-format"
)

// work counts what an operation asks of the stores under a replica.
type work struct {
	reads, scans, scanned, writes int
	dagReads, dagWrites           int
}

func (w work) String() string {
	return fmt.Sprintf("reads %d scans %d scanned %d writes %d dag reads %d dag writes %d",
		w.reads, w.scans, w.scanned, w.writes, w.dagReads, w.dagWrites)
}

func (w work) since(o work) work {
	return work{
		reads: w.reads - o.reads, scans: w.scans - o.scans, scanned: w.scanned - o.scanned,
		writes: w.writes - o.writes, dagReads: w.dagReads - o.dagReads, dagWrites: w.dagWrites - o.dagWrites,
	}
}

// workMeter counts the datastore operations and DAG operations of one
// replica.
type workMeter struct {
	mu sync.Mutex
	w  work
}

func (m *workMeter) add(f func(w *work)) {
	m.mu.Lock()
	f(&m.w)
	m.mu.Unlock()
}

func (m *workMeter) read() work {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.w
}

type meteredStore struct {
	ds.Datastore
	m *workMeter
}

func (s *meteredStore) Get(ctx context.Context, k ds.Key) ([]byte, error) {
	s.m.add(func(w *work) { w.reads++ })
	return s.Datastore.Get(ctx, k)
}

func (s *meteredStore) Has(ctx context.Context, k ds.Key) (bool, error) {
	s.m.add(func(w *work) { w.reads++ })
	return s.Datastore.Has(ctx, k)
}

func (s *meteredStore) GetSize(ctx context.Context, k ds.Key) (int, error) {
	s.m.add(func(w *work) { w.reads++ })
	return s.Datastore.GetSize(ctx, k)
}

func (s *meteredStore) Put(ctx context.Context, k ds.Key, v []byte) error {
	s.m.add(func(w *work) { w.writes++ })
	return s.Datastore.Put(ctx, k, v)
}

func (s *meteredStore) Delete(ctx context.Context, k ds.Key) error {
	s.m.add(func(w *work) { w.writes++ })
	return s.Datastore.Delete(ctx, k)
}

func (s *meteredStore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	s.m.add(func(w *work) { w.scans++ })
	r, err := s.Datastore.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	all, err := r.Rest()
	if err != nil {
		return nil, err
	}
	s.m.add(func(w *work) { w.scanned += len(all) })
	return query.ResultsWithEntries(q, all), nil
}

func (s *meteredStore) Batch(ctx context.Context) (ds.Batch, error) {
	b, err := batchOf(ctx, s.Datastore)
	return &meteredBatch{Batch: b, m: s.m}, err
}

type meteredBatch struct {
	ds.Batch
	m *workMeter
}

func (b *meteredBatch) Put(ctx context.Context, k ds.Key, v []byte) error {
	b.m.add(func(w *work) { w.writes++ })
	return b.Batch.Put(ctx, k, v)
}

func (b *meteredBatch) Delete(ctx context.Context, k ds.Key) error {
	b.m.add(func(w *work) { w.writes++ })
	return b.Batch.Delete(ctx, k)
}

type meteredDAG struct {
	ipld.DAGService
	m *workMeter
}

func (d *meteredDAG) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	d.m.add(func(w *work) { w.dagReads++ })
	return d.DAGService.Get(ctx, c)
}

func (d *meteredDAG) Add(ctx context.Context, nd ipld.Node) error {
	d.m.add(func(w *work) { w.dagWrites++ })
	return d.DAGService.Add(ctx, nd)
}

func (d *meteredDAG) AddMany(ctx context.Context, nds []ipld.Node) error {
	d.m.add(func(w *work) { w.dagWrites += len(nds) })
	return d.DAGService.AddMany(ctx, nds)
}

// warmReplica is a replica holding n keys, compacted, that compacts only
// when told to.
func warmReplica(t testing.TB, n int) (*Datastore, *workMeter) {
	t.Helper()
	ctx := context.Background()
	m := &workMeter{}
	o := replicaOptions(newSimTime().options(0), "warm")
	o.CompactAfterBlocks = 0
	o.manual = true
	o.Logger = DefaultOptions().Logger
	r, err := NewDatastore(&meteredStore{Datastore: newSortedStore(), m: m}, ds.NewKey("warm"),
		&meteredDAG{DAGService: newFetchThrough(), m: m}, &nullBroadcaster{}, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	b, err := r.Batch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if err := b.Put(ctx, warmKey(i), warmValue(i, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	return r, m
}

func warmKey(i int) ds.Key { return ds.RawKey(fmt.Sprintf("/k/%010d", i)) }

func warmValue(i, round int) []byte {
	v := make([]byte, 100)
	copy(v, fmt.Sprintf("%d-%d", i, round))
	return v
}

// TestCompactionWorkIsBounded: what a compaction asks of the stores grows
// with the records it changes, not with the records the store holds. A tree
// node a compaction changes many times is read once: one at a time, every
// changed record read its whole path again, and again to look it up.
func TestCompactionWorkIsBounded(t *testing.T) {
	ctx := context.Background()
	check := checkSnapshot
	checkSnapshot = nil
	defer func() { checkSnapshot = check }()
	const changes = 64
	var compactions []work
	for _, n := range []int{1000, 8000} {
		r, m := warmReplica(t, n)
		before := m.read()
		for i := range changes {
			if err := r.Put(ctx, warmKey((i*7919)%n), warmValue(i, 1)); err != nil {
				t.Fatal(err)
			}
		}
		mid := m.read()
		if _, err := r.Compact(ctx); err != nil {
			t.Fatal(err)
		}
		puts, c := mid.since(before), m.read().since(mid)
		t.Logf("n=%d: %d puts: %s; their compaction: %s", n, changes, puts, c)
		if puts.reads > 8*changes || puts.scans > 4*changes || puts.scanned > 4*changes {
			t.Errorf("n=%d: %d puts did %s", n, changes, puts)
		}
		if c.dagReads > 6*changes {
			t.Errorf("n=%d: a compaction of %d changes read %d tree nodes", n, changes, c.dagReads)
		}
		if c.reads > 40*changes || c.scanned > 16*changes || c.writes > 40*changes {
			t.Errorf("n=%d: a compaction of %d changes did %s", n, changes, c)
		}
		compactions = append(compactions, c)
	}
	if small, big := compactions[0], compactions[1]; big.reads > 3*small.reads || big.writes > 3*small.writes {
		t.Errorf("a store 8 times larger: compaction %s, against %s", big, small)
	}
}

// benchCompact measures a compaction of changes records in a warm store of
// n keys, the Puts before it left out.
func benchCompact(b *testing.B, n, changes int) {
	ctx := context.Background()
	r, _ := warmReplica(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for round := range b.N {
		b.StopTimer()
		for i := range changes {
			if err := r.Put(ctx, warmKey((round*131+i*7919)%n), warmValue(i, round)); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		if _, err := r.Compact(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHotCompact(b *testing.B) {
	check := checkSnapshot
	checkSnapshot = nil
	defer func() { checkSnapshot = check }()
	for _, changes := range []int{1, 32} {
		b.Run(fmt.Sprintf("changes=%d", changes), func(b *testing.B) { benchCompact(b, 4000, changes) })
	}
}

// TestCompactionAllocs: a compaction allocates in proportion to the
// records it changes. Applied one at a time, every change decoded and
// encoded each tree node on its path again.
func TestCompactionAllocs(t *testing.T) {
	ctx := context.Background()
	check := checkSnapshot
	checkSnapshot = nil
	defer func() { checkSnapshot = check }()
	r, _ := warmReplica(t, 4000)
	round := 0
	const changes = 32
	puts := func() {
		round++
		for i := range changes {
			if err := r.Put(ctx, warmKey((round*131+i*7919)%4000), warmValue(i, round)); err != nil {
				t.Fatal(err)
			}
		}
	}
	compact := func() {
		if _, err := r.Compact(ctx); err != nil {
			t.Fatal(err)
		}
	}
	onlyPuts := testing.AllocsPerRun(16, puts)
	compact()
	both := testing.AllocsPerRun(16, func() { puts(); compact() })
	t.Logf("%d puts: %.0f allocations; with their compaction: %.0f", changes, onlyPuts, both)
	if both > 60000 {
		t.Errorf("%d puts and their compaction allocate %.0f times", changes, both)
	}
}

func BenchmarkHotPut(b *testing.B) {
	ctx := context.Background()
	check := checkSnapshot
	checkSnapshot = nil
	defer func() { checkSnapshot = check }()
	r, _ := warmReplica(b, 4000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if err := r.Put(ctx, warmKey((i*7919)%4000), warmValue(i, 1)); err != nil {
			b.Fatal(err)
		}
	}
}
