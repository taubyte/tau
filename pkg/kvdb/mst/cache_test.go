package mst

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"unsafe"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	mh "github.com/multiformats/go-multihash"
)

// heldDAG serves the very nodes added to it, so a test can tell whether a
// decoded node points into a block's bytes.
type heldDAG struct {
	ipld.DAGService
	nodes map[cid.Cid]ipld.Node
	gets  int
}

func newHeldDAG() *heldDAG { return &heldDAG{nodes: map[cid.Cid]ipld.Node{}} }

func (h *heldDAG) Get(_ context.Context, c cid.Cid) (ipld.Node, error) {
	h.gets++
	if nd, ok := h.nodes[c]; ok {
		return nd, nil
	}
	return nil, ipld.ErrNotFound{Cid: c}
}

func (h *heldDAG) Add(_ context.Context, nd ipld.Node) error {
	h.nodes[nd.Cid()] = nd
	return nil
}

func (h *heldDAG) AddMany(ctx context.Context, nds []ipld.Node) error {
	for _, nd := range nds {
		h.nodes[nd.Cid()] = nd
	}
	return nil
}

func randomEntries(r *rand.Rand, n int) map[string][]byte {
	m := map[string][]byte{}
	for range n {
		i := r.IntN(1 << 30)
		m[string(key(i))] = value(i)
	}
	return m
}

// within reports whether b starts inside block.
func within(b, block []byte) bool {
	if len(b) == 0 || len(block) == 0 {
		return false
	}
	p, lo := uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&block[0]))
	return p >= lo && p < lo+uintptr(len(block))
}

// held is what c holds, by CID.
func (c *Cache) held() map[cid.Cid]*node {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[cid.Cid]*node{}
	for k, i := range c.index {
		id, err := cid.Cast(k[:])
		if err != nil {
			panic(err)
		}
		out[id] = c.slots[i].n
	}
	return out
}

// recount is what c's nodes cost, counted from the nodes themselves.
func (c *Cache) recount() int {
	total := 0
	for _, n := range c.held() {
		buf, cids := 0, 0
		for _, e := range n.entries {
			buf += len(e.key) + len(e.val)
		}
		for _, k := range n.kids {
			buf += len(k.sum)
			cids += len(k.c.KeyString())
		}
		total += footprint(len(n.entries), buf, cids)
	}
	return total
}

func (c *Cache) check(t testing.TB) {
	t.Helper()
	used, n := c.Used(), c.recount()
	if used != n {
		t.Fatalf("the cache counts %d bytes, its nodes cost %d", used, n)
	}
	if used > c.budget {
		t.Fatalf("the cache holds %d bytes, over its budget of %d", used, c.budget)
	}
}

// TestACachedNodeOwnsItsBytes: a node the cache keeps holds no byte of the
// block it was decoded from, while a node it does not keep aliases the
// block as before.
func TestACachedNodeOwnsItsBytes(t *testing.T) {
	ctx := context.Background()
	s := newHeldDAG()
	tr := build(t, s, randomEntries(rand.New(rand.NewPCG(1, 2)), 3000), WithSummarizer(maxSummary{}))
	c := NewCache(1 << 20)
	ct := Load(s, tr.Root(), WithSummarizer(maxSummary{}), WithCache(c))
	if err := ct.Range(ctx, nil, nil, func(_, _ []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	held := c.held()
	if len(held) < 2 {
		t.Fatalf("the cache holds %d nodes", len(held))
	}
	for id, n := range held {
		data := blockData(t, s.nodes[id])
		for _, e := range n.entries {
			if within(e.key, data) || within(e.val, data) {
				t.Fatalf("cached node %s holds bytes of its block", id)
			}
		}
		for _, k := range n.kids {
			if within(k.sum, data) {
				t.Fatalf("cached node %s holds a summary in its block", id)
			}
		}
	}
	c.check(t)

	var leaf cid.Cid
	for id := range s.nodes {
		if _, ok := held[id]; !ok {
			leaf = id
			break
		}
	}
	n, cost, err := decodeNode(s.nodes[leaf], noLevel, func(uint32, int) bool { return false })
	if err != nil || cost != 0 {
		t.Fatal(cost, err)
	}
	if !within(n.entries[0].val, blockData(t, s.nodes[leaf])) {
		t.Fatal("a node the cache does not keep copied its block")
	}
}

// TestACachedTreeReadsOnlyItsLowestLevel: once a tree's upper levels are
// cached, a lookup reads at most the level-0 node of its key; before, it
// read its whole path.
func TestACachedTreeReadsOnlyItsLowestLevel(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	m := randomEntries(rand.New(rand.NewPCG(3, 4)), 5000)
	tr := build(t, s, m)
	c := NewCache(1 << 20)
	ct := Load(s, tr.Root(), WithCache(c))
	if err := ct.Range(ctx, nil, nil, func(_, _ []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	c.check(t)
	paths := 0
	for k := range m {
		paths += pathLen(t, s, tr, []byte(k))
		want := 0
		if Level([]byte(k)) == 0 {
			want = 1
		}
		if n := pathLen(t, s, ct, []byte(k)); n != want {
			t.Fatalf("a cached lookup of a key of level %d read %d nodes, want %d", Level([]byte(k)), n, want)
		}
	}
	if paths <= 2*len(m) {
		t.Fatalf("uncached lookups read %d nodes for %d keys: the tree is too shallow to test", paths, len(m))
	}
}

// TestACacheKeepsTheUpperLevelsWithinItsBudget: a cache too small for a
// tree's upper levels keeps its highest levels, never a node of level 0
// that is not a root, and never more than its budget, through runs of
// changes to the tree.
func TestACacheKeepsTheUpperLevelsWithinItsBudget(t *testing.T) {
	ctx := context.Background()
	r := rand.New(rand.NewPCG(5, 6))
	s := newStore()
	m := randomEntries(r, 8000)
	c := NewCache(64 << 10)
	tr := Load(s, build(t, s, m).Root(), WithCache(c))
	for round := range 40 {
		e := tr.Edit()
		for range 20 {
			i := r.IntN(1 << 30)
			if err := e.Put(ctx, key(i), value(round)); err != nil {
				t.Fatal(err)
			}
		}
		next, err := e.Tree(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := Diff(ctx, tr, next, func(Change) error { return nil }); err != nil {
			t.Fatal(err)
		}
		tr = next
		c.check(t)
	}
	if err := tr.Range(ctx, nil, nil, func(_, _ []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	c.check(t)
	held := c.held()
	lowest := uint32(noLevel)
	for id, n := range held {
		if n.level == 0 && id != tr.Root() {
			t.Fatalf("the cache holds %s, of level 0 and no root", id)
		}
		lowest = min(lowest, n.level)
	}
	all, _ := nodes(t, s, tr.Root())
	for id := range all {
		nd, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		n, err := decode(nd, noLevel)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := held[id]; !ok && n.level > lowest {
			t.Fatalf("the cache keeps a node of level %d but not %s, of level %d", lowest, id, n.level)
		}
	}
	if lowest == 0 || lowest == noLevel {
		t.Fatalf("the cache's lowest level is %d: the budget does not test eviction", lowest)
	}
}

// TestACacheEvictsTheLeastRecentlyUsedOfALevel: a node takes the place of
// the node of its level used least recently, or of a lower level first,
// and a node of a level below every one the full cache holds is not kept.
func TestACacheEvictsTheLeastRecentlyUsedOfALevel(t *testing.T) {
	type kept struct {
		id   cid.Cid
		n    *node
		cost int
	}
	mk := func(level uint32, k string) kept {
		n := &node{level: level, entries: []entry{{key: []byte(k), val: []byte("v")}}, kids: make([]ref, 2)}
		nd, err := encode(n)
		if err != nil {
			t.Fatal(err)
		}
		n, cost, err := decodeNode(nd, noLevel, func(uint32, int) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		return kept{nd.Cid(), n, cost}
	}
	var ones []kept
	for i := range 9 {
		ones = append(ones, mk(1, fmt.Sprintf("k%d", i)))
	}
	size := ones[0].cost
	c := NewCache(8 * size)
	for _, k := range ones[:8] {
		if !c.wants(1, size, false) {
			t.Fatal("a cache with room does not want a node")
		}
		c.add(k.id, k.n, k.cost)
	}
	c.add(ones[0].id, ones[0].n, ones[0].cost)
	if c.Used() != 8*size || c.get(ones[0].id) != ones[0].n {
		t.Fatal("the first eight nodes are not cached once each")
	}
	c.add(ones[8].id, ones[8].n, size)
	if c.get(ones[1].id) != nil || c.get(ones[0].id) == nil || c.get(ones[8].id) == nil {
		t.Fatal("a node did not take the place of the least recently used")
	}
	two := mk(2, "t2")
	c.add(two.id, two.n, two.cost)
	if c.get(two.id) == nil || c.get(ones[2].id) != nil {
		t.Fatal("a node of a higher level did not take the place of the least recently used below it")
	}
	if !c.wants(1, size, false) {
		t.Fatal("a full cache does not want a node of its lowest level")
	}
	if c.wants(0, size, true) {
		t.Fatal("a full cache of levels above 0 wants a root of level 0")
	}
	zero := mk(0, "z")
	c.add(zero.id, zero.n, zero.cost)
	if c.get(zero.id) != nil {
		t.Fatal("a node below every level of a full cache was kept")
	}
	if c.wants(0, size, false) {
		t.Fatal("the cache wants a node of level 0 that is no root")
	}
	if c.wants(3, c.budget/8+1, false) {
		t.Fatal("the cache wants a node of more than an eighth of its budget")
	}
	c.check(t)
}

// TestACachedNodeIsCheckedAgainstItsParent: a node the cache holds is
// refused under a parent whose level is not above its own, as a decoded
// one is.
func TestACachedNodeIsCheckedAgainstItsParent(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	tr := build(t, s, randomEntries(rand.New(rand.NewPCG(7, 8)), 500))
	ct := Load(s, tr.Root(), WithCache(NewCache(1<<20)))
	n, err := ct.load(ctx, tr.Root(), noLevel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ct.load(ctx, tr.Root(), n.level); !errors.Is(err, ErrMalformed) {
		t.Fatalf("a cached root read under its own level: %v", err)
	}
}

// TestACacheKeepsOnlyNodesOfItsCIDs: a node named by a CID of another
// kind than the ones this package writes is not kept.
func TestACacheKeepsOnlyNodesOfItsCIDs(t *testing.T) {
	h, err := mh.Sum([]byte("node"), mh.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	v0 := cid.NewCidV0(h)
	c := NewCache(1 << 20)
	c.add(v0, &node{kids: make([]ref, 1)}, 100)
	if c.get(v0) != nil || c.Used() != 0 {
		t.Fatal("a CIDv0 node was kept")
	}
}

func blockData(t *testing.T, nd ipld.Node) []byte {
	t.Helper()
	data, err := BlockData(nd)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
