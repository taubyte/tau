package statetree

import (
	"bytes"
	"context"
	"math/rand/v2"
	"sync"
	"testing"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

func TestEditorIsOneAtATime(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	sum := WithSummarizer(maxSummary{})
	rng := rand.New(rand.NewPCG(3, 4))
	m := model{}
	for i := range 2000 {
		m[string(key(i))] = stamped(uint64(i))
	}
	base := build(t, s, m, sum)
	for round := range 40 {
		ed := base.Edit()
		one := base
		want := model{}
		for k, v := range m {
			want[k] = v
		}
		for range 1 + rng.IntN(300) {
			k := key(rng.IntN(3000))
			switch rng.IntN(4) {
			case 0:
				if err := ed.Delete(ctx, k); err != nil {
					t.Fatal(err)
				}
				one = del(t, one, k)
				delete(want, string(k))
			case 1:
				v, ok := want[string(k)]
				if !ok {
					v = stamped(7)
				}
				if err := ed.Put(ctx, k, v); err != nil {
					t.Fatal(err)
				}
				one = put(t, one, k, v)
				want[string(k)] = v
			default:
				v := stamped(rng.Uint64N(1 << 40))
				if err := ed.Put(ctx, k, v); err != nil {
					t.Fatal(err)
				}
				one = put(t, one, k, v)
				want[string(k)] = v
			}
			k = key(rng.IntN(3000))
			v, has, err := ed.Get(ctx, k)
			if err != nil {
				t.Fatal(err)
			}
			if w, ok := want[string(k)]; has != ok || !bytes.Equal(v, w) {
				t.Fatalf("round %d: Get %s = %x %v, want %x %v", round, k, v, has, w, ok)
			}
			if rng.IntN(50) == 0 {
				if _, err := ed.Tree(ctx); err != nil {
					t.Fatal(err)
				}
			}
		}
		got, err := ed.Tree(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got.Root() != one.Root() {
			t.Fatalf("round %d: root %s, one at a time %s", round, got.Root(), one.Root())
		}
		verify(t, got)
		if g := entries(t, got); len(g) != len(want) {
			t.Fatalf("round %d: %d entries, want %d", round, len(g), len(want))
		}
	}
	ed := base.Edit()
	for k := range m {
		if err := ed.Delete(ctx, []byte(k)); err != nil {
			t.Fatal(err)
		}
	}
	empty, err := ed.Tree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Root().Defined() {
		t.Fatalf("emptied tree has root %s", empty.Root())
	}
}

type perCid struct {
	*counting
	mu    sync.Mutex
	reads map[cid.Cid]int
}

func (p *perCid) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	p.mu.Lock()
	p.reads[c]++
	p.mu.Unlock()
	return p.counting.Get(ctx, c)
}

func TestEditorWritesEachNodeOnce(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	sum := WithSummarizer(maxSummary{})
	m := map[string][]byte{}
	for i := range 5000 {
		m[string(key(i))] = stamped(uint64(i))
	}
	base := build(t, s, m, sum)
	old, _ := nodes(t, s, base.Root())
	pc := &perCid{counting: s, reads: map[cid.Cid]int{}}
	ed := Load(pc, base.Root(), sum).Edit()
	rng := rand.New(rand.NewPCG(5, 6))
	s.reset()
	for range 400 {
		k := key(rng.IntN(5000))
		if rng.IntN(2) == 0 {
			if err := ed.Delete(ctx, k); err != nil {
				t.Fatal(err)
			}
		} else if err := ed.Put(ctx, k, stamped(rng.Uint64())); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ed.Get(ctx, key(rng.IntN(5000))); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ed.Tree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := nodes(t, s, got.Root())
	fresh := 0
	for c := range all {
		if _, ok := old[c]; !ok {
			fresh++
		}
	}
	if adds := int(s.adds.Load()); adds != fresh {
		t.Fatalf("wrote %d nodes, the new tree has %d new ones", adds, fresh)
	}
	for c, n := range pc.reads {
		if n > 1 {
			t.Fatalf("read %s %d times", c, n)
		}
	}
}
