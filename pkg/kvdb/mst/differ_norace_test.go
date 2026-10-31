//go:build !race

// The race detector makes sync.Pool drop what is put in it at random, and
// decoding a node takes its message from one, so allocations are counted
// only without it.

package mst

import (
	"context"
	"math/rand/v2"
	"runtime/debug"
	"testing"

	cid "github.com/ipfs/go-cid"
)

// TestDifferAllocatesOnlyItsReads: a Differ that has compared two trees
// once compares them again allocating no more than reading the nodes it
// reads does. DiffNodes allocated its cursors, their stacks, the opened
// lists, a map of one of them and both results on every call.
func TestDifferAllocatesOnlyItsReads(t *testing.T) {
	// A collection empties sync.Pool, and either count could take it: the
	// two are counted with none.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	ctx := context.Background()
	s := newStore()
	a, _ := bigTree(t, s)
	rng := rand.New(rand.NewPCG(7, 8))
	for _, k := range []int{1, 10, 100} {
		b := differCase(t, a, rng, k)
		rec := &recording{DAGService: s}
		ra, rb := Load(rec, a.Root()), Load(rec, b.Root())
		var d Differ
		if _, _, err := d.Nodes(ctx, ra, rb); err != nil {
			t.Fatal(err)
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
