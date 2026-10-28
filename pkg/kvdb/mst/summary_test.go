package mst

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"
)

// maxSummary summarises a run of entries by the largest uint64 their values
// start with, the shape of "nothing in here expires before t".
type maxSummary struct{}

func (maxSummary) Leaf(_, v []byte) []byte {
	var x uint64
	if len(v) >= 8 {
		x = binary.BigEndian.Uint64(v)
	}
	return binary.BigEndian.AppendUint64(nil, x)
}

func (maxSummary) Merge(a, b []byte) []byte {
	return binary.BigEndian.AppendUint64(nil, max(binary.BigEndian.Uint64(a), binary.BigEndian.Uint64(b)))
}

func stamped(at uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, at)
}

func TestSummary(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	sum := WithSummarizer(maxSummary{})
	rng := rand.New(rand.NewPCG(9, 10))
	m := map[string][]byte{}
	for i := range 4000 {
		m[string(key(i))] = stamped(uint64(rng.IntN(1_000_000)))
	}
	ref := build(t, s, m, sum)
	verify(t, ref)
	if plain := build(t, s, m); plain.Root() == ref.Root() {
		t.Fatal("a summarised tree has the root of an unsummarised one")
	}
	// Summaries are a function of the entries: Puts in any order, through
	// overwrites and deletes, make the same tree.
	tr := New(s, sum)
	for _, i := range rng.Perm(4000) {
		tr = put(t, tr, key(i), stamped(0))
		tr = put(t, tr, key(i+10_000), stamped(1<<40))
		tr = put(t, tr, key(i), m[string(key(i))])
	}
	for i := range 4000 {
		tr = del(t, tr, key(i+10_000))
	}
	if tr.Root() != ref.Root() {
		t.Fatalf("root %s, want %s", tr.Root(), ref.Root())
	}
	verify(t, tr)

	var top uint64
	for _, v := range m {
		top = max(top, binary.BigEndian.Uint64(v))
	}
	if got, err := tr.Summary(ctx); err != nil || binary.BigEndian.Uint64(got) != top {
		t.Fatalf("Summary = %x %v, want %d", got, err, top)
	}

	// Skipping subtrees whose newest stamp is old enough reads fewer nodes
	// and still finds every entry past the cut.
	wide := map[string][]byte{}
	for i := range 20_000 {
		wide[string(key(i))] = stamped(uint64(rng.IntN(1_000_000)))
	}
	big := build(t, s, wide, sum)
	cut := uint64(999_000)
	s.reset()
	var found []string
	if err := big.Scan(ctx, nil, nil, func(sum []byte) bool {
		return binary.BigEndian.Uint64(sum) < cut
	}, func(k, v []byte) error {
		if binary.BigEndian.Uint64(v) >= cut {
			found = append(found, string(k))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := s.gets.Load()
	var want []string
	for _, k := range slices.Sorted(maps.Keys(wide)) {
		if binary.BigEndian.Uint64(wide[k]) >= cut {
			want = append(want, k)
		}
	}
	if len(want) == 0 || !slices.Equal(found, want) {
		t.Fatalf("Scan found %q, want %q", found, want)
	}
	all, _ := nodes(t, s, big.Root())
	if read*4 > int64(len(all)) {
		t.Fatalf("Scan read %d of %d nodes", read, len(all))
	}
	t.Logf("scan for %d of %d entries read %d of %d nodes", len(want), len(wide), read, len(all))

	// A tree read with another summarizer, or none, is refused.
	if err := Load(s, tr.Root()).Verify(ctx); !errors.Is(err, ErrMalformed) {
		t.Fatalf("Verify without the summarizer: %v", err)
	}
	if err := Load(s, tr.Root(), WithSummarizer(otherSummary{})).Verify(ctx); !errors.Is(err, ErrMalformed) {
		t.Fatalf("Verify with another summarizer: %v", err)
	}
	if got, err := New(s, sum).Summary(ctx); got != nil || err != nil {
		t.Fatalf("Summary of the empty tree: %x %v", got, err)
	}
	if got, err := Load(s, tr.Root()).Summary(ctx); got != nil || err != nil {
		t.Fatalf("Summary without a summarizer: %x %v", got, err)
	}
}

type otherSummary struct{ maxSummary }

func (otherSummary) Merge(a, b []byte) []byte { return append(bytes.Clone(a), 0) }

func TestBuild(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	empty, err := Build(ctx, s, sortedSeq(nil))
	if err != nil || empty.Root().Defined() {
		t.Fatalf("Build of nothing: %v %v", empty, err)
	}
	pairs := func(kvs ...string) func(func([]byte, []byte) bool) {
		return func(yield func([]byte, []byte) bool) {
			for i := 0; i < len(kvs); i += 2 {
				if !yield([]byte(kvs[i]), []byte(kvs[i+1])) {
					return
				}
			}
		}
	}
	if _, err := Build(ctx, s, pairs("a", "1", "a", "2")); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("a duplicate key with another value: %v", err)
	}
	if _, err := Build(ctx, s, pairs("a", "1", "a", "1")); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("a duplicate key with the same value: %v", err)
	}
	if _, err := Build(ctx, s, pairs("b", "1", "a", "1")); !errors.Is(err, ErrUnsorted) {
		t.Fatalf("keys out of order: %v", err)
	}
	// The iterator's buffers are its own: Build copies what it keeps.
	buf := make([]byte, 8)
	reused := func(yield func([]byte, []byte) bool) {
		for i := range 3000 {
			binary.BigEndian.PutUint64(buf, uint64(i))
			if !yield(buf, buf) {
				return
			}
		}
	}
	tr, err := Build(ctx, s, reused)
	if err != nil {
		t.Fatal(err)
	}
	verify(t, tr)
	want := New(s)
	for i := range 3000 {
		k := binary.BigEndian.AppendUint64(nil, uint64(i))
		want = put(t, want, k, k)
	}
	if tr.Root() != want.Root() {
		t.Fatalf("built root %s, put root %s", tr.Root(), want.Root())
	}
	// A large build adds its nodes in batches.
	s.reset()
	big, _ := bigTree(t, s)
	ns, _ := nodes(t, s, big.Root())
	if int(s.adds.Load()) != len(ns) {
		t.Fatalf("build wrote %d nodes for a tree of %d", s.adds.Load(), len(ns))
	}
}

// TestARootSplitKeepsItsSummary puts a key above every level in a tree,
// before and after all of its keys: the old root becomes a whole subtree of
// the new one, which must carry its summary as Build writes it.
func TestARootSplitKeepsItsSummary(t *testing.T) {
	ctx := context.Background()
	find := func(first byte, ok func(uint32) bool, n int) [][]byte {
		var out [][]byte
		for i := 0; len(out) < n; i++ {
			if k := []byte{first, byte(i >> 8), byte(i)}; ok(Level(k)) {
				out = append(out, k)
			}
		}
		return out
	}
	low := find(0x80, func(l uint32) bool { return l == 0 }, 40)
	above := func(l uint32) bool { return l >= 1 }
	for _, top := range [][]byte{find(0x00, above, 1)[0], find(0xff, above, 1)[0]} {
		s := newStore()
		tr := New(s, WithSummarizer(maxSummary{}))
		want := map[string][]byte{}
		var err error
		for i, k := range low {
			if tr, err = tr.Put(ctx, k, stamped(uint64(i))); err != nil {
				t.Fatal(err)
			}
			want[string(k)] = stamped(uint64(i))
		}
		if tr, err = tr.Put(ctx, top, stamped(99)); err != nil {
			t.Fatal(err)
		}
		want[string(top)] = stamped(99)
		if err := tr.Verify(ctx); err != nil {
			t.Errorf("after putting %x: %v", top, err)
		}
		if built := build(t, s, want, WithSummarizer(maxSummary{})); tr.Root() != built.Root() {
			t.Errorf("after putting %x the root is %s, Build gives %s", top, tr.Root(), built.Root())
		}
	}
}
