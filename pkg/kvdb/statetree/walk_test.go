package statetree

import (
	"context"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"testing"

	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

func TestReachable(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	rng := rand.New(rand.NewPCG(7, 8))
	tr := New(s)
	var roots []*Tree
	for i := range 3000 {
		if rng.IntN(4) == 0 {
			tr = del(t, tr, key(rng.IntN(i+1)))
		} else {
			tr = put(t, tr, key(rng.IntN(5000)), value(i))
		}
		if i%250 == 249 {
			roots = append(roots, tr)
		}
	}
	live := []*Tree{roots[1], roots[4], roots[len(roots)-1]}
	want := map[cid.Cid]struct{}{}
	for _, r := range live {
		ns, _ := nodes(t, s, r.Root())
		for c := range ns {
			want[c] = struct{}{}
		}
	}
	got, err := Reachable(ctx, s, live[0].Root(), live[1].Root(), live[2].Root(), cid.Undef)
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != len(want) {
		t.Fatalf("reachable %d nodes, want %d", got.Len(), len(want))
	}
	for c := range want {
		if !got.Has(c) {
			t.Fatalf("%s is reachable and was not found", c)
		}
	}
	for _, r := range roots {
		verify(t, r)
	}
	keys, err := s.bs.AllKeysChan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The blockstore keys blocks by multihash and lists them as raw CIDs,
	// which a node's is.
	var dead []cid.Cid
	for c := range keys {
		if !got.Has(c) {
			dead = append(dead, c)
		}
	}
	if err := s.RemoveMany(ctx, dead); err != nil {
		t.Fatal(err)
	}
	for _, r := range live {
		verify(t, r)
	}
	if err := roots[0].Verify(ctx); !ipld.IsNotFound(err) {
		t.Fatalf("a collected root: %v", err)
	}
}

func TestMissing(t *testing.T) {
	ctx := context.Background()
	src := newStore()
	m := map[string][]byte{}
	for i := range 5000 {
		m[string(key(i))] = value(i)
	}
	tr := build(t, src, m)
	_, depth := nodes(t, src, tr.Root())
	dst := newStore()
	fetch := func(cs ...cid.Cid) {
		for _, c := range cs {
			nd, err := src.Get(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if err := dst.Add(ctx, nd); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := Missing(ctx, dst, tr.Root(), nil)
	if err != nil || len(got) != 1 || got[0] != tr.Root() {
		t.Fatalf("Missing on an empty store = %v, %v", got, err)
	}
	rounds := 0
	for len(got) > 0 {
		fetch(got...)
		rounds++
		if got, err = Missing(ctx, dst, tr.Root(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if rounds != depth {
		t.Fatalf("closure complete after %d rounds, depth %d", rounds, depth)
	}
	verify(t, Load(dst, tr.Root()))
	dst.reset()
	if got, err := Missing(ctx, dst, tr.Root(), func(cid.Cid) bool { return true }); err != nil || len(got) != 0 || dst.gets.Load() != 0 {
		t.Fatalf("Missing of a complete root: %v %v, %d gets", got, err, dst.gets.Load())
	}
	if got, err := Missing(ctx, dst, cid.Undef, nil); err != nil || got != nil {
		t.Fatalf("Missing of the empty tree: %v %v", got, err)
	}
	partial := newStore()
	nd, err := src.Get(ctx, tr.Root())
	if err != nil {
		t.Fatal(err)
	}
	if err := partial.Add(ctx, nd); err != nil {
		t.Fatal(err)
	}
	pt := Load(partial, tr.Root())
	if _, _, err := pt.Get(ctx, key(1)); !ipld.IsNotFound(err) {
		t.Fatalf("Get through an absent node: %v", err)
	}
	if err := Diff(ctx, pt, New(partial), nil); !ipld.IsNotFound(err) {
		t.Fatalf("Diff through an absent node: %v", err)
	}
	if _, err := pt.Put(ctx, key(1), nil); !ipld.IsNotFound(err) {
		t.Fatalf("Put through an absent node: %v", err)
	}
}

type failing struct {
	ipld.DAGService
	ok int
}

var errStore = errors.New("store failed")

func (f *failing) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	if f.ok == 0 {
		return nil, errStore
	}
	f.ok--
	return f.DAGService.Get(ctx, c)
}

func (f *failing) AddMany(ctx context.Context, nds []ipld.Node) error {
	if f.ok == 0 {
		return errStore
	}
	f.ok--
	return f.DAGService.AddMany(ctx, nds)
}

func TestStoreErrors(t *testing.T) {
	ctx := context.Background()
	base := mdutils.Mock()
	sum := WithSummarizer(maxSummary{})
	m := map[string][]byte{}
	for i := range 2000 {
		m[string(key(i))] = value(i)
	}
	full := build(t, base, m, sum)
	other := put(t, del(t, full, key(10)), key(1_000_001), []byte("x"))
	ops := map[string]func(*Tree) error{
		"get":    func(tr *Tree) error { _, _, err := tr.Get(ctx, key(1999)); return err },
		"put":    func(tr *Tree) error { _, err := tr.Put(ctx, key(5000), []byte("x")); return err },
		"insert": func(tr *Tree) error { _, err := tr.Put(ctx, highKey(t, "/high/", 2), []byte("x")); return err },
		"over":   func(tr *Tree) error { _, err := tr.Put(ctx, key(77), []byte("x")); return err },
		"delete": func(tr *Tree) error { _, err := tr.Delete(ctx, key(1000)); return err },
		"high":   func(tr *Tree) error { _, err := tr.Delete(ctx, firstAt(t, m, 1)); return err },
		"range":  func(tr *Tree) error { return tr.Range(ctx, nil, nil, func(k, v []byte) error { return nil }) },
		"sum":    func(tr *Tree) error { _, err := tr.Summary(ctx); return err },
		"verify": func(tr *Tree) error { return tr.Verify(ctx) },
		"diff":   func(tr *Tree) error { return Diff(ctx, tr, Load(base, other.Root(), sum), nil) },
		"diff2":  func(tr *Tree) error { return Diff(ctx, Load(base, other.Root(), sum), tr, nil) },
		"nodes":  func(tr *Tree) error { _, _, err := DiffNodes(ctx, tr, Load(base, other.Root(), sum)); return err },
		"reach":  func(tr *Tree) error { _, err := Reachable(ctx, tr.dags, tr.Root()); return err },
		"miss":   func(tr *Tree) error { _, err := Missing(ctx, tr.dags, tr.Root(), nil); return err },
		"build": func(tr *Tree) error {
			_, err := Build(ctx, tr.dags, sortedSeq(m), sum)
			return err
		},
	}
	for name, run := range ops {
		failed := false
		for ok := 0; ok < 10_000; ok++ {
			err := run(Load(&failing{DAGService: base, ok: ok}, full.Root(), sum))
			if err == nil {
				break
			}
			if !errors.Is(err, errStore) {
				t.Fatalf("%s after %d reads: %v", name, ok, err)
			}
			failed = true
		}
		if !failed {
			t.Fatalf("%s never read the store", name)
		}
	}
}

func highKey(t *testing.T, prefix string, l uint32) []byte {
	t.Helper()
	for i := 0; ; i++ {
		k := binary.BigEndian.AppendUint64([]byte(prefix), uint64(i))
		if Level(k) >= l {
			return k
		}
	}
}

func firstAt(t *testing.T, m map[string][]byte, l uint32) []byte {
	t.Helper()
	for k := range m {
		if Level([]byte(k)) == l {
			return []byte(k)
		}
	}
	t.Fatalf("no key at level %d", l)
	return nil
}
