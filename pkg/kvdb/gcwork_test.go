package kvdb

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	ipld "github.com/ipfs/go-ipld-format"
)

// Every namespace a collector has kept records under, past ones included.
var gcPrefixes = []string{"/n/", "/x/", "/i/", "/g/", "/p/"}

type gcCounter struct {
	ns                  string
	ops, writes, remove atomic.Int64
}

func (c *gcCounter) count(k ds.Key) {
	rest, ok := strings.CutPrefix(k.String(), c.ns)
	if !ok {
		return
	}
	for _, p := range gcPrefixes {
		if strings.HasPrefix(rest, p) {
			c.ops.Add(1)
			return
		}
	}
}

type gcCountedStore struct {
	ds.Datastore
	c *gcCounter
}

func (s *gcCountedStore) Get(ctx context.Context, k ds.Key) ([]byte, error) {
	s.c.count(k)
	return s.Datastore.Get(ctx, k)
}

func (s *gcCountedStore) Has(ctx context.Context, k ds.Key) (bool, error) {
	s.c.count(k)
	return s.Datastore.Has(ctx, k)
}

func (s *gcCountedStore) Put(ctx context.Context, k ds.Key, v []byte) error {
	s.c.count(k)
	return s.Datastore.Put(ctx, k, v)
}

func (s *gcCountedStore) Delete(ctx context.Context, k ds.Key) error {
	s.c.count(k)
	return s.Datastore.Delete(ctx, k)
}

func (s *gcCountedStore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	r, err := s.Datastore.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	all, err := r.Rest()
	if err != nil {
		return nil, err
	}
	for _, e := range all {
		s.c.count(ds.RawKey(e.Key))
	}
	return query.ResultsWithEntries(q, all), nil
}

type gcCountedDAG struct {
	ipld.DAGService
	c *gcCounter
}

func (d *gcCountedDAG) Add(ctx context.Context, nd ipld.Node) error {
	d.c.writes.Add(1)
	return d.DAGService.Add(ctx, nd)
}

func (d *gcCountedDAG) AddMany(ctx context.Context, nds []ipld.Node) error {
	d.c.writes.Add(int64(len(nds)))
	return d.DAGService.AddMany(ctx, nds)
}

func (d *gcCountedDAG) Remove(ctx context.Context, c cid.Cid) error {
	d.c.remove.Add(1)
	return d.DAGService.Remove(ctx, c)
}

func (d *gcCountedDAG) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	d.c.remove.Add(int64(len(cids)))
	return d.DAGService.RemoveMany(ctx, cids)
}

func TestCollectionWorkIsWhatChanged(t *testing.T) {
	ctx := context.Background()
	check := checkSnapshot
	checkSnapshot = nil
	defer func() { checkSnapshot = check }()
	const changes = 64
	for _, n := range []int{1000, 8000} {
		c := &gcCounter{ns: "/warm"}
		o := replicaOptions(newSimTime().options(0), "warm")
		o.CompactAfterBlocks = 0
		o.manual = true
		o.Logger = DefaultOptions().Logger
		r, err := NewDatastore(&gcCountedStore{Datastore: newSortedStore(), c: c}, ds.NewKey("warm"),
			&gcCountedDAG{DAGService: newFetchThrough(), c: c}, &nullBroadcaster{}, o)
		if err != nil {
			t.Fatal(err)
		}
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
		for round := 1; round <= 3; round++ {
			for i := range changes {
				if err := r.Put(ctx, warmKey((round*131+i*7919)%n), warmValue(i, round)); err != nil {
					t.Fatal(err)
				}
			}
			ops, writes, removed := c.ops.Load(), c.writes.Load(), c.remove.Load()
			if _, err := r.Compact(ctx); err != nil {
				t.Fatal(err)
			}
			ops, writes, removed = c.ops.Load()-ops, c.writes.Load()-writes, c.remove.Load()-removed
			t.Logf("n=%d round %d: %d record operations, %d blocks written, %d removed", n, round, ops, writes, removed)
			if ops > writes+removed+16 {
				t.Errorf("n=%d: a compaction writing %d blocks and removing %d did %d operations on the collector's records", n, writes, removed, ops)
			}
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
