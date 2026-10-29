package mst

import (
	"context"
	"math/rand/v2"
	"testing"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// recording is a DAGService that records the CIDs it is asked for.
type recording struct {
	ipld.DAGService
	got []cid.Cid
}

func (r *recording) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	r.got = append(r.got, c)
	return r.DAGService.Get(ctx, c)
}

// TestDifferAllocatesOnlyItsReads: a Differ that has compared two trees
// once compares them again allocating no more than reading the nodes it
// reads does. DiffNodes allocated its cursors, their stacks, the opened
// lists, a map of one of them and both results on every call.
func TestDifferAllocatesOnlyItsReads(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := bigTree(t, s)
	rng := rand.New(rand.NewPCG(7, 8))
	for _, k := range []int{1, 10, 100} {
		b := a
		for range k {
			i := rng.IntN(200_000)
			b = put(t, b, key(i), []byte("x"))
		}
		rec := &recording{DAGService: s}
		ra, rb := Load(rec, a.Root()), Load(rec, b.Root())
		var d Differ
		onlyA, onlyB, err := d.Nodes(ctx, ra, rb)
		if err != nil {
			t.Fatal(err)
		}
		wantA, wantB, err := DiffNodes(ctx, a, b)
		if err != nil {
			t.Fatal(err)
		}
		if !sameList(onlyA, wantA) || !sameList(onlyB, wantB) {
			t.Fatalf("%d changes: a reused Differ disagrees with DiffNodes", k)
		}
		reads := append([]cid.Cid(nil), rec.got...)
		diff := testing.AllocsPerRun(20, func() {
			rec.got = rec.got[:0]
			if _, _, err := d.Nodes(ctx, ra, rb); err != nil {
				t.Fatal(err)
			}
		})
		load := testing.AllocsPerRun(20, func() {
			rec.got = rec.got[:0]
			for _, c := range reads {
				if _, err := ra.load(ctx, c, noLevel); err != nil {
					t.Fatal(err)
				}
			}
		})
		t.Logf("%d changes: %d reads; the diff allocates %.0f times, its reads %.0f", k, len(reads), diff, load)
		if diff > load {
			t.Errorf("%d changes: the diff allocates %.0f times, %.0f more than its %d reads", k, diff, diff-load, len(reads))
		}
	}
}

func sameList(got, want []cid.Cid) bool {
	in := map[cid.Cid]struct{}{}
	for _, c := range want {
		in[c] = struct{}{}
	}
	if len(got) != len(in) {
		return false
	}
	for _, c := range got {
		if _, ok := in[c]; !ok {
			return false
		}
	}
	return true
}
