package statetree

import (
	"bytes"
	"context"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

var _ ipld.NodeGetter = ipld.DAGService(nil)

func TestHistoryIndependence(t *testing.T) {
	const n = 3000
	s := newStore()
	want := map[string][]byte{}
	for i := range n {
		want[string(key(i))] = value(i)
	}
	ref := build(t, s, want)
	verify(t, ref)
	rng := rand.New(rand.NewPCG(1, 2))
	for round := range 12 {
		order := rng.Perm(n)
		tr := New(s)
		for _, i := range order {
			tr = put(t, tr, key(i), value(i))
		}
		if tr.Root() != ref.Root() {
			t.Fatalf("order %d: root %s, want %s", round, tr.Root(), ref.Root())
		}
	}
	// Through keys that come and go, values that are overwritten, and
	// deletes of keys never held.
	for round := range 8 {
		tr := New(s)
		for _, i := range rng.Perm(n + n/2) {
			switch {
			case i >= n:
				tr = put(t, tr, key(i), []byte("transient"))
			case rng.IntN(3) == 0:
				tr = put(t, tr, key(i), []byte("stale"))
				tr = put(t, tr, key(i), value(i))
			default:
				tr = put(t, tr, key(i), value(i))
			}
			if rng.IntN(5) == 0 {
				tr = del(t, tr, key(n*2+rng.IntN(n)))
			}
		}
		for _, i := range rng.Perm(n / 2) {
			tr = del(t, tr, key(n+i))
		}
		if tr.Root() != ref.Root() {
			t.Fatalf("mixed %d: root %s, want %s", round, tr.Root(), ref.Root())
		}
		verify(t, tr)
	}
	// Emptied in any order, a tree is the empty tree again.
	tr := ref
	for _, i := range rng.Perm(n) {
		tr = del(t, tr, key(i))
	}
	if tr.Root().Defined() {
		t.Fatalf("emptied tree has root %s", tr.Root())
	}
}

type model map[string][]byte

func (m model) diff(next model) []Change {
	var out []Change
	for _, k := range slices.Sorted(maps.Keys(unionKeys(m, next))) {
		o, inO := m[k]
		n, inN := next[k]
		switch {
		case inO && !inN:
			out = append(out, Change{Kind: Removed, Key: []byte(k), Old: o})
		case !inO && inN:
			out = append(out, Change{Kind: Added, Key: []byte(k), New: n})
		case !bytes.Equal(o, n):
			out = append(out, Change{Kind: Changed, Key: []byte(k), Old: o, New: n})
		}
	}
	return out
}

func unionKeys(a, b model) map[string]struct{} {
	u := map[string]struct{}{}
	for k := range a {
		u[k] = struct{}{}
	}
	for k := range b {
		u[k] = struct{}{}
	}
	return u
}

func diffOf(t testing.TB, a, b *Tree) []Change {
	t.Helper()
	var got []Change
	if err := Diff(context.Background(), a, b, func(c Change) error {
		got = append(got, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func sameChanges(a, b []Change) bool {
	return slices.EqualFunc(a, b, func(x, y Change) bool {
		return x.Kind == y.Kind && bytes.Equal(x.Key, y.Key) && bytes.Equal(x.Old, y.Old) && bytes.Equal(x.New, y.New)
	})
}

func rangeOf(t testing.TB, tr *Tree, start, end []byte) []string {
	t.Helper()
	var got []string
	if err := tr.Range(context.Background(), start, end, func(k, v []byte) error {
		got = append(got, string(k)+"="+string(v))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func modelRange(m model, start, end []byte) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if (start == nil || k >= string(start)) && (end == nil || k < string(end)) {
			out = append(out, k+"="+string(m[k]))
		}
	}
	return out
}

func randKey(rng *rand.Rand) []byte {
	// A small alphabet and short keys, so keys share prefixes, collide, and
	// include the empty key and keys of high level.
	k := make([]byte, rng.IntN(5))
	for i := range k {
		k[i] = "ab\x00\xff/"[rng.IntN(5)]
	}
	return k
}

func TestModel(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(3, 4))
	s := newStore()
	tr, m := New(s), model{}
	var snaps []*Tree
	var models []model
	for step := range 4000 {
		k := randKey(rng)
		if rng.IntN(3) == 0 {
			tr = del(t, tr, k)
			delete(m, string(k))
		} else {
			v := []byte{byte(rng.IntN(4))}
			if rng.IntN(4) == 0 {
				v = nil
			}
			tr = put(t, tr, k, v)
			m[string(k)] = v
		}
		if step%100 != 99 {
			continue
		}
		verify(t, tr)
		for _, k := range append(slices.Collect(maps.Keys(m)), "zz", "a\x00\x00\x00\x00\x00") {
			v, ok, err := tr.Get(ctx, []byte(k))
			mv, mok := m[k]
			if err != nil || ok != mok || !bytes.Equal(v, mv) {
				t.Fatalf("Get(%q) = %q %v %v, want %q %v", k, v, ok, err, mv, mok)
			}
		}
		for range 20 {
			start, end := randKey(rng), randKey(rng)
			if rng.IntN(4) == 0 {
				start = nil
			}
			if rng.IntN(4) == 0 {
				end = nil
			}
			if got, want := rangeOf(t, tr, start, end), modelRange(m, start, end); !slices.Equal(got, want) {
				t.Fatalf("Range(%q, %q) = %q, want %q", start, end, got, want)
			}
			p := randKey(rng)
			var got []string
			if err := tr.Prefix(ctx, p, func(k, v []byte) error {
				got = append(got, string(k)+"="+string(v))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, kv := range modelRange(m, nil, nil) {
				if bytes.HasPrefix([]byte(kv), p) {
					want = append(want, kv)
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("Prefix(%q) = %q, want %q", p, got, want)
			}
		}
		if b := build(t, s, m); b.Root() != tr.Root() {
			t.Fatalf("step %d: built root %s, put root %s", step, b.Root(), tr.Root())
		}
		snaps = append(snaps, tr)
		models = append(models, maps.Clone(m))
	}
	for range 200 {
		i, j := rng.IntN(len(snaps)), rng.IntN(len(snaps))
		if got, want := diffOf(t, snaps[i], snaps[j]), models[i].diff(models[j]); !sameChanges(got, want) {
			t.Fatalf("Diff(%d, %d) = %v, want %v", i, j, got, want)
		}
	}
	if got, want := diffOf(t, New(s), tr), (model{}).diff(m); !sameChanges(got, want) {
		t.Fatalf("Diff(empty, tree) = %v, want %v", got, want)
	}
}

func TestRangeStops(t *testing.T) {
	s := newStore()
	m := map[string][]byte{}
	for i := range 500 {
		m[string(key(i))] = value(i)
	}
	tr := build(t, s, m)
	seen := 0
	if err := tr.Range(context.Background(), nil, nil, func(k, v []byte) error {
		seen++
		if seen == 10 {
			return Stop
		}
		return nil
	}); err != nil || seen != 10 {
		t.Fatalf("Range after Stop: %d entries, %v", seen, err)
	}
	if got := prefixEnd([]byte{0xff, 0xff}); got != nil {
		t.Fatalf("prefixEnd(ffff) = %x", got)
	}
	if got := prefixEnd([]byte{'a', 0xff}); !bytes.Equal(got, []byte{'b'}) {
		t.Fatalf("prefixEnd(a ff) = %x", got)
	}
}

func TestPutUnchangedWritesNothing(t *testing.T) {
	s := newStore()
	tr := put(t, New(s), []byte("a"), []byte("1"))
	s.reset()
	if same := put(t, tr, []byte("a"), []byte("1")); same != tr {
		t.Fatal("rewriting a value made a new tree")
	}
	if same := del(t, tr, []byte("b")); same != tr {
		t.Fatal("deleting an absent key made a new tree")
	}
	if a := s.adds.Load(); a != 0 {
		t.Fatalf("%d nodes written", a)
	}
	v, ok, err := New(s).Get(context.Background(), []byte("a"))
	if v != nil || ok || err != nil {
		t.Fatalf("Get on the empty tree: %q %v %v", v, ok, err)
	}
}

// The 100k tree the sharing and diff tests change, built once.
func bigTree(t testing.TB, s *counting) (*Tree, map[string][]byte) {
	m := map[string][]byte{}
	for i := range 100_000 {
		m[string(key(i*2))] = value(i * 2)
	}
	return build(t, s, m), m
}

func TestStructuralSharing(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	tr, _ := bigTree(t, s)
	before, depth := nodes(t, s, tr.Root())
	t.Logf("100k keys: %d nodes, depth %d", len(before), depth)
	check := func(name string, next *Tree, limit int) {
		t.Helper()
		after, _ := nodes(t, s, next.Root())
		added := 0
		for c := range after {
			if _, ok := before[c]; !ok {
				added++
			}
		}
		wrote := int(s.adds.Load())
		if added != wrote {
			t.Fatalf("%s: wrote %d nodes, %d of the new tree's are new", name, wrote, added)
		}
		if wrote > limit {
			t.Fatalf("%s: wrote %d nodes, at most %d allowed at depth %d", name, wrote, limit, depth)
		}
		t.Logf("%s: wrote %d of %d nodes", name, wrote, len(after))
	}
	for _, i := range []int{0, 777, 54_321, 99_999} {
		k := key(i * 2)
		path := pathLen(t, s, tr, k)
		s.reset()
		next := put(t, tr, k, []byte("changed"))
		check("overwrite "+string(k), next, path)
		if int(s.adds.Load()) != path {
			t.Fatalf("overwrite wrote %d nodes, its path has %d", s.adds.Load(), path)
		}
		s.reset()
		check("delete "+string(k), del(t, tr, k), 2*depth)
		k = key(i*2 + 1)
		s.reset()
		check("insert "+string(k), put(t, tr, k, []byte("new")), 2*depth+1)
	}
	// A key above every level, at the edge and in the middle, splits the
	// tree down its whole height.
	for _, prefix := range []string{"/high/", "/k/00100000/"} {
		k := highKey(t, prefix, 5)
		s.reset()
		check("insert above every level at "+prefix, put(t, tr, k, []byte("high")), 2*depth+1)
		high := put(t, tr, k, []byte("high"))
		s.reset()
		if back := del(t, high, k); back.Root() != tr.Root() {
			t.Fatalf("deleting the high key gave %s, not %s", back.Root(), tr.Root())
		}
		if wrote := int(s.adds.Load()); wrote > 2*depth+2 {
			t.Fatalf("deleting the high key wrote %d nodes at depth %d", wrote, depth)
		} else {
			t.Logf("delete above every level at %s: wrote %d nodes", prefix, wrote)
		}
	}
	if _, ok, err := tr.Get(ctx, key(0)); !ok || err != nil {
		t.Fatal("the old tree lost a key")
	}
}

func TestDiffMinimal(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, m := bigTree(t, s)
	all, depth := nodes(t, s, a.Root())
	rng := rand.New(rand.NewPCG(5, 6))
	for _, k := range []int{1, 10, 100} {
		b, next := a, maps.Clone(m)
		for range k {
			i := rng.IntN(200_000)
			switch rng.IntN(3) {
			case 0:
				b = put(t, b, key(i), []byte("x"))
				next[string(key(i))] = []byte("x")
			case 1:
				b = del(t, b, key(i))
				delete(next, string(key(i)))
			default:
				b = put(t, b, key(i), value(i))
				next[string(key(i))] = value(i)
			}
		}
		s.reset()
		got := diffOf(t, a, b)
		gets := int(s.gets.Load())
		if want := model(m).diff(next); !sameChanges(got, want) {
			t.Fatalf("%d changes: diff disagrees with the model", k)
		}
		// Each change opens its path in both trees, plus the spine of
		// one subtree beside it when that subtree has moved: at most
		// a few paths per change, linear in k and depth and far below
		// the tree's size.
		if limit := 6 * k * (depth + 1); gets > limit {
			t.Fatalf("%d changes: %d gets, more than %d (depth %d)", k, gets, limit, depth)
		}
		t.Logf("%d changes: diff read %d nodes of %d, depth %d", k, gets, len(all), depth)

		onlyA, onlyB, err := DiffNodes(ctx, a, b)
		if err != nil {
			t.Fatal(err)
		}
		bs, _ := nodes(t, s, b.Root())
		if !sameSet(onlyA, minusSet(all, bs)) || !sameSet(onlyB, minusSet(bs, all)) {
			t.Fatalf("%d changes: DiffNodes gave %d and %d nodes, want %d and %d", k, len(onlyA), len(onlyB), len(minusSet(all, bs)), len(minusSet(bs, all)))
		}
	}
	// A key above every level splits the tree down its whole height, and
	// the subtrees either side of the split are still matched, not read.
	for _, l := range []uint32{2, 3, 5} {
		k := highKey(t, "/k/00100000/", l)
		b := put(t, a, k, []byte("high"))
		s.reset()
		got := diffOf(t, a, b)
		if len(got) != 1 || got[0].Kind != Added || !bytes.Equal(got[0].Key, k) {
			t.Fatalf("level %d key: diff %v", l, got)
		}
		if gets := int(s.gets.Load()); gets > 6*(depth+1) {
			t.Fatalf("level %d key: %d gets at depth %d", l, gets, depth)
		} else {
			t.Logf("level %d key: diff read %d nodes", l, gets)
		}
	}
	// Equal trees read nothing.
	s.reset()
	if got := diffOf(t, a, Load(s, a.Root())); len(got) != 0 || s.gets.Load() != 0 {
		t.Fatalf("diff of a tree with itself: %d changes, %d gets", len(got), s.gets.Load())
	}
}

func minusSet(a, b map[cid.Cid]struct{}) map[cid.Cid]struct{} {
	out := map[cid.Cid]struct{}{}
	for c := range a {
		if _, ok := b[c]; !ok {
			out[c] = struct{}{}
		}
	}
	return out
}

func sameSet(got []cid.Cid, want map[cid.Cid]struct{}) bool {
	if len(got) != len(want) {
		return false
	}
	for _, c := range got {
		if _, ok := want[c]; !ok {
			return false
		}
	}
	return true
}

func TestDiffAcrossStores(t *testing.T) {
	sa, sb := newStore(), newStore()
	a := put(t, put(t, New(sa), []byte("x"), []byte("1")), []byte("y"), []byte("2"))
	b := put(t, put(t, New(sb), []byte("y"), []byte("3")), []byte("z"), []byte("4"))
	want := []Change{
		{Kind: Removed, Key: []byte("x"), Old: []byte("1")},
		{Kind: Changed, Key: []byte("y"), Old: []byte("2"), New: []byte("3")},
		{Kind: Added, Key: []byte("z"), New: []byte("4")},
	}
	if got := diffOf(t, a, b); !sameChanges(got, want) {
		t.Fatalf("Diff = %v", got)
	}
}
