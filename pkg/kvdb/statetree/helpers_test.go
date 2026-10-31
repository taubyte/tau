package statetree

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
	"testing"

	blockstore "github.com/ipfs/boxo/blockstore"
	dag "github.com/ipfs/boxo/ipld/merkledag"
	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// counting is a DAGService that counts the nodes read and written.
type counting struct {
	ipld.DAGService
	bs         blockstore.Blockstore
	gets, adds atomic.Int64
}

func newStore() *counting {
	bs := mdutils.Bserv()
	return &counting{DAGService: dag.NewDAGService(bs), bs: bs.Blockstore()}
}

func (c *counting) Get(ctx context.Context, id cid.Cid) (ipld.Node, error) {
	c.gets.Add(1)
	return c.DAGService.Get(ctx, id)
}

func (c *counting) Add(ctx context.Context, nd ipld.Node) error {
	c.adds.Add(1)
	return c.DAGService.Add(ctx, nd)
}

func (c *counting) AddMany(ctx context.Context, nds []ipld.Node) error {
	c.adds.Add(int64(len(nds)))
	return c.DAGService.AddMany(ctx, nds)
}

func (c *counting) reset() {
	c.gets.Store(0)
	c.adds.Store(0)
}

func key(i int) []byte   { return fmt.Appendf(nil, "/k/%08d", i) }
func value(i int) []byte { return fmt.Appendf(nil, "value-%d-%032d", i, i) }

func sortedSeq(m map[string][]byte) func(func([]byte, []byte) bool) {
	return func(yield func([]byte, []byte) bool) {
		for _, k := range slices.Sorted(maps.Keys(m)) {
			if !yield([]byte(k), m[k]) {
				return
			}
		}
	}
}

func build(t testing.TB, s ipld.DAGService, m map[string][]byte, opts ...Option) *Tree {
	t.Helper()
	tr, err := Build(context.Background(), s, sortedSeq(m), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func put(t testing.TB, tr *Tree, k, v []byte) *Tree {
	t.Helper()
	n, err := tr.Put(context.Background(), k, v)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func del(t testing.TB, tr *Tree, k []byte) *Tree {
	t.Helper()
	n, err := tr.Delete(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func verify(t testing.TB, tr *Tree) {
	t.Helper()
	if err := tr.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func entries(t testing.TB, tr *Tree) map[string][]byte {
	t.Helper()
	m := map[string][]byte{}
	if err := tr.Range(context.Background(), nil, nil, func(k, v []byte) error {
		m[string(k)] = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return m
}

// nodes is every node under root, read by decoding each one rather than by
// merkledag's walk, with the length of the longest root-to-node path.
func nodes(t testing.TB, s ipld.DAGService, root cid.Cid) (map[cid.Cid]struct{}, int) {
	t.Helper()
	out := map[cid.Cid]struct{}{}
	var walk func(c cid.Cid, below uint32) int
	walk = func(c cid.Cid, below uint32) int {
		if !c.Defined() {
			return 0
		}
		nd, err := s.Get(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		n, err := decode(nd, below)
		if err != nil {
			t.Fatal(err)
		}
		out[c] = struct{}{}
		d := 0
		for _, k := range n.kids {
			d = max(d, walk(k.c, n.level))
		}
		return d + 1
	}
	depth := walk(root, noLevel)
	return out, depth
}

// pathLen is how many nodes Get reads to reach k.
func pathLen(t testing.TB, s *counting, tr *Tree, k []byte) int {
	t.Helper()
	s.reset()
	if _, _, err := tr.Get(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	return int(s.gets.Load())
}
