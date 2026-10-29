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

// differCase is a 200k-key tree and a copy of it with k changes.
func differCase(t *testing.T, a *Tree, rng *rand.Rand, k int) *Tree {
	t.Helper()
	b := a
	for range k {
		i := rng.IntN(200_000)
		b = put(t, b, key(i), []byte("x"))
	}
	return b
}

// TestAReusedDifferAgreesWithDiffNodes: a Differ that compares tree after
// tree in the same storage finds the nodes DiffNodes finds, every time.
func TestAReusedDifferAgreesWithDiffNodes(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := bigTree(t, s)
	rng := rand.New(rand.NewPCG(7, 8))
	var d Differ
	for _, k := range []int{1, 10, 100, 10, 1} {
		b := differCase(t, a, rng, k)
		for _, pair := range [][2]*Tree{{a, b}, {b, a}} {
			onlyA, onlyB, err := d.Nodes(ctx, pair[0], pair[1])
			if err != nil {
				t.Fatal(err)
			}
			wantA, wantB, err := DiffNodes(ctx, pair[0], pair[1])
			if err != nil {
				t.Fatal(err)
			}
			if !sameList(onlyA, wantA) || !sameList(onlyB, wantB) {
				t.Fatalf("%d changes: a reused Differ disagrees with DiffNodes", k)
			}
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
